package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/txchen/git-remote-cloak/internal/domain"
	"github.com/txchen/git-remote-cloak/internal/engine"
	cloakformat "github.com/txchen/git-remote-cloak/internal/format"
	"github.com/txchen/git-remote-cloak/internal/localstate"
	"github.com/txchen/git-remote-cloak/internal/remotehelper"
	"github.com/txchen/git-remote-cloak/internal/secret"
	"golang.org/x/term"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 0 {
		return usageError()
	}
	switch arguments[0] {
	case "version":
		return runVersion(arguments[1:])
	case "init":
		return runInit(arguments[1:])
	case "clone":
		return runClone(arguments[1:])
	case "doctor":
		return runDoctor(arguments[1:])
	case "cache":
		return runCache(arguments[1:])
	case "status":
		return runStatus(arguments[1:])
	case "set-head":
		return runSetHead(arguments[1:])
	case "compact":
		return runCompact(arguments[1:])
	case "rekey":
		return runRekey(arguments[1:])
	case "migrate":
		return runMigrate(arguments[1:])
	default:
		if len(arguments) == 2 {
			recoverySecret, err := acquireSecret("", false)
			if err != nil {
				return err
			}
			autoCompact, err := remoteAutoCompact(arguments[0])
			if err != nil {
				return err
			}
			return remotehelper.RunWithOptions(arguments[1], recoverySecret, os.Stdin, os.Stdout, engine.PublishOptions{
				AutoCompact: autoCompact,
				Progress:    func(message string) { fmt.Fprintln(os.Stderr, message) },
			})
		}
		return usageError()
	}
}

func runMigrate(arguments []string) error {
	remoteName, target, confirmed, dryRun, _, err := parseMigrationArguments(arguments)
	if err != nil {
		return err
	}
	recoverySecret, err := acquireSecret("", false)
	if err != nil {
		return err
	}
	workspace, err := os.Getwd()
	if err != nil {
		return err
	}
	gitDirectory, err := absoluteGitDirectory()
	if err != nil {
		return errors.New("migrate must run inside an Authorized Host Git repository")
	}
	configuredURL, err := exec.Command("git", "-C", workspace, "remote", "get-url", remoteName).Output()
	if err != nil {
		return fmt.Errorf("read Cloak remote %s", remoteName)
	}
	repositoryURL, found := strings.CutPrefix(strings.TrimSpace(string(configuredURL)), "cloak::")
	if !found || repositoryURL == "" {
		return errors.New("configured remote is not a Cloak remote")
	}
	repositoryEngine := engine.NewWithLocalState(gitDirectory)
	plan, err := repositoryEngine.PlanMigration(repositoryURL, target, recoverySecret)
	if err != nil {
		return err
	}
	if dryRun {
		return json.NewEncoder(os.Stdout).Encode(plan)
	}
	fmt.Printf("Format Migration plan: %s -> %s\n", plan.CurrentFormat, plan.TargetFormat)
	fmt.Printf("Authenticated Ciphertext Repository state: %d Logical Refs\n", plan.LogicalRefCount)
	fmt.Printf("Estimated full upload: %d bytes\n", plan.EstimatedFullUploadBytes)
	fmt.Printf("Compatibility check: %s\nCapacity check: %s\n", plan.CompatibilityCheck, plan.CapacityCheck)
	fmt.Printf("Target writer compatibility: %s\n", plan.WriterCompatibilityEffect)
	if !confirmed {
		fmt.Print("Type MIGRATE to publish the parentless target-format Ciphertext Snapshot: ")
		answer, readErr := bufio.NewReader(os.Stdin).ReadString('\n')
		if readErr != nil && len(answer) == 0 {
			return fmt.Errorf("read Format Migration confirmation: %w", readErr)
		}
		if strings.TrimSpace(answer) != "MIGRATE" {
			return errors.New("Format Migration cancelled; Ciphertext Repository was not changed")
		}
	}
	if err := repositoryEngine.Migrate(repositoryURL, plan, recoverySecret, func(phase string) {
		fmt.Printf("%s Format Migration candidate.\n", phase)
	}); err != nil {
		return err
	}
	fmt.Printf("Format Migration published a validated %s Ciphertext Snapshot with one parentless Storage History root.\n", plan.TargetFormat)
	fmt.Println("Warning: Repository Host retention may preserve superseded ciphertext; Format Migration cannot guarantee erasure or immediate quota recovery.")
	return nil
}

