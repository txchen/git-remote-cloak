// Package storage provides the ordinary-Git Storage Transport seam.
package storage

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const StorageRef = "refs/heads/cloak-storage"

const (
	maximumBootstrapBlobSize  = 16 + 64*1024
	maximumCiphertextBlobSize = 32*1024*1024 + 28
)

// Snapshot is the byte representation read through the Storage Transport.
type Snapshot struct {
	Bootstrap         []byte
	Manifest          []byte
	CiphertextObjects map[string][]byte
	StorageCommitID   string
}

// LocalBare is a deterministic adapter for a local bare Repository Host.
type LocalBare struct {
	path       string
	zeroObject string
}

// OpenLocalBare validates and opens a local path or file URL.
func OpenLocalBare(repositoryURL string) (*LocalBare, error) {
	path := repositoryURL
	if strings.HasPrefix(repositoryURL, "file://") {
		parsed, err := url.Parse(repositoryURL)
		if err != nil || parsed.Host != "" {
			return nil, errors.New("unsupported local Repository Host URL")
		}
		path = parsed.Path
	}
	if !filepath.IsAbs(path) {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		path = absolute
	}
	if output, err := runGit(path, nil, "rev-parse", "--is-bare-repository"); err != nil || strings.TrimSpace(string(output)) != "true" {
		return nil, errors.New("Repository Host is not a local bare Git repository")
	}
	objectFormat, err := runGit(path, nil, "rev-parse", "--show-object-format")
	if err != nil {
		return nil, fmt.Errorf("read Repository Host object format: %w", err)
	}
	zeroObject := strings.Repeat("0", 40)
	if strings.TrimSpace(string(objectFormat)) == "sha256" {
		zeroObject = strings.Repeat("0", 64)
	}
	return &LocalBare{path: path, zeroObject: zeroObject}, nil
}

// Refs returns every public ref on the Repository Host.
func (transport *LocalBare) Refs() ([]string, error) {
	output, err := runGit(transport.path, nil, "for-each-ref", "--format=%(refname)")
	if err != nil {
		return nil, err
	}
	if len(output) == 0 {
		return nil, nil
	}
	return strings.Fields(string(output)), nil
}

// Current returns the current Storage Ref object ID or the all-zero object ID.
func (transport *LocalBare) Current() (string, error) {
	output, err := runGit(transport.path, nil, "for-each-ref", "--format=%(objectname)", StorageRef)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(output)) == "" {
		return transport.zeroObject, nil
	}
	return strings.TrimSpace(string(output)), nil
}

// Read obtains the current complete Ciphertext Snapshot.
func (transport *LocalBare) Read() (Snapshot, error) {
	bootstrap, storageID, err := transport.ReadBootstrap()
	if err != nil {
		return Snapshot{}, err
	}
	paths, err := runGit(transport.path, nil, "ls-tree", "-r", "--name-only", storageID, "objects")
	if err != nil {
		return Snapshot{}, fmt.Errorf("list encrypted manifest: %w", err)
	}
	objectPaths := strings.Fields(string(paths))
	if len(objectPaths) == 0 {
		return Snapshot{}, errors.New("Ciphertext Snapshot contains no encrypted objects")
	}
	objects := make(map[string][]byte, len(objectPaths))
	for _, objectPath := range objectPaths {
		if !strings.HasPrefix(objectPath, "objects/") || strings.Count(objectPath, "/") != 1 {
			return Snapshot{}, errors.New("Ciphertext Snapshot contains an invalid encrypted object path")
		}
		contents, err := transport.ReadObject(storageID, strings.TrimPrefix(objectPath, "objects/"))
		if err != nil {
			return Snapshot{}, fmt.Errorf("read encrypted object: %w", err)
		}
		objects[strings.TrimPrefix(objectPath, "objects/")] = contents
	}
	snapshot := Snapshot{Bootstrap: bootstrap, CiphertextObjects: objects, StorageCommitID: storageID}
	if len(objects) == 1 {
		for _, contents := range objects {
			snapshot.Manifest = contents
		}
	}
	return snapshot, nil
}

// ReadBootstrap reads only the bounded public Bootstrap Header from one fixed Storage History commit.
func (transport *LocalBare) ReadBootstrap() ([]byte, string, error) {
	storageCommitID, err := transport.Current()
	if err != nil {
		return nil, "", err
	}
	if storageCommitID == transport.zeroObject {
		return nil, "", errors.New("Storage Ref does not exist")
	}
	bootstrap, err := transport.ReadBootstrapAt(storageCommitID)
	return bootstrap, storageCommitID, err
}

// ReadBootstrapAt reads the bounded Bootstrap Header from one explicit
// retained Storage History commit without changing the Storage Ref.
func (transport *LocalBare) ReadBootstrapAt(storageCommitID string) ([]byte, error) {
	bootstrap, err := transport.readBoundedBlob(storageCommitID+":bootstrap", maximumBootstrapBlobSize)
	if err != nil {
		return nil, fmt.Errorf("read Bootstrap Header: %w", err)
	}
	return bootstrap, nil
}

// ReadObject resolves one authenticated Opaque Segment Identifier from a fixed Storage History commit.
func (transport *LocalBare) ReadObject(storageCommitID, locator string) ([]byte, error) {
	if locator == "" || strings.Contains(locator, "/") {
		return nil, errors.New("invalid Opaque Segment Identifier")
	}
	object, err := transport.readBoundedBlob(storageCommitID+":objects/"+locator, maximumCiphertextBlobSize)
	if err != nil {
		return nil, fmt.Errorf("read ciphertext object: %w", err)
	}
	return object, nil
}

