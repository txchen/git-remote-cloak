package engine

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/txchen/git-remote-cloak/internal/domain"
	"github.com/txchen/git-remote-cloak/internal/localstate"
	"github.com/txchen/git-remote-cloak/internal/storage"
)

// ReconcileRecoverySecret completes a durably staged Rekey after a process
// exit or lost response. Ordinary operations incur no network access here.
func (engine *Engine) ReconcileRecoverySecret() error {
	pending, exists, err := localstate.LoadPendingSecret(engine.localGitDirectory)
	if err != nil || !exists {
		return err
	}
	lock, err := localstate.AcquireSecretLock(engine.localGitDirectory)
	if err != nil {
		return err
	}
	defer lock.Close()
	pending, exists, err = localstate.LoadPendingSecret(engine.localGitDirectory)
	if err != nil || !exists {
		return err
	}
	_, err = engine.reconcilePendingSecret(pending)
	return err
}

// The caller holds the common repository secret lock.
func (engine *Engine) reconcilePendingSecret(pending localstate.PendingSecret) (bool, error) {
	transport, err := storage.OpenGit(pending.RepositoryURL)
	if err != nil {
		return false, fmt.Errorf("resolve interrupted Rekey: %w", err)
	}
	defer transport.Close()
	current, err := transport.Current()
	if err != nil {
		return false, err
	}
	if current == pending.StartingCommit {
		return false, nil
	}
	decoded, commit, decodeErr := engine.decodeTransportSnapshotWithoutMutation(pending.Secret, transport)
	if decodeErr != nil || decoded.Repository.RepositoryID != pending.RepositoryID {
		// A concurrent writer may have won the CAS with the old identity. Keep
		// the pending key for an explicit retry, and authenticate the old state.
		old, exists, err := localstate.LoadRecoverySecret(engine.localGitDirectory)
		if err == nil && exists {
			if _, _, err := engine.decodeTransportSnapshot(old, transport); err == nil {
				return false, nil
			}
		}
		return false, errors.New("cannot resolve interrupted Rekey; retain secret.pending and retry with the original Recovery Secret")
	}
	if commit != pending.PreparedCommit && !transport.StorageHistoryContinues(pending.PreparedCommit, commit) && decoded.Repository.PreviousStorageRef != pending.PreparedCommit {
		return false, errors.New("interrupted Rekey has unexplained Storage History; pending Recovery Secret retained")
	}
	checkpoint, exists, err := localstate.LoadCheckpoint(engine.localGitDirectory)
	if err != nil {
		return false, err
	}
	if exists && checkpoint.RepositoryID == hex.EncodeToString(pending.RepositoryID[:]) {
		err = localstate.ObserveCheckpoint(engine.localGitDirectory, pending.RepositoryID, decoded.Repository.Generation, commit, decoded.Repository.PreviousStorageRef, transport.StorageHistoryContinues)
	} else {
		err = localstate.ReplaceCheckpoint(engine.localGitDirectory, pending.RepositoryID, decoded.Repository.Generation, commit)
	}
	if err != nil {
		return false, err
	}
	if err := localstate.ReplaceRecoverySecret(engine.localGitDirectory, pending.Secret); err != nil {
		return false, err
	}
	if err := engine.recordRekeyIdentity(pending.RepositoryURL, pending.RepositoryID); err != nil {
		return false, err
	}
	if err := localstate.ClearPendingSecret(engine.localGitDirectory); err != nil {
		return false, err
	}
	return true, nil
}

// Keep public remote metadata in the same recoverable transition as the key.
func (engine *Engine) recordRekeyIdentity(repositoryURL string, repositoryID domain.RepositoryID) error {
	output, err := git(".", nil, "--git-dir="+engine.localGitDirectory, "remote")
	if err != nil {
		return err
	}
	for _, name := range strings.Fields(string(output)) {
		url, err := git(".", nil, "--git-dir="+engine.localGitDirectory, "remote", "get-url", name)
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(url)) != "cloak::"+repositoryURL {
			continue
		}
		if _, err := git(".", nil, "--git-dir="+engine.localGitDirectory, "config", "remote."+name+".cloakRepositoryID", hex.EncodeToString(repositoryID[:])); err != nil {
			return err
		}
	}
	return nil
}