func parseMigrationArguments(arguments []string) (remoteName, target string, confirmed, dryRun, structured bool, err error) {
	for index := 0; index < len(arguments); index++ {
		switch arguments[index] {
		case "--to":
			index++
			if index >= len(arguments) || target != "" {
				return "", "", false, false, false, migrationUsageError()
			}
			target = arguments[index]
		case "--yes":
			if confirmed {
				return "", "", false, false, false, migrationUsageError()
			}
			confirmed = true
		case "--dry-run":
			dryRun = true
		case "--json":
			structured = true
		default:
			if strings.HasPrefix(arguments[index], "-") || remoteName != "" {
				return "", "", false, false, false, migrationUsageError()
			}
			remoteName = arguments[index]
		}
	}
	if remoteName == "" || confirmed && target == "" || dryRun && target == "" || dryRun != structured || dryRun && confirmed {
		return "", "", false, false, false, migrationUsageError()
	}
	return remoteName, target, confirmed, dryRun, structured, nil
}

func migrationUsageError() error {
	return errors.New("usage: git-remote-cloak migrate <remote-name> [--to <format> --yes | --to <format> --dry-run --json]")
}

func runRekey(arguments []string) error {
	remoteName, refspecs, confirmed, err := parseRekeyArguments(arguments)
	if err != nil {
		return err
	}
	workspace, err := os.Getwd()
	if err != nil {
		return err
	}
	gitDirectory, err := absoluteGitDirectory()
	if err != nil {
		return errors.New("rekey must run inside a complete local Logical Repository")
	}
	configuredURL, err := exec.Command("git", "-C", workspace, "remote", "get-url", remoteName).Output()
	if err != nil {
		return fmt.Errorf("read Cloak remote %s", remoteName)
	}
	repositoryURL, found := strings.CutPrefix(strings.TrimSpace(string(configuredURL)), "cloak::")
	if !found || repositoryURL == "" {
		return errors.New("configured remote is not a Cloak remote")
	}
	repositoryEngine := engine.NewWithLocalState(gitDirectory)
	plan, err := repositoryEngine.PlanRekey(gitDirectory, refspecs)
	if err != nil {
		return err
	}
	fmt.Println("Rekey will replace the Ciphertext Repository from these selected Logical Refs:")
	for _, name := range plan.SelectedRefs {
		fmt.Printf("  %s\n", name)
	}
	for _, name := range plan.RemoteTrackingWithoutLocal {
		fmt.Printf("Warning: remote-tracking branch %s has no corresponding local branch and will not be selected.\n", name)
	}
	input := bufio.NewReader(os.Stdin)
	if !confirmed {
		fmt.Print("Type REKEY to replace the Ciphertext Repository: ")
		answer, readErr := input.ReadString('\n')
		if readErr != nil && len(answer) == 0 {
			return fmt.Errorf("read Rekey confirmation: %w", readErr)
		}
		if strings.TrimSpace(answer) != "REKEY" {
			return errors.New("Rekey cancelled; Ciphertext Repository was not changed")
		}
	}
	newSecret, err := acquireNewSecret(input)
	if err != nil {
		return err
	}
	result, err := repositoryEngine.Rekey(repositoryURL, gitDirectory, plan, newSecret, func(phase string) {
		fmt.Printf("%s Rekey candidate.\n", phase)
	})
	if err != nil {
		return err
	}
	if _, err := exec.Command("git", "-C", workspace, "config", "remote."+remoteName+".cloakRepositoryID", fmt.Sprintf("%x", result.RepositoryID[:])).CombinedOutput(); err != nil {
		return fmt.Errorf("record new public Repository ID: %w", err)
	}
	fmt.Println("Rekey published a validated generation-one Ciphertext Snapshot with a new Repository ID.")
	fmt.Println("Warning: Repository Host retention may preserve ciphertext recoverable with the old Recovery Secret; Rekey cannot guarantee erasure or immediate quota recovery.")
	return nil
}

