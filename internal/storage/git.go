package storage

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/txchen/git-remote-cloak/internal/gitexec"
)

// Git is the production Storage Transport adapter for local, SSH, and HTTPS Repository Hosts.
type Git struct {
	*LocalBare
	temporaryRoot string
}

// ErrConcurrentUpdate reports that another writer changed the Storage Ref
// after this transport read it.
var ErrConcurrentUpdate = errors.New("Storage Ref changed concurrently")

var (
	testStorageRefBarrierOnce sync.Once
	testStorageRefBarrierErr  error
)

// OpenGit clones the Repository Host through ordinary Git transport into restrictive local storage.
func OpenGit(repositoryURL string) (*Git, error) {
	if repositoryURL == "" {
		return nil, fmt.Errorf("Repository Host URL is required")
	}
	temporaryRoot, err := os.MkdirTemp("", "git-remote-cloak-storage-")
	if err != nil {
		return nil, fmt.Errorf("create temporary Storage Transport repository: %w", err)
	}
	gitDirectory := filepath.Join(temporaryRoot, "repository.git")
	command := exec.Command("git", "clone", "--bare", "--no-checkout", "--filter=blob:none", repositoryURL, gitDirectory)
	command.Env = gitexec.Environment(os.Environ())
	if output, err := command.CombinedOutput(); err != nil {
		_ = os.RemoveAll(temporaryRoot)
		return nil, fmt.Errorf("open Repository Host through ordinary Git transport: %s", strings.TrimSpace(string(output)))
	}
	objectFormat, err := runGit(gitDirectory, nil, "rev-parse", "--show-object-format")
	if err != nil {
		_ = os.RemoveAll(temporaryRoot)
		return nil, fmt.Errorf("read Repository Host object format: %w", err)
	}
	zeroObject := strings.Repeat("0", 40)
	if strings.TrimSpace(string(objectFormat)) == "sha256" {
		zeroObject = strings.Repeat("0", 64)
	}
	return &Git{LocalBare: &LocalBare{path: gitDirectory, zeroObject: zeroObject}, temporaryRoot: temporaryRoot}, nil
}

// Close removes the adapter's reconstructable local ciphertext clone.
func (transport *Git) Close() error {
	return os.RemoveAll(transport.temporaryRoot)
}

// PrefetchSnapshotBlobs fetches blobs absent from a filtered clone in batches.
// Cached ciphertext can be read later without downloading its Git blob.
func (transport *Git) PrefetchSnapshotBlobs(storageCommitID string, hasCachedObject func(string) bool) error {
	if !validStorageCommitID(storageCommitID) || storageCommitID == transport.zeroObject {
		return errors.New("invalid Storage commit ID")
	}
	output, err := runGit(transport.path, nil, "rev-list", "--objects", "--missing=print", "--max-count=1", storageCommitID)
	if err != nil {
		return err
	}
	missing := make(map[string]bool)
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "?") && validStorageCommitID(line[1:]) {
			missing[line[1:]] = true
		}
	}
	if len(missing) == 0 {
		return nil
	}
	tree, err := runGit(transport.path, nil, "ls-tree", "-r", "-z", storageCommitID)
	if err != nil {
		return err
	}
	objectIDs := make([]string, 0, len(missing))
	for _, entry := range strings.Split(string(tree), "\x00") {
		metadata, path, found := strings.Cut(entry, "\t")
		if !found {
			continue
		}
		fields := strings.Fields(metadata)
		if len(fields) != 3 || fields[1] != "blob" || !missing[fields[2]] {
			continue
		}
		if path != "bootstrap" {
			locator, ok := strings.CutPrefix(path, "objects/")
			if !ok || locator == "" || strings.Contains(locator, "/") || hasCachedObject != nil && hasCachedObject(locator) {
				continue
			}
		}
		objectIDs = append(objectIDs, fields[2])
		delete(missing, fields[2])
	}
	for start := 0; start < len(objectIDs); start += 512 {
		end := min(start+512, len(objectIDs))
		arguments := append([]string{"fetch", "--no-tags", "origin"}, objectIDs[start:end]...)
		if _, err := runGit(transport.path, nil, arguments...); err != nil {
			return err
		}
	}
	return nil
}

