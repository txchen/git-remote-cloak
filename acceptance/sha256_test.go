package acceptance_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSHA256ProtectedBranchRoundTrips(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	host := filepath.Join(root, "host.git")
	humanRecovered := filepath.Join(root, "human-recovered")
	helperRecovered := filepath.Join(root, "helper-recovered")
	mustGit(t, root, "init", "--bare", "--object-format=sha256", host)
	mustGit(t, root, "init", "--object-format=sha256", "-b", "main", workspace)
	if err := os.WriteFile(filepath.Join(workspace, "sha256.txt"), []byte("sha256 protected object\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, workspace, "add", ".")
	mustGit(t, workspace, "commit", "-m", "sha256 protected commit")
	wantCommit := mustGit(t, workspace, "rev-parse", "HEAD")
	mustInit(t, binary, workspace, host, testMnemonic)
	push := exec.Command("git", "push", "backup", "main")
	push.Dir = workspace
	push.Env = cloakGitEnvironment(binary)
	if output, err := push.CombinedOutput(); err != nil {
		t.Fatalf("SHA-256 push failed: %v\n%s", err, output)
	}

	clone := exec.Command(binary, "clone", host, humanRecovered)
	clone.Env = append(withoutEnvironment(os.Environ(), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE"), "CLOAK_RECOVERY_SECRET="+testMnemonic)
	if output, err := clone.CombinedOutput(); err != nil {
		t.Fatalf("human SHA-256 clone failed: %v\n%s", err, output)
	}
	if got := mustGit(t, humanRecovered, "rev-parse", "--show-object-format"); got != "sha256\n" {
		t.Fatalf("human clone object format = %q, want sha256", got)
	}
	if got := mustGit(t, humanRecovered, "rev-parse", "HEAD"); got != wantCommit {
		t.Fatalf("human clone commit = %q, want %q", got, wantCommit)
	}
	if got, err := os.ReadFile(filepath.Join(humanRecovered, "sha256.txt")); err != nil || string(got) != "sha256 protected object\n" {
		t.Fatalf("human clone worktree was not recovered: contents=%q err=%v", got, err)
	}

	gitClone := exec.Command("git", "clone", "cloak::"+host, helperRecovered)
	gitClone.Dir = root
	gitClone.Env = append(withoutEnvironment(os.Environ(), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE"),
		"CLOAK_RECOVERY_SECRET="+testMnemonic,
		"PATH="+filepath.Dir(binary)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	if output, err := gitClone.CombinedOutput(); err != nil {
		t.Fatalf("remote-helper SHA-256 clone failed: %v\n%s", err, output)
	}
	if got := mustGit(t, helperRecovered, "rev-parse", "--show-object-format"); got != "sha256\n" {
		t.Fatalf("remote-helper clone object format = %q, want sha256", got)
	}
	if got := mustGit(t, helperRecovered, "rev-parse", "HEAD"); got != wantCommit {
		t.Fatalf("remote-helper clone commit = %q, want %q", got, wantCommit)
	}
}
