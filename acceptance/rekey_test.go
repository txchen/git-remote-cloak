package acceptance_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRekeyReplacesRemoteFromExactlySelectedLocalRefs(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	owner := filepath.Join(root, "owner")
	host := filepath.Join(root, "host.git")
	recovered := filepath.Join(root, "recovered")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", owner)
	writeAndCommit(t, owner, "main.md", "# main\n", "main")
	mustGit(t, owner, "branch", "topic")
	mustGit(t, owner, "tag", "v1")
	mustGit(t, owner, "notes", "--ref=review", "add", "-m", "review note")
	mustInit(t, binary, owner, host, testMnemonic)
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	oldRepositoryID := strings.TrimSpace(mustGit(t, owner, "config", "--get", "remote.backup.cloakRepositoryID"))

	command := exec.Command(binary, "rekey", "backup", "refs/notes/review", "--yes")
	command.Dir = owner
	command.Env = append(withoutEnvironment(os.Environ(), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE"), "CLOAK_RECOVERY_SECRET="+otherMnemonic)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("rekey failed: %v\n%s", err, output)
	}
	for _, selected := range []string{"refs/heads/main", "refs/heads/topic", "refs/tags/v1", "refs/notes/review"} {
		if !strings.Contains(string(output), selected) {
			t.Fatalf("rekey plan omitted selected ref %s:\n%s", selected, output)
		}
	}
	if !strings.Contains(string(output), "retention") || !strings.Contains(string(output), "erasure") || !strings.Contains(string(output), "quota") {
		t.Fatalf("rekey output omitted retention warning:\n%s", output)
	}
	newRepositoryID := strings.TrimSpace(mustGit(t, owner, "config", "--get", "remote.backup.cloakRepositoryID"))
	if newRepositoryID == oldRepositoryID {
		t.Fatal("Rekey preserved the old Repository ID")
	}
	if got := storageHistoryLength(t, host); got != 1 {
		t.Fatalf("Rekey Storage History length = %d, want one parentless generation-one root", got)
	}
	if got := checkpointGeneration(t, binary, owner); got != 1 {
		t.Fatalf("Rekey checkpoint generation = %d, want 1", got)
	}
	if got := mustGit(t, owner, "remote", "get-url", "backup"); got != "cloak::"+host+"\n" {
		t.Fatalf("Rekey changed Repository Host URL to %q", got)
	}

	clone := exec.Command(binary, "clone", host, recovered)
	clone.Env = append(withoutEnvironment(os.Environ(), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE"), "CLOAK_RECOVERY_SECRET="+otherMnemonic)
	if cloneOutput, err := clone.CombinedOutput(); err != nil {
		t.Fatalf("new Recovery Secret cannot clone Rekeyed repository: %v\n%s", err, cloneOutput)
	}
	wantRefs := mustGit(t, owner, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads", "refs/tags", "refs/notes/review")
	gotRefs := mustGit(t, recovered, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads", "refs/tags", "refs/notes/review")
	if gotRefs != wantRefs {
		t.Fatalf("Rekey recovered refs differ:\nwant:\n%s\ngot:\n%s", wantRefs, gotRefs)
	}
	oldClone := exec.Command(binary, "clone", host, filepath.Join(root, "old-secret"))
	oldClone.Env = cloakGitEnvironment(binary)
	if oldOutput, err := oldClone.CombinedOutput(); err == nil {
		t.Fatalf("old Recovery Secret cloned Rekeyed identity:\n%s", oldOutput)
	}
}

func TestRekeyCancellationLeavesExistingCiphertextRepositoryAuthoritative(t *testing.T) {
	binary, root, owner, host, oldStorageRef := rekeyFixture(t)
	mustGit(t, owner, "update-ref", "refs/remotes/origin/orphan", "refs/heads/main")
	command := exec.Command(binary, "rekey", "backup")
	command.Dir = owner
	command.Env = withoutEnvironment(os.Environ(), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE")
	command.Stdin = strings.NewReader("cancel\n")
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "cancelled") {
		t.Fatalf("cancelled Rekey result = %v:\n%s", err, output)
	}
	if !strings.Contains(string(output), "refs/heads/main") || !strings.Contains(string(output), "refs/remotes/origin/orphan") {
		t.Fatalf("Rekey cancellation prompt omitted selected refs or remote-tracking warning:\n%s", output)
	}
	assertStorageRef(t, host, oldStorageRef)
	mustCloakGit(t, binary, root, "clone", "cloak::"+host, filepath.Join(root, "old-recovery"))
}

func TestRekeyRejectsIncompleteLocalObjectsBeforePublication(t *testing.T) {
	binary, _, owner, host, oldStorageRef := rekeyFixture(t)
	blob := strings.TrimSpace(mustGit(t, owner, "rev-parse", "HEAD:main.md"))
	if err := os.Remove(filepath.Join(owner, ".git", "objects", blob[:2], blob[2:])); err != nil {
		t.Fatal(err)
	}
	command := newRekeyCommand(binary, owner, "")
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("Rekey with incomplete local objects succeeded:\n%s", output)
	}
	assertStorageRef(t, host, oldStorageRef)
}

func TestRekeyRejectsExplicitLocalOperationalRefs(t *testing.T) {
	binary, _, owner, host, oldStorageRef := rekeyFixture(t)
	for _, name := range []string{"refs/original/tool", "refs/prefetch/origin/main", "refs/bundle/main"} {
		mustGit(t, owner, "update-ref", name, "refs/heads/main")
		command := exec.Command(binary, "rekey", "backup", name, "--yes")
		command.Dir = owner
		command.Env = append(withoutEnvironment(os.Environ(), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE"), "CLOAK_RECOVERY_SECRET="+otherMnemonic)
		if output, err := command.CombinedOutput(); err == nil || !strings.Contains(string(output), "operational ref") {
			t.Fatalf("explicit operational ref %s was not rejected: %v\n%s", name, err, output)
		}
	}
	assertStorageRef(t, host, oldStorageRef)
}

func TestRekeyFailuresBeforeCompareAndSwapLeaveExistingCiphertextRepositoryAuthoritative(t *testing.T) {
	for _, fault := range []string{"candidate-validation", "before-storage-ref", "stale-storage-ref"} {
		t.Run(fault, func(t *testing.T) {
			binary, root, owner, host, oldStorageRef := rekeyFixture(t)
			command := newRekeyCommand(binary, owner, fault)
			if output, err := command.CombinedOutput(); err == nil {
				t.Fatalf("Rekey fault %s succeeded:\n%s", fault, output)
			}
			if fault != "stale-storage-ref" {
				assertStorageRef(t, host, oldStorageRef)
			}
			mustCloakGit(t, binary, root, "clone", "cloak::"+host, filepath.Join(root, "old-recovery"))
			newClone := exec.Command(binary, "clone", host, filepath.Join(root, "new-recovery"))
			newClone.Env = append(withoutEnvironment(os.Environ(), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE"), "CLOAK_RECOVERY_SECRET="+otherMnemonic)
			if output, err := newClone.CombinedOutput(); err == nil {
				t.Fatalf("failed Rekey fault %s published the new identity:\n%s", fault, output)
			}
		})
	}
}

func TestRekeyRecognizesSuccessfulPublicationAfterLostResponse(t *testing.T) {
	binary, root, owner, host, _ := rekeyFixture(t)
	command := newRekeyCommand(binary, owner, "after-storage-ref")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Rekey did not recognize the confirmed publication after a lost response: %v\n%s", err, output)
	}
	if got := checkpointGeneration(t, binary, owner); got != 1 {
		t.Fatalf("Rekey checkpoint generation after lost response = %d, want 1", got)
	}
	newClone := exec.Command(binary, "clone", host, filepath.Join(root, "new-recovery"))
	newClone.Env = append(withoutEnvironment(os.Environ(), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE"), "CLOAK_RECOVERY_SECRET="+otherMnemonic)
	if output, err := newClone.CombinedOutput(); err != nil {
		t.Fatalf("confirmed Rekey publication after lost response is not recoverable: %v\n%s", err, output)
	}
}

func rekeyFixture(t *testing.T) (binary, root, owner, host, storageRef string) {
	t.Helper()
	binary = buildBinary(t)
	root = t.TempDir()
	owner = filepath.Join(root, "owner")
	host = filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", owner)
	writeAndCommit(t, owner, "main.md", "# complete local repository\n", "main")
	mustInit(t, binary, owner, host, testMnemonic)
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	storageRef = strings.TrimSpace(mustGit(t, host, "rev-parse", "refs/heads/cloak-storage"))
	return binary, root, owner, host, storageRef
}

func newRekeyCommand(binary, owner, fault string) *exec.Cmd {
	command := exec.Command(binary, "rekey", "backup", "--yes")
	command.Dir = owner
	command.Env = append(withoutEnvironment(os.Environ(), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE"), "CLOAK_RECOVERY_SECRET="+otherMnemonic)
	if fault != "" {
		command.Env = append(command.Env, "CLOAK_TEST_FAULT="+fault)
	}
	return command
}

func assertStorageRef(t *testing.T, host, want string) {
	t.Helper()
	if got := strings.TrimSpace(mustGit(t, host, "rev-parse", "refs/heads/cloak-storage")); got != want {
		t.Fatalf("Storage Ref changed from %s to %s", want, got)
	}
}
