package acceptance_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const otherMnemonic = "cloak-v1:zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo vote"

func TestInitIsIdempotentForSameCloakIdentity(t *testing.T) {
	binary, root, workspace, host := emptyInitFixture(t)
	mustInit(t, binary, workspace, host, testMnemonic)
	storageCommit := mustGit(t, host, "rev-parse", "refs/heads/cloak-storage")

	mustInit(t, binary, workspace, host, testMnemonic)
	if got := mustGit(t, host, "rev-parse", "refs/heads/cloak-storage"); got != storageCommit {
		t.Fatalf("idempotent init changed Storage Ref from %q to %q", storageCommit, got)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal(err)
	}

	unassociatedWorkspace := filepath.Join(root, "unassociated-workspace")
	mustGit(t, root, "init", "-b", "main", unassociatedWorkspace)
	output := rejectedInit(t, binary, unassociatedWorkspace, host, testMnemonic)
	if !strings.Contains(output, "matching configured remote") {
		t.Fatalf("unexpected unassociated repository rejection: %s", output)
	}
	if got := mustGit(t, host, "rev-parse", "refs/heads/cloak-storage"); got != storageCommit {
		t.Fatalf("unassociated init changed Storage Ref")
	}
}

func TestInitRejectsForeignRefsAndAnotherCloakIdentityWithoutMutation(t *testing.T) {
	t.Run("foreign refs", func(t *testing.T) {
		binary, _, workspace, host := emptyInitFixture(t)
		mustInit(t, binary, workspace, host, testMnemonic)
		storageCommit := strings.TrimSpace(mustGit(t, host, "rev-parse", "refs/heads/cloak-storage"))
		mustGit(t, host, "update-ref", "refs/heads/foreign", storageCommit)
		before := mustGit(t, host, "show-ref")

		output := rejectedInit(t, binary, workspace, host, testMnemonic)
		if !strings.Contains(output, "foreign refs") {
			t.Fatalf("unexpected rejection: %s", output)
		}
		if got := mustGit(t, host, "show-ref"); got != before {
			t.Fatalf("rejected init changed refs:\nbefore %s\nafter %s", before, got)
		}
	})

	t.Run("another Cloak identity", func(t *testing.T) {
		binary, root, workspace, host := emptyInitFixture(t)
		mustInit(t, binary, workspace, host, testMnemonic)
		storageCommit := mustGit(t, host, "rev-parse", "refs/heads/cloak-storage")
		otherWorkspace := filepath.Join(root, "other-workspace")
		mustGit(t, root, "init", "-b", "main", otherWorkspace)
		mustGit(t, otherWorkspace, "remote", "add", "backup", "cloak::"+host)
		repositoryID := strings.TrimSpace(mustGit(t, workspace, "config", "--get", "remote.backup.cloakRepositoryID"))
		mustGit(t, otherWorkspace, "config", "remote.backup.cloakRepositoryID", repositoryID)

		output := rejectedInit(t, binary, otherWorkspace, host, otherMnemonic)
		if !strings.Contains(output, "another Cloak identity") {
			t.Fatalf("unexpected rejection: %s", output)
		}
		if got := mustGit(t, host, "rev-parse", "refs/heads/cloak-storage"); got != storageCommit {
			t.Fatalf("rejected identity changed Storage Ref")
		}
	})
}