func parseRekeyArguments(arguments []string) (remoteName string, refspecs []string, confirmed bool, err error) {
	for index := 0; index < len(arguments); index++ {
		switch arguments[index] {
		case "--yes":
			if confirmed {
				return "", nil, false, errors.New("usage: git-remote-cloak rekey <remote-name> [refspec ...] [--yes]")
			}
			confirmed = true
		default:
			if strings.HasPrefix(arguments[index], "-") {
				return "", nil, false, errors.New("usage: git-remote-cloak rekey <remote-name> [refspec ...] [--yes]")
			}
			if remoteName == "" {
				remoteName = arguments[index]
			} else {
				refspecs = append(refspecs, arguments[index])
			}
		}
	}
	if remoteName == "" {
		return "", nil, false, errors.New("usage: git-remote-cloak rekey <remote-name> [refspec ...] [--yes]")
	}
	return remoteName, refspecs, confirmed, nil
}

func acquireNewSecret(input *bufio.Reader) (domain.RecoverySecret, error) {
	if _, environmentSet := os.LookupEnv("CLOAK_RECOVERY_SECRET"); environmentSet {
		return acquireSecret("", false)
	}
	if _, environmentFileSet := os.LookupEnv("CLOAK_RECOVERY_SECRET_FILE"); environmentFileSet {
		return acquireSecret("", false)
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return domain.RecoverySecret{}, errors.New("non-interactive Rekey requires a configured new Recovery Secret")
	}
	recoverySecret, mnemonic, err := secret.Generate()
	if err != nil {
		return domain.RecoverySecret{}, err
	}
	fmt.Fprintf(os.Stdout, "New Recovery Mnemonic (shown once): %s\nConfirm it is saved by typing SAVED: ", mnemonic)
	confirmation, err := input.ReadString('\n')
	if err != nil {
		return domain.RecoverySecret{}, fmt.Errorf("read Recovery Mnemonic confirmation: %w", err)
	}
	if strings.TrimSpace(confirmation) != "SAVED" {
		return domain.RecoverySecret{}, errors.New("new Recovery Mnemonic was not confirmed; Ciphertext Repository was not changed")
	}
	return recoverySecret, nil
}

func remoteAutoCompact(remoteName string) (bool, error) {
	command := exec.Command("git", "config", "--bool", "--get", "remote."+remoteName+".cloakAutoCompact")
	output, err := command.Output()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() == 1 {
			return true, nil
		}
		return false, fmt.Errorf("read automatic Compaction configuration: %w", err)
	}
	switch strings.TrimSpace(string(output)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, errors.New("remote.<name>.cloakAutoCompact must be true or false")
	}
}

func runCompact(arguments []string) error {
	if len(arguments) != 1 {
		return fmt.Errorf("usage: git-remote-cloak compact <remote-name>")
	}
	recoverySecret, err := acquireSecret("", false)
	if err != nil {
		return err
	}
	workspace, err := os.Getwd()
	if err != nil {
		return err
	}
	configuredURL, err := exec.Command("git", "-C", workspace, "remote", "get-url", arguments[0]).Output()
	if err != nil {
		return fmt.Errorf("read Cloak remote %s", arguments[0])
	}
	repositoryURL, found := strings.CutPrefix(strings.TrimSpace(string(configuredURL)), "cloak::")
	if !found || repositoryURL == "" {
		return fmt.Errorf("configured remote is not a Cloak remote")
	}
	gitDirectory, err := absoluteGitDirectory()
	if err != nil {
		return err
	}
	if err := engine.NewWithLocalState(gitDirectory).Compact(repositoryURL, recoverySecret, func(phase string) {
		fmt.Printf("%s Compaction candidate.\n", phase)
	}); err != nil {
		return err
	}
	fmt.Println("Compaction published a validated optimized Ciphertext Snapshot and a parentless Storage History root.")
	fmt.Println("Warning: Repository Host retention and garbage collection may delay quota recovery or physical erasure of superseded ciphertext.")
	return nil
}

