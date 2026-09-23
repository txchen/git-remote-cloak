package localstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/txchen/git-remote-cloak/internal/domain"
	"github.com/txchen/git-remote-cloak/internal/gitexec"
	"github.com/txchen/git-remote-cloak/internal/secret"
	"golang.org/x/sys/unix"
)

// SecretDirectory follows Git's common directory so linked worktrees share the
// repository's credential, without depending on the working directory or HOME.
func SecretDirectory(gitDirectory string) (string, error) {
	if gitDirectory == "" {
		return "", errors.New("Recovery Secret persistence requires a Git directory")
	}
	command := exec.Command("git", "--git-dir="+gitDirectory, "rev-parse", "--path-format=absolute", "--git-common-dir")
	command.Env = gitexec.Environment(os.Environ())
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("locate repository Recovery Secret: %w", err)
	}
	directory := filepath.Join(strings.TrimSpace(string(output)), "cloak")
	info, err := os.Lstat(directory)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return "", errors.New("repository Recovery Secret directory must be a real directory")
	}
	return directory, nil
}

// AcquireSecretLock serializes initialization, Rekey and interrupted Rekey
// recovery across all worktrees. Callers hold it throughout the transition.
func AcquireSecretLock(gitDirectory string) (*OperationLock, error) {
	directory, err := SecretDirectory(gitDirectory)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(filepath.Join(directory, "secret.lock"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil && !info.Mode().IsRegular() {
		return nil, errors.New("invalid Recovery Secret lock file")
	}
	return acquireLock(filepath.Dir(directory), "secret.lock", unix.LOCK_EX|unix.LOCK_NB)
}

func readSecretState(gitDirectory, name string) ([]byte, bool, error) {
	directory, err := SecretDirectory(gitDirectory)
	if err != nil {
		return nil, false, err
	}
	path := filepath.Join(directory, name)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, false, fmt.Errorf("repository Recovery Secret file %s must be a regular file with permissions 0600", name)
	}
	contents, err := os.ReadFile(path)
	return contents, true, err
}

// LoadRecoverySecret fails closed on corrupt or unsafe managed credentials.
func LoadRecoverySecret(gitDirectory string) (domain.RecoverySecret, bool, error) {
	contents, exists, err := readSecretState(gitDirectory, "secret")
	if err != nil || !exists {
		return domain.RecoverySecret{}, exists, err
	}
	key, err := secret.Parse(string(contents))
	if err != nil {
		return key, true, fmt.Errorf("repository Recovery Secret file is damaged: %w", err)
	}
	return key, true, nil
}

// EnsureRecoverySecret never overwrites a different credential during init.
// The caller must hold the secret lock, or own an unpublished clone directory.
func EnsureRecoverySecret(gitDirectory string, key domain.RecoverySecret) error {
	current, exists, err := LoadRecoverySecret(gitDirectory)
	if err != nil {
		return err
	}
	if exists {
		if current != key {
			return errors.New("repository already stores a different Recovery Secret; use Rekey to change its identity")
		}
		return nil
	}
	return ReplaceRecoverySecret(gitDirectory, key)
}

// ReplaceRecoverySecret atomically installs a confirmed credential.
func ReplaceRecoverySecret(gitDirectory string, key domain.RecoverySecret) error {
	directory, err := SecretDirectory(gitDirectory)
	if err != nil {
		return err
	}
	if _, _, err := readSecretState(gitDirectory, "secret"); err != nil {
		return err
	}
	mnemonic, err := secret.Mnemonic(key)
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(directory, "secret"), []byte(mnemonic+"\n"))
}

// PendingSecret durably retains the new credential before Rekey publication.
// This dedicated secret file is not a cache or a public transaction journal.
type PendingSecret struct {
	Version        int                   `json:"version"`
	Mnemonic       string                `json:"recovery_mnemonic"`
	RepositoryURL  string                `json:"repository_url"`
	RepositoryID   domain.RepositoryID   `json:"repository_id"`
	Secret         domain.RecoverySecret `json:"-"`
	StartingCommit string                `json:"starting_commit"`
	PreparedCommit string                `json:"prepared_commit"`
}

func LoadPendingSecret(gitDirectory string) (PendingSecret, bool, error) {
	contents, exists, err := readSecretState(gitDirectory, "secret.pending")
	if err != nil || !exists {
		return PendingSecret{}, exists, err
	}
	var pending PendingSecret
	if json.Unmarshal(contents, &pending) != nil || pending.Version != 1 || pending.RepositoryURL == "" || !validGitObjectID(pending.StartingCommit) || !validGitObjectID(pending.PreparedCommit) {
		return PendingSecret{}, true, errors.New("pending Recovery Secret file is damaged")
	}
	pending.Secret, err = secret.Parse(pending.Mnemonic)
	if err != nil {
		return PendingSecret{}, true, errors.New("pending Recovery Secret file is damaged")
	}
	return pending, true, nil
}

func StorePendingSecret(gitDirectory string, pending PendingSecret) error {
	directory, err := SecretDirectory(gitDirectory)
	if err != nil {
		return err
	}
	if _, _, err := readSecretState(gitDirectory, "secret.pending"); err != nil {
		return err
	}
	pending.Version = 1
	pending.Mnemonic, err = secret.Mnemonic(pending.Secret)
	if err != nil {
		return err
	}
	contents, err := json.Marshal(pending)
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(directory, "secret.pending"), append(contents, '\n'))
}

func ClearPendingSecret(gitDirectory string) error {
	directory, err := SecretDirectory(gitDirectory)
	if err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(directory, "secret.pending")); err != nil && !os.IsNotExist(err) {
		return err
	}
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
