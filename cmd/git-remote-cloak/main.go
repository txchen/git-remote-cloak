package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/txchen/git-remote-cloak/internal/domain"
	"github.com/txchen/git-remote-cloak/internal/engine"
	cloakformat "github.com/txchen/git-remote-cloak/internal/format"
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
	case "set-head":
		return runSetHead(arguments[1:])
	default:
		if len(arguments) == 2 {
			recoverySecret, err := acquireSecret("", false)
			if err != nil {
				return err
			}
			return remotehelper.Run(arguments[1], recoverySecret, os.Stdin, os.Stdout)
		}
		return usageError()
	}
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
	if err := engine.New().SetHead(workspace, arguments[0], arguments[1], recoverySecret); err != nil {
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
	if err := engine.New().Initialize(workspace, positional[0], positional[1], defaultBranch, recoverySecret); err != nil {
		return err
	}
	fmt.Printf("Initialized Ciphertext Repository for remote %s.\n", positional[0])
	return nil
}

func runClone(arguments []string) error {
	positional, secretFile, _, err := parseArguments(arguments, false)
	if err != nil || len(positional) < 1 || len(positional) > 2 {
		return fmt.Errorf("usage: git-remote-cloak clone <repository-url> [directory] [--secret-file PATH]")
	}
	recoverySecret, err := acquireSecret(secretFile, false)
	if err != nil {
		return err
	}
	destination := ""
	if len(positional) == 2 {
		destination = positional[1]
	}
	if err := engine.New().Recover(positional[0], destination, recoverySecret); err != nil {
		return err
	}
	fmt.Println("Recovered Logical Repository.")
	return nil
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
	return fmt.Errorf("usage: git-remote-cloak <init|clone|set-head|version>")
}
