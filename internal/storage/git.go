package storage

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Git is the production Storage Transport adapter for local, SSH, and HTTPS Repository Hosts.
type Git struct {
	*LocalBare
	temporaryRoot string
}

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
	command.Env = append(cleanStorageEnvironment(os.Environ()), "GIT_CONFIG_NOSYSTEM=1")
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

// PublishSnapshot uploads immutable ciphertext and compare-and-swap publishes through ordinary Git push.
func (transport *Git) PublishSnapshot(expectedStorageCommitID string, bootstrap []byte, ciphertextObjects map[string][]byte) (string, error) {
	commitID, err := transport.LocalBare.PublishSnapshot(expectedStorageCommitID, bootstrap, ciphertextObjects)
	if err != nil {
		return "", err
	}
	lease := "--force-with-lease=" + StorageRef + ":" + expectedStorageCommitID
	if expectedStorageCommitID == transport.zeroObject {
		lease = "--force-with-lease=" + StorageRef + ":"
	}
	if _, err := runGit(transport.path, nil, "push", lease, "origin", commitID+":"+StorageRef); err != nil {
		return "", fmt.Errorf("compare-and-swap publish Storage Ref through ordinary Git transport: %w", err)
	}
	return commitID, nil
}

// PublishEmpty creates the initial Ciphertext Snapshot through ordinary Git transport.
func (transport *Git) PublishEmpty(bootstrap, manifest []byte, locator string) error {
	_, err := transport.PublishSnapshot(transport.zeroObject, bootstrap, map[string][]byte{locator: manifest})
	return err
}

func cleanStorageEnvironment(environment []string) []string {
	clean := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if name != "GIT_DIR" && name != "GIT_WORK_TREE" && name != "GIT_INDEX_FILE" && name != "GIT_OBJECT_DIRECTORY" && name != "GIT_ALTERNATE_OBJECT_DIRECTORIES" {
			clean = append(clean, entry)
		}
	}
	return clean
}
