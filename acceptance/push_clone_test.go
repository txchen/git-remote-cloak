package acceptance_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestOwnerPushesAndClonesOneProtectedBranch(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	host := filepath.Join(root, "host.git")
	recovered := filepath.Join(root, "recovered")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", workspace)

	text := []byte("protected contents\n")
	binaryContents := append([]byte{0, 1, 2, 0xff, 0xfe}, bytes.Repeat([]byte{0x89, 0x50, 0x4e, 0x47}, 1024)...)
	if err := os.WriteFile(filepath.Join(workspace, "private-notes.md"), text, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "compressed.bin"), binaryContents, 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, workspace, "add", ".")
	mustGit(t, workspace, "commit", "-m", "private first commit")
	wantCommit := mustGit(t, workspace, "rev-parse", "HEAD")
	wantObjects := mustGit(t, workspace, "rev-list", "--objects", "HEAD")
	mustInit(t, binary, workspace, host, testMnemonic)

	push := exec.Command("git", "push", "backup", "main")
	push.Dir = workspace
	push.Env = cloakGitEnvironment(binary)
	if output, err := push.CombinedOutput(); err != nil {
		t.Fatalf("ordinary Git push failed: %v\n%s", err, output)
	}
	mustInit(t, binary, workspace, host, testMnemonic)

	clone := exec.Command("git", "clone", "cloak::"+host, recovered)
	clone.Dir = root
	clone.Env = cloakGitEnvironment(binary)
	if output, err := clone.CombinedOutput(); err != nil {
		t.Fatalf("ordinary Git clone failed: %v\n%s", err, output)
	}
	if got := mustGit(t, recovered, "rev-parse", "HEAD"); got != wantCommit {
		t.Fatalf("recovered commit = %q, want %q", got, wantCommit)
	}
	if got := mustGit(t, recovered, "rev-list", "--objects", "HEAD"); got != wantObjects {
		t.Fatalf("recovered reachable objects:\n%s\nwant:\n%s", got, wantObjects)
	}
	if got, err := os.ReadFile(filepath.Join(recovered, "compressed.bin")); err != nil || !bytes.Equal(got, binaryContents) {
		t.Fatalf("recovered binary differs: err=%v", err)
	}
	if output, err := exec.Command("git", "-C", recovered, "fsck", "--full").CombinedOutput(); err != nil {
		t.Fatalf("recovered repository fails fsck: %v\n%s", err, output)
	}
}

func TestFailedStorageRefPublicationLeavesPreviousSnapshotAuthoritative(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	host := filepath.Join(root, "host.git")
	recovered := filepath.Join(root, "recovered")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", workspace)
	if err := os.WriteFile(filepath.Join(workspace, "protected.txt"), []byte("protected\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, workspace, "add", ".")
	mustGit(t, workspace, "commit", "-m", "protected commit")
	mustInit(t, binary, workspace, host, testMnemonic)
	before := mustGit(t, host, "rev-parse", "refs/heads/cloak-storage")

	lock := filepath.Join(host, "refs", "heads", "cloak-storage.lock")
	if err := os.WriteFile(lock, []byte("publication blocked by test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	push := exec.Command("git", "push", "backup", "main")
	push.Dir = workspace
	push.Env = cloakGitEnvironment(binary)
	if output, err := push.CombinedOutput(); err == nil {
		t.Fatalf("push succeeded despite blocked Storage Ref publication:\n%s", output)
	}
	if got := mustGit(t, host, "rev-parse", "refs/heads/cloak-storage"); got != before {
		t.Fatalf("failed publication changed authoritative snapshot from %q to %q", before, got)
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}

	clone := exec.Command(binary, "clone", host, recovered)
	clone.Env = cloakGitEnvironment(binary)
	if output, err := clone.CombinedOutput(); err != nil {
		t.Fatalf("previous empty snapshot no longer recovers: %v\n%s", err, output)
	}
	if got := mustGit(t, recovered, "for-each-ref", "--format=%(refname)"); got != "" {
		t.Fatalf("failed push became authoritative: refs = %q", got)
	}
}

func cloakGitEnvironment(binary string) []string {
	return append(withoutEnvironment(os.Environ(), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE"),
		"CLOAK_RECOVERY_SECRET="+testMnemonic,
		"PATH="+filepath.Dir(binary)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GIT_CONFIG_NOSYSTEM=1",
	)
}
