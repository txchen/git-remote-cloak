package acceptance_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestEmptySHA256LogicalAndCiphertextRepositoriesRoundTrip(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	host := filepath.Join(root, "host.git")
	humanRecovered := filepath.Join(root, "human-recovered")
	helperRecovered := filepath.Join(root, "helper-recovered")
	mustGit(t, root, "init", "--bare", "--object-format=sha256", host)
	mustGit(t, root, "init", "--object-format=sha256", "-b", "main", workspace)
	mustInit(t, binary, workspace, host, testMnemonic)

	clone := exec.Command(binary, "clone", host, humanRecovered)
	clone.Env = append(withoutEnvironment(os.Environ(), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE"), "CLOAK_RECOVERY_SECRET="+testMnemonic)
	if output, err := clone.CombinedOutput(); err != nil {
		t.Fatalf("human SHA-256 clone failed: %v\n%s", err, output)
	}
	if got := mustGit(t, humanRecovered, "rev-parse", "--show-object-format"); got != "sha256\n" {
		t.Fatalf("human clone object format = %q, want sha256", got)
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
}