// PublishSnapshot uploads immutable ciphertext and compare-and-swap publishes through ordinary Git push.
func (transport *Git) PublishSnapshot(expectedStorageCommitID string, bootstrap []byte, ciphertextObjects map[string][]byte) (string, error) {
	commitID, err := transport.PrepareSnapshot(expectedStorageCommitID, bootstrap, ciphertextObjects)
	if err != nil {
		return "", err
	}
	if err := transport.PublishPrepared(expectedStorageCommitID, commitID); err != nil {
		return "", err
	}
	return commitID, nil
}

// PrepareRootSnapshot uploads immutable ciphertext and creates a parentless
// Storage commit for a maintenance rebuild.
func (transport *Git) PrepareRootSnapshot(expectedStorageCommitID string, bootstrap []byte, ciphertextObjects map[string][]byte) (string, error) {
	return transport.LocalBare.PrepareRootSnapshot(expectedStorageCommitID, bootstrap, ciphertextObjects)
}

// PublishPrepared uploads a prepared immutable Storage commit and updates the
// Storage Ref. Transient transport failures receive at most three attempts.
func (transport *Git) PublishPrepared(expectedStorageCommitID, commitID string) error {
	if err := waitAtTestStorageRefBarrier(); err != nil {
		return err
	}
	if os.Getenv("CLOAK_TEST_FAULT") == "before-storage-ref" {
		return errors.New("injected interruption before Storage Ref publication")
	}
	if os.Getenv("CLOAK_TEST_FAULT") == "lost-process-before-storage-ref" {
		os.Exit(86)
	}
	if os.Getenv("CLOAK_TEST_FAULT") == "stale-storage-ref" && expectedStorageCommitID != transport.zeroObject {
		tree, err := runGit(transport.path, nil, "rev-parse", expectedStorageCommitID+"^{tree}")
		if err != nil {
			return err
		}
		concurrentCommit, err := runGit(transport.path, []byte("concurrent storage publication\n"), "commit-tree", strings.TrimSpace(string(tree)), "-p", expectedStorageCommitID)
		if err != nil {
			return err
		}
		if _, err := runGit(transport.path, nil, "push", "origin", strings.TrimSpace(string(concurrentCommit))+":"+StorageRef); err != nil {
			return err
		}
	}
	lease := "--force-with-lease=" + StorageRef + ":" + expectedStorageCommitID
	if expectedStorageCommitID == transport.zeroObject {
		lease = "--force-with-lease=" + StorageRef + ":"
	}
	var lastError error
	for attempt := 0; attempt < 3; attempt++ {
		if os.Getenv("CLOAK_TEST_FAULT") == "immutable-upload-failure" {
			lastError = errors.New("injected immutable-object upload failure")
			continue
		}
		if _, err := runGit(transport.path, nil, "push", lease, "origin", commitID+":"+StorageRef); err == nil {
			if os.Getenv("CLOAK_TEST_FAULT") == "after-storage-ref" {
				return errors.New("injected lost response after Storage Ref publication")
			}
			if os.Getenv("CLOAK_TEST_FAULT") == "lost-process-after-storage-ref" {
				os.Exit(87)
			}
			return nil
		} else {
			lastError = err
			if concurrentPushError(err.Error()) {
				return fmt.Errorf("%w: %v", ErrConcurrentUpdate, err)
			}
			if nonRetryablePushError(err.Error()) {
				break
			}
		}
	}
	return fmt.Errorf("compare-and-swap publish Storage Ref through ordinary Git transport failed after at most 3 attempts: %w", lastError)
}