func (transport *LocalBare) readBoundedBlob(revision string, maximumSize int) ([]byte, error) {
	objectID, err := runGit(transport.path, nil, "rev-parse", "--verify", revision)
	if err != nil {
		return nil, err
	}
	sizeOutput, err := runGit(transport.path, nil, "cat-file", "-s", strings.TrimSpace(string(objectID)))
	if err != nil {
		return nil, err
	}
	var size int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(sizeOutput)), "%d", &size); err != nil || size < 0 || size > maximumSize {
		return nil, errors.New("Ciphertext Repository blob exceeds its strict allocation limit")
	}
	return runGit(transport.path, nil, "cat-file", "blob", strings.TrimSpace(string(objectID)))
}

// PublishEmpty uploads immutable objects and compare-and-swap creates the Storage Ref.
func (transport *LocalBare) PublishEmpty(bootstrap, manifest []byte, locator string) error {
	_, err := transport.PublishSnapshot(transport.zeroObject, bootstrap, map[string][]byte{locator: manifest})
	return err
}

// PublishSnapshot uploads immutable ciphertext before compare-and-swap publishing one Storage History commit.
func (transport *LocalBare) PublishSnapshot(expectedStorageCommitID string, bootstrap []byte, ciphertextObjects map[string][]byte) (string, error) {
	commitID, err := transport.PrepareSnapshot(expectedStorageCommitID, bootstrap, ciphertextObjects)
	if err != nil {
		return "", err
	}
	if err := transport.PublishPrepared(expectedStorageCommitID, commitID); err != nil {
		return "", err
	}
	return commitID, nil
}

// PrepareSnapshot uploads immutable ciphertext and builds a Storage commit
// without changing the authoritative Storage Ref.
func (transport *LocalBare) PrepareSnapshot(expectedStorageCommitID string, bootstrap []byte, ciphertextObjects map[string][]byte) (string, error) {
	if len(ciphertextObjects) == 0 {
		return "", errors.New("Ciphertext Snapshot contains no encrypted objects")
	}
	bootstrapOID, err := transport.writeBlob(bootstrap)
	if err != nil {
		return "", err
	}
	locators := make([]string, 0, len(ciphertextObjects))
	for locator := range ciphertextObjects {
		locators = append(locators, locator)
	}
	sort.Strings(locators)
	var objectTreeInput strings.Builder
	for _, locator := range locators {
		if locator == "" || strings.Contains(locator, "/") {
			return "", errors.New("invalid opaque ciphertext object locator")
		}
		objectID, err := transport.writeBlob(ciphertextObjects[locator])
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&objectTreeInput, "100644 blob %s\t%s\n", objectID, locator)
	}
	objectsTree, err := runGit(transport.path, []byte(objectTreeInput.String()), "mktree")
	if err != nil {
		return "", fmt.Errorf("build ciphertext objects tree: %w", err)
	}
	rootInput := fmt.Sprintf("100644 blob %s\tbootstrap\n040000 tree %s\tobjects\n", bootstrapOID, strings.TrimSpace(string(objectsTree)))
	rootTree, err := runGit(transport.path, []byte(rootInput), "mktree")
	if err != nil {
		return "", fmt.Errorf("build Ciphertext Snapshot tree: %w", err)
	}
	commitArguments := []string{"commit-tree", strings.TrimSpace(string(rootTree))}
	if expectedStorageCommitID != transport.zeroObject {
		commitArguments = append(commitArguments, "-p", expectedStorageCommitID)
	}
	commit, err := runGit(transport.path, []byte("cloak snapshot\n"), commitArguments...)
	if err != nil {
		return "", fmt.Errorf("build Storage History commit: %w", err)
	}
	commitID := strings.TrimSpace(string(commit))
	return commitID, nil
}

// PublishPrepared atomically changes the Storage Ref to a prepared commit.
func (transport *LocalBare) PublishPrepared(expectedStorageCommitID, commitID string) error {
	if _, err := runGit(transport.path, nil, "update-ref", StorageRef, commitID, expectedStorageCommitID); err != nil {
		return fmt.Errorf("%w: compare-and-swap publish Storage Ref: %v", ErrConcurrentUpdate, err)
	}
	return nil
}

func (transport *LocalBare) writeBlob(contents []byte) (string, error) {
	output, err := runGit(transport.path, contents, "hash-object", "-w", "--stdin")
	if err != nil {
		return "", fmt.Errorf("upload immutable ciphertext object: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

func runGit(gitDirectory string, stdin []byte, arguments ...string) ([]byte, error) {
	fullArguments := append([]string{"--git-dir=" + gitDirectory}, arguments...)
	command := exec.Command("git", fullArguments...)
	command.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=git-remote-cloak",
		"GIT_AUTHOR_EMAIL=cloak@invalid",
		"GIT_COMMITTER_NAME=git-remote-cloak",
		"GIT_COMMITTER_EMAIL=cloak@invalid",
		"GIT_AUTHOR_DATE=2000-01-01T00:00:00Z",
		"GIT_COMMITTER_DATE=2000-01-01T00:00:00Z",
	)
	if stdin != nil {
		command.Stdin = bytes.NewReader(stdin)
	}
	output, err := command.Output()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("git %s: %s", strings.Join(arguments, " "), strings.TrimSpace(string(exitError.Stderr)))
		}
		return nil, err
	}
	return output, nil
}
