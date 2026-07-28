package acceptance_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type checkpointStatus struct {
	RepositoryID                   string `json:"repository_id"`
	HighestAuthenticatedGeneration uint64 `json:"highest_authenticated_generation"`
	LastSeenStorageCommitID        string `json:"last_seen_storage_commit_id"`
	Freshness                      string `json:"freshness"`
}

type doctorReport struct {
	OK            bool   `json:"ok"`
	Generation    uint64 `json:"generation"`
	ProviderError string `json:"provider_error"`
	Checks        []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"checks"`
}

func TestDoctorRetainsSafeProviderErrors(t *testing.T) {
	binary := buildBinary(t)
	missingHost := filepath.Join(t.TempDir(), "missing-host.git")
	doctor := exec.Command(binary, "doctor", missingHost, "--json")
	doctor.Env = cloakGitEnvironment(binary)
	var stdout bytes.Buffer
	doctor.Stdout = &stdout
	if err := doctor.Run(); err == nil {
		t.Fatalf("doctor accepted missing Repository Host: %s", stdout.String())
	}
	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("provider failure is not structured JSON: %v\n%s", err, stdout.String())
	}
	if report.ProviderError == "" || strings.Contains(report.ProviderError, testMnemonic) {
		t.Fatalf("provider error was discarded or leaked a Secret: %+v", report)
	}
}

func TestCompatibleConcurrentWritersRebuildAndPublishBothLogicalRefs(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	owner := filepath.Join(root, "owner")
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	recovered := filepath.Join(root, "recovered")
	host := filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", owner)
	writeAndCommit(t, owner, "base.txt", "base\n", "base")
	mustInit(t, binary, owner, host, testMnemonic)
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	mustCloakGit(t, binary, root, "clone", "cloak::"+host, first)
	mustCloakGit(t, binary, root, "clone", "cloak::"+host, second)

	mustGit(t, first, "switch", "-c", "first")
	mustGit(t, second, "switch", "-c", "second")
	large := bytes.Repeat([]byte("concurrent ciphertext payload\n"), 150_000)
	if err := os.WriteFile(filepath.Join(first, "first.bin"), large, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "second.bin"), large, 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, first, "add", "first.bin")
	mustGit(t, first, "commit", "-m", "first concurrent update")
	mustGit(t, second, "add", "second.bin")
	mustGit(t, second, "commit", "-m", "second concurrent update")

	commands := []*exec.Cmd{
		exec.Command("git", "push", "origin", "first"),
		exec.Command("git", "push", "origin", "second"),
	}
	commands[0].Dir, commands[1].Dir = first, second
	barrier := t.TempDir()
	commands[0].Env = append(cloakGitEnvironment(binary), "CLOAK_TEST_STORAGE_REF_BARRIER="+barrier, "CLOAK_TEST_STORAGE_REF_PARTICIPANT=first")
	commands[1].Env = append(cloakGitEnvironment(binary), "CLOAK_TEST_STORAGE_REF_BARRIER="+barrier, "CLOAK_TEST_STORAGE_REF_PARTICIPANT=second")
	outputs := make([]bytes.Buffer, len(commands))
	for index, command := range commands {
		command.Stdout = &outputs[index]
		command.Stderr = &outputs[index]
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
	}
	waitForStorageRefBarrier(t, barrier, "first", "second")
	if err := os.WriteFile(filepath.Join(barrier, "release"), []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for index, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatalf("concurrent push %d failed: %v\n%s", index+1, err, outputs[index].String())
		}
	}

	mustCloakGit(t, binary, root, "clone", "cloak::"+host, recovered)
	if got, want := mustGit(t, recovered, "rev-parse", "origin/first"), mustGit(t, first, "rev-parse", "first"); got != want {
		t.Fatalf("recovered first ref = %q, want %q", got, want)
	}
	if got, want := mustGit(t, recovered, "rev-parse", "origin/second"), mustGit(t, second, "rev-parse", "second"); got != want {
		t.Fatalf("recovered second ref = %q, want %q", got, want)
	}
}

func waitForStorageRefBarrier(t *testing.T, directory string, participants ...string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		allReady := true
		for _, participant := range participants {
			if _, err := os.Stat(filepath.Join(directory, participant+".ready")); err != nil {
				allReady = false
				break
			}
		}
		if allReady {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("writers did not reach the deterministic Storage Ref barrier: %v", participants)
}

func TestLocalOperationLockRejectsAConflictingPublication(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	owner := filepath.Join(root, "owner")
	host := filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", owner)
	writeAndCommit(t, owner, "base.txt", "base\n", "base")
	mustInit(t, binary, owner, host, testMnemonic)
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	large := bytes.Repeat([]byte("same logical repository publication\n"), 150_000)
	if err := os.WriteFile(filepath.Join(owner, "large.bin"), large, 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, owner, "add", "large.bin")
	mustGit(t, owner, "commit", "-m", "one local operation")

	commands := []*exec.Cmd{
		exec.Command("git", "push", "backup", "main"),
		exec.Command("git", "push", "backup", "main"),
	}
	outputs := make([]bytes.Buffer, 2)
	for index, command := range commands {
		command.Dir = owner
		command.Env = cloakGitEnvironment(binary)
		command.Stdout, command.Stderr = &outputs[index], &outputs[index]
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
	}
	successes := 0
	lockFailures := 0
	for index, command := range commands {
		if err := command.Wait(); err == nil {
			successes++
		} else if bytes.Contains(outputs[index].Bytes(), []byte("another Cloak publication or maintenance operation")) {
			lockFailures++
		} else {
			t.Fatalf("conflicting push failed unexpectedly: %v\n%s", err, outputs[index].String())
		}
	}
	if successes != 1 || lockFailures != 1 {
		t.Fatalf("conflicting local pushes: successes=%d lock_failures=%d outputs=%q / %q", successes, lockFailures, outputs[0].String(), outputs[1].String())
	}
}

func TestTrustedCheckpointRejectsKnownRollbackAndStatusExportsPublicState(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	owner := filepath.Join(root, "owner")
	authorized := filepath.Join(root, "authorized")
	fresh := filepath.Join(root, "fresh")
	host := filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", owner)
	writeAndCommit(t, owner, "first.txt", "first protected value\n", "first protected commit")
	mustInit(t, binary, owner, host, testMnemonic)
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	olderStorageCommitID := mustGit(t, host, "rev-parse", "refs/heads/cloak-storage")
	mustCloakGit(t, binary, root, "clone", "cloak::"+host, authorized)

	writeAndCommit(t, owner, "second.txt", "second protected value\n", "second protected commit")
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	newerStorageCommitID := mustGit(t, host, "rev-parse", "refs/heads/cloak-storage")
	mustCloakGit(t, binary, authorized, "fetch", "origin")

	status := exec.Command(binary, "status", "--json")
	status.Dir = authorized
	status.Env = withoutEnvironment(os.Environ(), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE")
	output, err := status.Output()
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	var decoded checkpointStatus
	if err := json.Unmarshal(output, &decoded); err != nil {
		t.Fatalf("status is not structured JSON: %v\n%s", err, output)
	}
	if decoded.RepositoryID == "" || decoded.HighestAuthenticatedGeneration != 3 || decoded.LastSeenStorageCommitID != string(bytes.TrimSpace([]byte(newerStorageCommitID))) {
		t.Fatalf("status checkpoint = %+v", decoded)
	}
	if decoded.Freshness != "protects_future_observations_only" {
		t.Fatalf("status freshness = %q", decoded.Freshness)
	}
	statePath := filepath.Join(authorized, ".git", "cloak", "state")
	assertFilesExclude(t, filepath.Dir(statePath), testMnemonic, "first protected value", "second protected value", "refs/heads/main")

	mustGit(t, host, "update-ref", "refs/heads/cloak-storage", string(bytes.TrimSpace([]byte(olderStorageCommitID))))
	fetch := exec.Command("git", "fetch", "origin")
	fetch.Dir = authorized
	fetch.Env = cloakGitEnvironment(binary)
	if rollbackOutput, err := fetch.CombinedOutput(); err == nil || !bytes.Contains(bytes.ToLower(rollbackOutput), []byte("rollback")) {
		t.Fatalf("known rollback was not rejected clearly: err=%v\n%s", err, rollbackOutput)
	}
	doctor := exec.Command(binary, "doctor", host, "--json")
	doctor.Dir = authorized
	doctor.Env = cloakGitEnvironment(binary)
	var doctorOutput bytes.Buffer
	doctor.Stdout = &doctorOutput
	if err := doctor.Run(); err == nil {
		t.Fatalf("doctor accepted a snapshot contradicted by its trusted checkpoint: %s", doctorOutput.String())
	}
	var rollbackReport doctorReport
	if err := json.Unmarshal(doctorOutput.Bytes(), &rollbackReport); err != nil {
		t.Fatalf("rollback doctor output is not structured JSON: %v\n%s", err, doctorOutput.String())
	}
	rollbackDiagnosed := false
	for _, check := range rollbackReport.Checks {
		if check.Name == "rollback_checkpoint" && check.Status == "error" {
			rollbackDiagnosed = true
		}
	}
	if !rollbackDiagnosed {
		t.Fatalf("doctor did not diagnose trusted rollback: %+v", rollbackReport.Checks)
	}

	// A new Authorized Host has no earlier trusted observation and therefore
	// can authenticate this older snapshot but cannot prove it is newest.
	mustCloakGit(t, binary, root, "clone", "cloak::"+host, fresh)
	freshStatus := exec.Command(binary, "status", "--json")
	freshStatus.Dir = fresh
	freshStatus.Env = withoutEnvironment(os.Environ(), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE")
	freshOutput, err := freshStatus.Output()
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(freshOutput, &decoded); err != nil || decoded.Freshness != "protects_future_observations_only" {
		t.Fatalf("fresh status does not state its freshness limit: err=%v status=%+v", err, decoded)
	}
}

func TestHistoricalRecoveryIsExplicitSeparateAndDoesNotMutateStorageRef(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	owner := filepath.Join(root, "owner")
	historical := filepath.Join(root, "historical")
	host := filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", owner)
	writeAndCommit(t, owner, "first.txt", "first generation\n", "first generation")
	firstLogicalCommit := mustGit(t, owner, "rev-parse", "HEAD")
	mustInit(t, binary, owner, host, testMnemonic)
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	olderStorageCommitID := bytes.TrimSpace([]byte(mustGit(t, host, "rev-parse", "refs/heads/cloak-storage")))

	writeAndCommit(t, owner, "second.txt", "second generation\n", "second generation")
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	currentStorageCommitID := mustGit(t, host, "rev-parse", "refs/heads/cloak-storage")

	recover := exec.Command(binary, "clone", host, historical, "--storage-commit", string(olderStorageCommitID))
	recover.Dir = root
	recover.Env = cloakGitEnvironment(binary)
	if output, err := recover.CombinedOutput(); err != nil {
		t.Fatalf("explicit historical recovery failed: %v\n%s", err, output)
	}
	if got := mustGit(t, historical, "rev-parse", "HEAD"); got != firstLogicalCommit {
		t.Fatalf("historical recovery HEAD = %q, want %q", got, firstLogicalCommit)
	}
	if got := mustGit(t, host, "rev-parse", "refs/heads/cloak-storage"); got != currentStorageCommitID {
		t.Fatalf("historical recovery mutated Storage Ref from %q to %q", currentStorageCommitID, got)
	}
}

func TestDoctorIsReadOnlyStructuredCompleteAndRedacted(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	owner := filepath.Join(root, "owner")
	host := filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", owner)
	protectedValues := []string{"private-doctor.txt", "doctor protected contents", "doctor protected commit"}
	writeAndCommit(t, owner, protectedValues[0], protectedValues[1]+"\n", protectedValues[2])
	mustInit(t, binary, owner, host, testMnemonic)
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	before := mustGit(t, host, "rev-parse", "refs/heads/cloak-storage")

	doctor := exec.Command(binary, "doctor", host, "--json")
	doctor.Dir = owner
	doctor.Env = cloakGitEnvironment(binary)
	output, err := doctor.Output()
	if err != nil {
		t.Fatalf("doctor failed: %v", err)
	}
	var report doctorReport
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("doctor output is not JSON: %v\n%s", err, output)
	}
	if !report.OK || report.Generation != 2 {
		t.Fatalf("doctor report = %+v", report)
	}
	wantChecks := map[string]bool{
		"bootstrap_header_authentication":       false,
		"encrypted_manifest_authentication":     false,
		"ciphertext_availability_and_integrity": false,
		"pack_index_consistency":                false,
		"lfs_backed_content":                    false,
		"logical_ref_targets":                   false,
		"recovered_git_object_graph":            false,
	}
	for _, check := range report.Checks {
		if _, wanted := wantChecks[check.Name]; wanted && check.Status == "ok" {
			wantChecks[check.Name] = true
		}
	}
	for name, passed := range wantChecks {
		if !passed {
			t.Fatalf("doctor did not pass %s: %+v", name, report.Checks)
		}
	}
	for _, protected := range append(protectedValues, testMnemonic, "CLOAK_RECOVERY_SECRET") {
		if bytes.Contains(output, []byte(protected)) {
			t.Fatalf("doctor output exposed protected value %q: %s", protected, output)
		}
	}
	if got := mustGit(t, host, "rev-parse", "refs/heads/cloak-storage"); got != before {
		t.Fatalf("doctor mutated Storage Ref from %q to %q", before, got)
	}
}

func TestDoctorReportsMissingCiphertextWithoutProtectedPlaintext(t *testing.T) {
	binary := buildBinary(t)
	_, host, _, encoded, transport := preparedProtectedSnapshot(t, binary, false, false)
	for locator := range encoded.CiphertextObjects {
		if locator != encoded.ManifestLocator {
			delete(encoded.CiphertextObjects, locator)
			break
		}
	}
	if _, err := transport.PublishSnapshot(encodedStorageParent(t, transport), encoded.Bootstrap, encoded.CiphertextObjects); err != nil {
		t.Fatal(err)
	}
	before := mustGit(t, host, "rev-parse", "refs/heads/cloak-storage")
	doctor := exec.Command(binary, "doctor", host, "--json")
	doctor.Env = cloakGitEnvironment(binary)
	var stdout, stderr bytes.Buffer
	doctor.Stdout, doctor.Stderr = &stdout, &stderr
	if err := doctor.Run(); err == nil {
		t.Fatalf("doctor accepted missing ciphertext: %s", stdout.String())
	}
	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("failed doctor result is not structured JSON: %v\n%s\n%s", err, stdout.String(), stderr.String())
	}
	if report.OK {
		t.Fatalf("failed doctor report says OK: %+v", report)
	}
	foundFailure := false
	for _, check := range report.Checks {
		if (check.Name == "ciphertext_availability_and_integrity" || check.Name == "pack_index_consistency") && check.Status == "error" {
			foundFailure = true
		}
	}
	if !foundFailure {
		t.Fatalf("doctor did not diagnose missing ciphertext: %+v", report.Checks)
	}
	for _, protected := range []string{testMnemonic, "protected.txt", "protected commit", "protected\n"} {
		if strings.Contains(stdout.String()+stderr.String(), protected) {
			t.Fatalf("failed doctor exposed protected value %q", protected)
		}
	}
	if got := mustGit(t, host, "rev-parse", "refs/heads/cloak-storage"); got != before {
		t.Fatalf("failed doctor mutated Storage Ref from %q to %q", before, got)
	}
}
