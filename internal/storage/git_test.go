package storage

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGitFetchesRetainedHistoryAcrossAParentlessRoot(t *testing.T) {
	hostPath := filepath.Join(t.TempDir(), "host.git")
	if output, err := exec.Command("git", "init", "--bare", hostPath).CombinedOutput(); err != nil {
		t.Fatalf("initialize local Repository Host: %v\n%s", err, output)
	}
	host, err := OpenLocalBare(hostPath)
	if err != nil {
		t.Fatal(err)
	}
	zero, err := host.Current()
	if err != nil {
		t.Fatal(err)
	}
	first, err := host.PublishSnapshot(zero, []byte("first bootstrap"), map[string][]byte{"first": []byte("ciphertext")})
	if err != nil {
		t.Fatal(err)
	}
	second, err := host.PublishSnapshot(first, []byte("second bootstrap"), map[string][]byte{"second": []byte("ciphertext")})
	if err != nil {
		t.Fatal(err)
	}
	root, err := host.PrepareRootSnapshot(second, []byte("root bootstrap"), map[string][]byte{"root": []byte("ciphertext")})
	if err != nil {
		t.Fatal(err)
	}
	if err := host.PublishPrepared(second, root); err != nil {
		t.Fatal(err)
	}

	transport, err := OpenGit(hostPath)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	gotRoot, err := transport.StorageHistoryRoot(root)
	if err != nil || gotRoot != root {
		t.Fatalf("Storage History root = %q, err=%v, want %q", gotRoot, err, root)
	}
	if err := transport.FetchStorageCommit(second); err != nil {
		t.Fatalf("fetch retained pre-root Storage commit: %v", err)
	}
	if !transport.StorageHistoryContinues(first, second) {
		t.Fatal("fetched pre-root Storage History omitted its parent")
	}
	if err := transport.FetchStorageCommit("not-an-object-id"); err == nil {
		t.Fatal("invalid historical Storage commit ID was accepted")
	}
}