func runCache(arguments []string) error {
	if len(arguments) != 1 || arguments[0] != "clear" {
		return fmt.Errorf("usage: git-remote-cloak cache clear")
	}
	command := exec.Command("git", "rev-parse", "--absolute-git-dir")
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("cache clear must run inside a Git repository")
	}
	if err := localstate.ClearCache(strings.TrimSpace(string(output))); err != nil {
		return fmt.Errorf("clear reconstructable cache: %w", err)
	}
	fmt.Println("Cleared reconstructable Cloak cache.")
	return nil
}

func runStatus(arguments []string) error {
	structured := len(arguments) == 1 && arguments[0] == "--json"
	if len(arguments) != 0 && !structured {
		return fmt.Errorf("usage: git-remote-cloak status [--json]")
	}
	gitDirectory, err := absoluteGitDirectory()
	if err != nil {
		return fmt.Errorf("status must run inside a Git repository")
	}
	checkpoint, exists, err := localstate.LoadCheckpoint(gitDirectory)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("no trusted Rollback Checkpoint exists for this Logical Repository")
	}
	status := struct {
		RepositoryID                   string `json:"repository_id"`
		HighestAuthenticatedGeneration uint64 `json:"highest_authenticated_generation"`
		LastSeenStorageCommitID        string `json:"last_seen_storage_commit_id"`
		Freshness                      string `json:"freshness"`
	}{
		RepositoryID: checkpoint.RepositoryID, HighestAuthenticatedGeneration: checkpoint.HighestAuthenticatedGeneration,
		LastSeenStorageCommitID: checkpoint.LastSeenStorageCommitID, Freshness: "protects_future_observations_only",
	}
	if structured {
		return json.NewEncoder(os.Stdout).Encode(status)
	}
	fmt.Printf("Repository ID: %s\nHighest authenticated generation: %d\nLast-seen Storage Ref target commit: %s\nFreshness: protects future observations only; it cannot prove the first observed snapshot was newest.\n",
		status.RepositoryID, status.HighestAuthenticatedGeneration, status.LastSeenStorageCommitID)
	return nil
}

func runDoctor(arguments []string) error {
	structured := false
	positional := make([]string, 0, 1)
	for _, argument := range arguments {
		if argument == "--json" && !structured {
			structured = true
			continue
		}
		if strings.HasPrefix(argument, "-") {
			return fmt.Errorf("usage: git-remote-cloak doctor <repository-url> [--json]")
		}
		positional = append(positional, argument)
	}
	if len(positional) != 1 {
		return fmt.Errorf("usage: git-remote-cloak doctor <repository-url> [--json]")
	}
	recoverySecret, err := acquireSecret("", false)
	if err != nil {
		return err
	}
	repositoryEngine := engine.New()
	if gitDirectory := localStateForRepositoryURL(positional[0]); gitDirectory != "" {
		repositoryEngine = engine.NewWithLocalState(gitDirectory)
	}
	report, diagnosticErr := repositoryEngine.Doctor(positional[0], recoverySecret)
	if structured {
		if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
			return err
		}
	} else {
		for _, check := range report.Checks {
			fmt.Printf("%s: %s", check.Name, check.Status)
			if check.Detail != "" {
				fmt.Printf(" — %s", check.Detail)
			}
			fmt.Println()
		}
		fmt.Printf("Freshness: %s\n", report.Freshness)
	}
	if diagnosticErr != nil {
		return fmt.Errorf("doctor found repository integrity problems")
	}
	return nil
}