func waitAtTestStorageRefBarrier() error {
	directory := os.Getenv("CLOAK_TEST_STORAGE_REF_BARRIER")
	participant := os.Getenv("CLOAK_TEST_STORAGE_REF_PARTICIPANT")
	if directory == "" && participant == "" {
		return nil
	}
	testStorageRefBarrierOnce.Do(func() {
		if directory == "" || participant == "" || filepath.Base(participant) != participant {
			testStorageRefBarrierErr = errors.New("invalid test Storage Ref barrier configuration")
			return
		}
		if err := os.WriteFile(filepath.Join(directory, participant+".ready"), []byte("ready\n"), 0o600); err != nil {
			testStorageRefBarrierErr = fmt.Errorf("signal test Storage Ref barrier: %w", err)
			return
		}
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(filepath.Join(directory, "release")); err == nil {
				return
			} else if !os.IsNotExist(err) {
				testStorageRefBarrierErr = fmt.Errorf("inspect test Storage Ref barrier: %w", err)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		testStorageRefBarrierErr = errors.New("timed out waiting at test Storage Ref barrier")
	})
	return testStorageRefBarrierErr
}

func concurrentPushError(message string) bool {
	lower := strings.ToLower(message)
	for _, fragment := range []string{"stale info", "incorrect old value", "but expected"} {
		if strings.Contains(lower, fragment) {
			return true
		}
	}
	return false
}

// ContainsStorageCommit reports whether a commit is retained in the fetched
// Storage History.
func (transport *Git) ContainsStorageCommit(storageCommitID string) bool {
	_, err := runGit(transport.path, nil, "cat-file", "-e", storageCommitID+"^{commit}")
	return err == nil
}

// FetchStorageCommit obtains one retained historical Storage commit by object ID without changing Storage Ref state.
func (transport *Git) FetchStorageCommit(storageCommitID string) error {
	if !validStorageCommitID(storageCommitID) {
		return errors.New("invalid historical Storage commit ID")
	}
	if transport.ContainsStorageCommit(storageCommitID) {
		return nil
	}
	if output, err := runGit(transport.path, nil, "fetch", "--no-tags", "origin", storageCommitID); err != nil {
		return fmt.Errorf("fetch retained historical Storage commit: %s", strings.TrimSpace(string(output)))
	}
	if !transport.ContainsStorageCommit(storageCommitID) {
		return errors.New("Repository Host did not return retained historical Storage commit")
	}
	return nil
}

// StorageHistoryRoot returns the unique parentless root reachable from one Storage History tip.
func (transport *Git) StorageHistoryRoot(storageCommitID string) (string, error) {
	if !validStorageCommitID(storageCommitID) {
		return "", errors.New("invalid Storage History tip")
	}
	output, err := runGit(transport.path, nil, "rev-list", "--max-parents=0", storageCommitID)
	if err != nil {
		return "", fmt.Errorf("find Storage History root: %w", err)
	}
	roots := strings.Fields(string(output))
	if len(roots) != 1 || !validStorageCommitID(roots[0]) {
		return "", errors.New("Storage History does not have exactly one parentless root")
	}
	return roots[0], nil
}

// StorageHistoryContinues reports whether newerStorageCommitID descends from
// the previously trusted Storage History commit.
func (transport *Git) StorageHistoryContinues(previousStorageCommitID, newerStorageCommitID string) bool {
	_, err := runGit(transport.path, nil, "merge-base", "--is-ancestor", previousStorageCommitID, newerStorageCommitID)
	return err == nil
}

func validStorageCommitID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

// PublishEmpty creates the initial Ciphertext Snapshot through ordinary Git transport.
func (transport *Git) PublishEmpty(bootstrap, manifest []byte, locator string) error {
	_, err := transport.PublishSnapshot(transport.zeroObject, bootstrap, map[string][]byte{locator: manifest})
	return err
}

func nonRetryablePushError(message string) bool {
	lower := strings.ToLower(message)
	for _, fragment := range []string{
		"authentication failed", "permission denied", "access denied", "could not read username",
		"rejected", "stale info", "non-fast-forward", "cannot lock ref", "fetch first",
	} {
		if strings.Contains(lower, fragment) {
			return true
		}
	}
	return false
}