func TestInitRejectsMismatchedRemoteAndDetachedHEADBeforePublication(t *testing.T) {
	t.Run("mismatched remote", func(t *testing.T) {
		binary, root, workspace, host := emptyInitFixture(t)
		otherHost := filepath.Join(root, "other.git")
		mustGit(t, root, "init", "--bare", otherHost)
		mustGit(t, workspace, "remote", "add", "backup", "cloak::"+otherHost)

		output := rejectedInit(t, binary, workspace, host, testMnemonic)
		if !strings.Contains(output, "remote configuration does not match") {
			t.Fatalf("unexpected rejection: %s", output)
		}
		if got := mustGit(t, host, "for-each-ref", "--format=%(refname)"); got != "" {
			t.Fatalf("mismatched configuration mutated Repository Host: %q", got)
		}
	})

	t.Run("mismatched public Repository ID", func(t *testing.T) {
		binary, _, workspace, host := emptyInitFixture(t)
		mustInit(t, binary, workspace, host, testMnemonic)
		storageCommit := mustGit(t, host, "rev-parse", "refs/heads/cloak-storage")
		mustGit(t, workspace, "config", "remote.backup.cloakRepositoryID", strings.Repeat("0", 32))

		output := rejectedInit(t, binary, workspace, host, testMnemonic)
		if !strings.Contains(output, "recorded Repository ID does not match") {
			t.Fatalf("unexpected rejection: %s", output)
		}
		if got := mustGit(t, host, "rev-parse", "refs/heads/cloak-storage"); got != storageCommit {
			t.Fatalf("Repository ID mismatch changed Storage Ref")
		}
	})

	t.Run("detached HEAD", func(t *testing.T) {
		binary, _, workspace, host := emptyInitFixture(t)
		mustGit(t, workspace, "commit", "--allow-empty", "-m", "local only")
		mustGit(t, workspace, "checkout", "--detach")

		output := rejectedInit(t, binary, workspace, host, testMnemonic)
		if !strings.Contains(output, "detached HEAD requires --default-branch") {
			t.Fatalf("unexpected rejection: %s", output)
		}
		if got := mustGit(t, host, "for-each-ref", "--format=%(refname)"); got != "" {
			t.Fatalf("detached rejection mutated Repository Host: %q", got)
		}

		invalid := exec.Command(binary, "init", "backup", host, "--default-branch", "bad name")
		invalid.Dir = workspace
		invalid.Env = append(withoutEnvironment(os.Environ(), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE"), "CLOAK_RECOVERY_SECRET="+testMnemonic)
		if output, err := invalid.CombinedOutput(); err == nil {
			t.Fatalf("invalid default branch unexpectedly published:\n%s", output)
		}
		if got := mustGit(t, host, "for-each-ref", "--format=%(refname)"); got != "" {
			t.Fatalf("invalid default branch mutated Repository Host: %q", got)
		}

		command := exec.Command(binary, "init", "backup", host, "--default-branch", "main")
		command.Dir = workspace
		command.Env = append(withoutEnvironment(os.Environ(), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE"), "CLOAK_RECOVERY_SECRET="+testMnemonic)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("init with explicit default branch failed: %v\n%s", err, output)
		}
	})
}

func emptyInitFixture(t *testing.T) (binary, root, workspace, host string) {
	t.Helper()
	binary = buildBinary(t)
	root = t.TempDir()
	workspace = filepath.Join(root, "workspace")
	host = filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", workspace)
	return binary, root, workspace, host
}

func mustInit(t *testing.T, binary, workspace, host, mnemonic string) {
	t.Helper()
	command := exec.Command(binary, "init", "backup", host)
	command.Dir = workspace
	command.Env = append(withoutEnvironment(os.Environ(), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE"), "CLOAK_RECOVERY_SECRET="+mnemonic)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("init failed: %v\n%s", err, output)
	}
	// Most acceptance fixtures exercise non-maintenance behavior and keep
	// Storage History linear. Compaction tests explicitly enable the default.
	mustGit(t, workspace, "config", "remote.backup.cloakAutoCompact", "false")
}

func rejectedInit(t *testing.T, binary, workspace, host, mnemonic string) string {
	t.Helper()
	command := exec.Command(binary, "init", "backup", host)
	command.Dir = workspace
	command.Env = append(withoutEnvironment(os.Environ(), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE"), "CLOAK_RECOVERY_SECRET="+mnemonic)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("init unexpectedly succeeded:\n%s", output)
	}
	return string(output)
}