func runSetHead(arguments []string) error {
	if len(arguments) != 2 {
		return fmt.Errorf("usage: git-remote-cloak set-head <remote-name> <branch>")
	}
	recoverySecret, err := acquireSecret("", false)
	if err != nil {
		return err
	}
	workspace, err := os.Getwd()
	if err != nil {
		return err
	}
	gitDirectory, err := absoluteGitDirectory()
	if err != nil {
		return err
	}
	if err := engine.NewWithLocalState(gitDirectory).SetHead(workspace, arguments[0], arguments[1], recoverySecret); err != nil {
		return err
	}
	fmt.Printf("Logical HEAD now selects refs/heads/%s.\n", strings.TrimPrefix(arguments[1], "refs/heads/"))
	return nil
}

func runVersion(arguments []string) error {
	capabilities := cloakformat.NewRegistry().Capabilities()
	if len(arguments) == 1 && arguments[0] == "--formats" {
		for _, capability := range capabilities {
			requiredFeatures := "none"
			if len(capability.RequiredFeatures) > 0 {
				requiredFeatures = strings.Join(capability.RequiredFeatures, ",")
			}
			fmt.Printf("v%d.%d read=%s write=%s cryptographic-suite=%s required-features=%s\n",
				capability.Major, capability.Minor, yesNo(capability.Read), yesNo(capability.Write),
				capability.CryptographicSuite, requiredFeatures)
		}
		return nil
	}
	if len(arguments) == 2 && arguments[0] == "--formats" && arguments[1] == "--json" {
		return json.NewEncoder(os.Stdout).Encode(struct {
			Formats []cloakformat.Capability `json:"formats"`
		}{Formats: capabilities})
	}
	return fmt.Errorf("usage: git-remote-cloak version --formats [--json]")
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func runInit(arguments []string) error {
	positional, secretFile, defaultBranch, err := parseArguments(arguments, true)
	if err != nil || len(positional) != 2 {
		return fmt.Errorf("usage: git-remote-cloak init <remote-name> <repository-url> [--secret-file PATH] [--default-branch BRANCH]")
	}
	recoverySecret, err := acquireSecret(secretFile, true)
	if err != nil {
		return err
	}
	workspace, err := os.Getwd()
	if err != nil {
		return err
	}
	gitDirectory, err := absoluteGitDirectory()
	if err != nil {
		return err
	}
	if err := engine.NewWithLocalState(gitDirectory).Initialize(workspace, positional[0], positional[1], defaultBranch, recoverySecret); err != nil {
		return err
	}
	fmt.Printf("Initialized Ciphertext Repository for remote %s.\n", positional[0])
	return nil
}

func runClone(arguments []string) error {
	positional, secretFile, storageCommitID, err := parseCloneArguments(arguments)
	if err != nil || len(positional) < 1 || len(positional) > 2 {
		return fmt.Errorf("usage: git-remote-cloak clone <repository-url> [directory] [--secret-file PATH] [--storage-commit OBJECT-ID]")
	}
	recoverySecret, err := acquireSecret(secretFile, false)
	if err != nil {
		return err
	}
	destination := ""
	if len(positional) == 2 {
		destination = positional[1]
	}
	if storageCommitID != "" && destination == "" {
		return fmt.Errorf("historical recovery with --storage-commit requires a separate destination")
	}
	var recoverErr error
	if storageCommitID == "" {
		recoverErr = engine.New().Recover(positional[0], destination, recoverySecret)
	} else {
		recoverErr = engine.New().RecoverHistorical(positional[0], storageCommitID, destination, recoverySecret)
	}
	if recoverErr != nil {
		return recoverErr
	}
	fmt.Println("Recovered Logical Repository.")
	return nil
}

func parseCloneArguments(arguments []string) (positional []string, secretFile, storageCommitID string, err error) {
	for index := 0; index < len(arguments); index++ {
		switch arguments[index] {
		case "--secret-file":
			index++
			if index >= len(arguments) || secretFile != "" {
				return nil, "", "", usageError()
			}
			secretFile = arguments[index]
		case "--storage-commit":
			index++
			if index >= len(arguments) || storageCommitID != "" {
				return nil, "", "", usageError()
			}
			storageCommitID = arguments[index]
		default:
			if strings.HasPrefix(arguments[index], "-") {
				return nil, "", "", usageError()
			}
			positional = append(positional, arguments[index])
		}
	}
	return positional, secretFile, storageCommitID, nil
}

func parseArguments(arguments []string, allowDefaultBranch bool) (positional []string, secretFile, defaultBranch string, err error) {
	for index := 0; index < len(arguments); index++ {
		switch arguments[index] {
		case "--secret-file":
			index++
			if index >= len(arguments) || secretFile != "" {
				return nil, "", "", usageError()
			}
			secretFile = arguments[index]
		case "--default-branch":
			if !allowDefaultBranch {
				return nil, "", "", usageError()
			}
			index++
			if index >= len(arguments) || defaultBranch != "" {
				return nil, "", "", usageError()
			}
			defaultBranch = arguments[index]
		default:
			if strings.HasPrefix(arguments[index], "-") {
				return nil, "", "", usageError()
			}
			positional = append(positional, arguments[index])
		}
	}
	return positional, secretFile, defaultBranch, nil
}

func acquireSecret(explicitFile string, allowGeneration bool) (domain.RecoverySecret, error) {
	environmentValue, environmentSet := os.LookupEnv("CLOAK_RECOVERY_SECRET")
	environmentFile, environmentFileSet := os.LookupEnv("CLOAK_RECOVERY_SECRET_FILE")
	if !environmentSet && !environmentFileSet && explicitFile == "" && allowGeneration {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return domain.RecoverySecret{}, fmt.Errorf("non-interactive init requires a configured Recovery Secret")
		}
		recoverySecret, mnemonic, err := secret.Generate()
		if err != nil {
			return domain.RecoverySecret{}, err
		}
		fmt.Fprintf(os.Stdout, "Recovery Mnemonic (shown once): %s\nConfirm it is saved by typing SAVED: ", mnemonic)
		confirmation, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			return domain.RecoverySecret{}, fmt.Errorf("read Recovery Mnemonic confirmation: %w", err)
		}
		if strings.TrimSpace(confirmation) != "SAVED" {
			return domain.RecoverySecret{}, fmt.Errorf("Recovery Mnemonic was not confirmed; repository was not initialized")
		}
		return recoverySecret, nil
	}
	return secret.Acquire(secret.Sources{
		EnvironmentValue:   environmentValue,
		EnvironmentSet:     environmentSet,
		EnvironmentFile:    environmentFile,
		EnvironmentFileSet: environmentFileSet,
		ExplicitFile:       explicitFile,
	}, func(message string) { fmt.Fprintln(os.Stderr, message) })
}

func usageError() error {
	return fmt.Errorf("usage: git-remote-cloak <init|clone|rekey|migrate|compact|cache|doctor|set-head|status|version>")
}

func absoluteGitDirectory() (string, error) {
	command := exec.Command("git", "rev-parse", "--absolute-git-dir")
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func localStateForRepositoryURL(repositoryURL string) string {
	gitDirectory, err := absoluteGitDirectory()
	if err != nil {
		return ""
	}
	command := exec.Command("git", "config", "--get-regexp", `^remote\..*\.url$`)
	output, err := command.Output()
	if err != nil {
		return ""
	}
	wanted := "cloak::" + repositoryURL
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		_, configuredURL, found := strings.Cut(line, " ")
		if found && configuredURL == wanted {
			return gitDirectory
		}
	}
	return ""
}
