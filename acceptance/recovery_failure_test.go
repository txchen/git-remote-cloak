package acceptance_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	cloakformat "github.com/txchen/git-remote-cloak/internal/format"
	"github.com/txchen/git-remote-cloak/internal/gitdb"
	"github.com/txchen/git-remote-cloak/internal/secret"
	"github.com/txchen/git-remote-cloak/internal/storage"
)

func TestCorruptRecoveryInputsLeaveNoUsablePlaintextCheckout(t *testing.T) {
	binary := buildBinary(t)
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *cloakformat.EncodedSnapshot)
	}{
		{
			name: "missing encrypted object",
			mutate: func(t *testing.T, encoded *cloakformat.EncodedSnapshot) {
				for locator := range encoded.Objects {
					if locator != encoded.ManifestLocator {
						delete(encoded.Objects, locator)
						return
					}
				}
				t.Fatal("fixture has no payload object to remove")
			},
		},
		{
			name: "chunk substitution",
			mutate: func(t *testing.T, encoded *cloakformat.EncodedSnapshot) {
				locators := make([]string, 0, 2)
				for locator := range encoded.Objects {
					if locator != encoded.ManifestLocator {
						locators = append(locators, locator)
					}
				}
				if len(locators) < 2 {
					t.Fatal("fixture does not contain an index and chunk")
				}
				encoded.Objects[locators[0]] = encoded.Objects[locators[1]]
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, host, workspace, encoded, transport := preparedProtectedSnapshot(t, binary, false, false)
			test.mutate(t, &encoded)
			if _, err := transport.PublishSnapshot(encodedStorageParent(t, transport), encoded.Bootstrap, encoded.Objects); err != nil {
				t.Fatalf("publish corrupt fixture: %v", err)
			}
			assertCloneFailsWithoutDestination(t, binary, root, host, filepath.Join(root, "recovered"))
			_ = workspace
		})
	}

	for _, test := range []struct {
		name           string
		malformedIndex bool
		corruptPack    bool
	}{
		{name: "malformed Encrypted Pack Index", malformedIndex: true},
		{name: "corrupt native pack", corruptPack: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, host, _, encoded, transport := preparedProtectedSnapshot(t, binary, test.malformedIndex, test.corruptPack)
			if _, err := transport.PublishSnapshot(encodedStorageParent(t, transport), encoded.Bootstrap, encoded.Objects); err != nil {
				t.Fatalf("publish corrupt fixture: %v", err)
			}
			assertCloneFailsWithoutDestination(t, binary, root, host, filepath.Join(root, "recovered"))
		})
	}
}

func preparedProtectedSnapshot(t *testing.T, binary string, malformedIndex, corruptPack bool) (root, host, workspace string, encoded cloakformat.EncodedSnapshot, transport *storage.LocalBare) {
	t.Helper()
	root = t.TempDir()
	host = filepath.Join(root, "host.git")
	workspace = filepath.Join(root, "workspace")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", workspace)
	if err := os.WriteFile(filepath.Join(workspace, "protected.txt"), []byte("protected\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, workspace, "add", ".")
	mustGit(t, workspace, "commit", "-m", "protected commit")
	mustInit(t, binary, workspace, host, testMnemonic)

	recoverySecret, err := secret.Parse(testMnemonic)
	if err != nil {
		t.Fatal(err)
	}
	transport, err = storage.OpenLocalBare(host)
	if err != nil {
		t.Fatal(err)
	}
	current, err := transport.Read()
	if err != nil {
		t.Fatal(err)
	}
	empty, err := cloakformat.NewRegistry().DecodeEmpty(recoverySecret, current.Bootstrap, current.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := gitdb.CreatePack(filepath.Join(workspace, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if malformedIndex {
		payload.ObjectIDs = []string{strings.Repeat("f", 40)}
	}
	if corruptPack {
		payload.Pack = []byte("not a native Git pack")
	}
	commit := strings.TrimSpace(mustGit(t, workspace, "rev-parse", "HEAD"))
	encoded, err = cloakformat.NewRegistry().EncodeSnapshot(recoverySecret, cloakformat.SnapshotInput{
		Repository: cloakformat.Repository{
			RepositoryID: empty.RepositoryID, Generation: 2, LogicalHEAD: empty.LogicalHEAD,
			ObjectFormat: empty.ObjectFormat, LogicalRefs: map[string]string{"refs/heads/main": commit},
			PreviousStorageRef: current.StorageID,
		},
		Packs: []cloakformat.PackPayload{payload},
	})
	if err != nil {
		t.Fatal(err)
	}
	return root, host, workspace, encoded, transport
}

func encodedStorageParent(t *testing.T, transport *storage.LocalBare) string {
	t.Helper()
	current, err := transport.Current()
	if err != nil {
		t.Fatal(err)
	}
	return current
}

func assertCloneFailsWithoutDestination(t *testing.T, binary, root, host, destination string) {
	t.Helper()
	clone := exec.Command(binary, "clone", host, destination)
	clone.Dir = root
	clone.Env = cloakGitEnvironment(binary)
	if output, err := clone.CombinedOutput(); err == nil {
		t.Fatalf("clone of corrupt snapshot succeeded:\n%s", output)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("failed recovery exposed a partial destination: %v", err)
	}
}
