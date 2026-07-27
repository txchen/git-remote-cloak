package localstate

import (
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Cache persists only public metadata and ciphertext beneath one Git directory.
type Cache struct {
	root string
}

// NewCache returns the reconstructable cache owned by gitDirectory.
func NewCache(gitDirectory string) *Cache {
	if gitDirectory == "" {
		return nil
	}
	return &Cache{root: filepath.Join(gitDirectory, "cloak", "cache")}
}

// ClearCache removes only reconstructable cached data.
func ClearCache(gitDirectory string) error {
	if gitDirectory == "" {
		return errors.New("Git directory is required")
	}
	return os.RemoveAll(filepath.Join(gitDirectory, "cloak", "cache"))
}

// ReadObject returns a content-addressed ciphertext entry. Damaged entries are
// isolated by removing them and are reported as cache misses.
func (cache *Cache) ReadObject(locator string) ([]byte, bool) {
	if cache == nil || !validLocator(locator) {
		return nil, false
	}
	path := filepath.Join(cache.root, "objects", locator)
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	if ciphertextLocator(contents) != locator {
		_ = os.Remove(path)
		return nil, false
	}
	return contents, true
}

// StoreSnapshot atomically adds authenticated snapshot inputs to the cache.
// Existing valid immutable entries are reused without rewriting them.
func (cache *Cache) StoreSnapshot(storageCommitID string, bootstrap []byte, objects map[string][]byte) error {
	if cache == nil {
		return nil
	}
	for locator, contents := range objects {
		if !validLocator(locator) || ciphertextLocator(contents) != locator {
			continue
		}
		if _, valid := cache.ReadObject(locator); valid {
			continue
		}
		if err := atomicWrite(filepath.Join(cache.root, "objects", locator), contents); err != nil {
			return err
		}
	}
	if storageCommitID != "" && isLowercaseAlphanumeric(storageCommitID) {
		return atomicWrite(filepath.Join(cache.root, "snapshots", storageCommitID, "bootstrap"), bootstrap)
	}
	return nil
}

func atomicWrite(path string, contents []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".cloak-write-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	remove := true
	defer func() {
		_ = temporary.Close()
		if remove {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	remove = false
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer directoryHandle.Close()
	return directoryHandle.Sync()
}

func ciphertextLocator(contents []byte) string {
	digest := sha256.Sum256(contents)
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(digest[:]))
}

func validLocator(locator string) bool {
	return len(locator) == 52 && isLowercaseAlphanumeric(locator)
}

func isLowercaseAlphanumeric(value string) bool {
	if value == "" || strings.Contains(value, ".") {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'z' {
				return false
			}
		}
	}
	return true
}
