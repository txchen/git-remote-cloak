package acceptance_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/hkdf"
)

func TestReachableCiphertextRepositoryObjectsContainNoProtectedPlaintext(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	host := filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", workspace)
	privatePath := "original-private-path.txt"
	privateContent := "the private rain falls mainly in the plaintext workspace\n"
	privateMessage := "original private commit message"
	if err := os.WriteFile(filepath.Join(workspace, privatePath), []byte(privateContent), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, workspace, "add", privatePath)
	mustGit(t, workspace, "commit", "-m", privateMessage)
	logicalCommit := mustGit(t, workspace, "rev-parse", "HEAD")

	command := exec.Command(binary, "init", "backup", host)
	command.Dir = workspace
	command.Env = append(withoutEnvironment(os.Environ(), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE"), "CLOAK_RECOVERY_SECRET="+testMnemonic)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("init failed: %v\n%s", err, output)
	}
	if got := mustGit(t, workspace, "rev-parse", "HEAD"); got != logicalCommit {
		t.Fatalf("init changed local Logical Ref from %q to %q", logicalCommit, got)
	}

	reachable := mustGit(t, host, "rev-list", "--objects", "--all")
	var inspected bytes.Buffer
	inspected.WriteString(reachable)
	for _, line := range strings.Split(strings.TrimSpace(reachable), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		inspected.WriteString(mustGit(t, host, "cat-file", "-p", fields[0]))
	}
	for _, protected := range []string{
		testMnemonic,
		"refs/heads/main",
		privatePath,
		privateContent,
		privateMessage,
		strings.TrimSpace(logicalCommit),
	} {
		if bytes.Contains(inspected.Bytes(), []byte(protected)) {
			t.Fatalf("reachable Ciphertext Repository objects expose Protected Plaintext %q", protected)
		}
	}
	repositoryID, err := hex.DecodeString(strings.TrimSpace(mustGit(t, workspace, "config", "--get", "remote.backup.cloakRepositoryID")))
	if err != nil {
		t.Fatal(err)
	}
	rawRecoverySecret := make([]byte, 32)
	if bytes.Contains(inspected.Bytes(), rawRecoverySecret) {
		t.Fatal("reachable Ciphertext Repository objects expose the raw Recovery Secret")
	}
	for _, purpose := range []string{
		"bootstrap-header-authentication",
		"manifest-encryption",
		"pack-index-encryption",
		"pack-payload-encryption",
		"stable-metadata-identifier",
	} {
		derivedKey := make([]byte, 32)
		reader := hkdf.New(sha256.New, rawRecoverySecret, repositoryID, []byte("git-remote-cloak/v1/aes-256-gcm-siv/"+purpose))
		if _, err := io.ReadFull(reader, derivedKey); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(inspected.Bytes(), derivedKey) {
			t.Fatalf("reachable Ciphertext Repository objects expose the %s derived key", purpose)
		}
	}
}
