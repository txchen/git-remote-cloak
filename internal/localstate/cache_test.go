package localstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnchangedBootstrapCacheDoesNotRewritePrivateFile(t *testing.T) {
	gitDirectory := t.TempDir()
	cache := NewCache(gitDirectory)
	commitID := strings.Repeat("a", 40)
	bootstrap := []byte("authenticated bootstrap")
	store := func() {
		t.Helper()
		if err := cache.StoreSnapshot(commitID, bootstrap, nil); err != nil {
			t.Fatal(err)
		}
	}
	store()
	path := filepath.Join(gitDirectory, "cloak", "cache", "snapshots", commitID, "bootstrap")
	first, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	store()
	second, err := os.Stat(path)
	if err != nil || !os.SameFile(first, second) {
		t.Fatalf("unchanged bootstrap cache was rewritten: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	store()
	repaired, err := os.Stat(path)
	if err != nil || repaired.Mode().Perm() != 0o600 || os.SameFile(second, repaired) {
		t.Fatalf("bootstrap cache permissions were not repaired: %v", err)
	}
}

func TestReadBootstrapRejectsOversizedOrPublicCacheEntry(t *testing.T) {
	gitDirectory := t.TempDir()
	cache := NewCache(gitDirectory)
	commitID := strings.Repeat("a", 40)
	bootstrap := []byte("authenticated bootstrap")
	if err := cache.StoreSnapshot(commitID, bootstrap, nil); err != nil {
		t.Fatal(err)
	}
	if got, ok := cache.ReadBootstrap(commitID, len(bootstrap)); !ok || string(got) != string(bootstrap) {
		t.Fatalf("valid bootstrap cache = %q, ok=%v", got, ok)
	}
	if _, ok := cache.ReadBootstrap(commitID, len(bootstrap)-1); ok {
		t.Fatal("oversized bootstrap cache entry was accepted")
	}
	path := filepath.Join(gitDirectory, "cloak", "cache", "snapshots", commitID, "bootstrap")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.ReadBootstrap(commitID, len(bootstrap)); ok {
		t.Fatal("public bootstrap cache entry was accepted")
	}
}
