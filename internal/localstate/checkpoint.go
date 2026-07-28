package localstate

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/txchen/git-remote-cloak/internal/domain"
)

const checkpointVersion = 1

// Checkpoint is the trusted public record of the newest authenticated
// Ciphertext Snapshot observed by one Authorized Host.
type Checkpoint struct {
	Version                        int    `json:"version"`
	RepositoryID                   string `json:"repository_id"`
	HighestAuthenticatedGeneration uint64 `json:"highest_authenticated_generation"`
	LastSeenStorageCommitID        string `json:"last_seen_storage_commit_id"`
}

// LoadCheckpoint loads trusted local rollback state. Corruption is an error
// rather than a cache miss because silently discarding it would disable
// rollback protection.
func LoadCheckpoint(gitDirectory string) (Checkpoint, bool, error) {
	if gitDirectory == "" {
		return Checkpoint{}, false, nil
	}
	contents, err := os.ReadFile(checkpointPath(gitDirectory))
	if os.IsNotExist(err) {
		return Checkpoint{}, false, nil
	}
	if err != nil {
		return Checkpoint{}, false, fmt.Errorf("read trusted Rollback Checkpoint: %w", err)
	}
	var checkpoint Checkpoint
	if json.Unmarshal(contents, &checkpoint) != nil || !validCheckpoint(checkpoint) {
		return Checkpoint{}, false, errors.New("trusted Rollback Checkpoint is damaged")
	}
	return checkpoint, true, nil
}

// ObserveCheckpoint checks one authenticated snapshot against trusted state,
// then records it as the newest observation.
func ObserveCheckpoint(gitDirectory string, repositoryID domain.RepositoryID, generation uint64, storageCommitID, previousStorageCommitID string, storageHistoryContinues func(string, string) bool) error {
	if gitDirectory == "" {
		return nil
	}
	if err := CheckCheckpoint(gitDirectory, repositoryID, generation, storageCommitID, previousStorageCommitID, storageHistoryContinues); err != nil {
		return err
	}
	return StoreCheckpoint(gitDirectory, Checkpoint{
		Version: checkpointVersion, RepositoryID: hex.EncodeToString(repositoryID[:]),
		HighestAuthenticatedGeneration: generation, LastSeenStorageCommitID: storageCommitID,
	})
}

// CheckCheckpoint fails closed when an authenticated snapshot contradicts
// trusted local rollback state, without changing that state.
func CheckCheckpoint(gitDirectory string, repositoryID domain.RepositoryID, generation uint64, storageCommitID, previousStorageCommitID string, storageHistoryContinues func(string, string) bool) error {
	if gitDirectory == "" {
		return nil
	}
	checkpoint, exists, err := LoadCheckpoint(gitDirectory)
	if err != nil {
		return err
	}
	repositoryIDText := hex.EncodeToString(repositoryID[:])
	if exists {
		if checkpoint.RepositoryID != repositoryIDText {
			return errors.New("suspected rollback: Ciphertext Repository identity differs from trusted Rollback Checkpoint")
		}
		switch {
		case generation < checkpoint.HighestAuthenticatedGeneration:
			return errors.New("suspected rollback: authenticated storage generation regressed")
		case generation == checkpoint.HighestAuthenticatedGeneration && storageCommitID != checkpoint.LastSeenStorageCommitID:
			return errors.New("suspected rollback: authenticated storage generation was substituted")
		case generation > checkpoint.HighestAuthenticatedGeneration:
			continues := storageHistoryContinues != nil && storageHistoryContinues(checkpoint.LastSeenStorageCommitID, storageCommitID)
			if !continues && previousStorageCommitID != checkpoint.LastSeenStorageCommitID {
				return errors.New("suspected rollback: unexplained Storage History reversal")
			}
		}
	}
	return nil
}

// StoreCheckpoint atomically writes public trusted rollback state.
func StoreCheckpoint(gitDirectory string, checkpoint Checkpoint) error {
	if gitDirectory == "" || !validCheckpoint(checkpoint) {
		return errors.New("invalid Rollback Checkpoint")
	}
	contents, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	return atomicWrite(checkpointPath(gitDirectory), append(contents, '\n'))
}

// ReplaceCheckpoint installs trusted state for a deliberately confirmed new
// Ciphertext Repository identity, such as a successful Rekey publication.
func ReplaceCheckpoint(gitDirectory string, repositoryID domain.RepositoryID, generation uint64, storageCommitID string) error {
	return StoreCheckpoint(gitDirectory, Checkpoint{
		Version: checkpointVersion, RepositoryID: hex.EncodeToString(repositoryID[:]),
		HighestAuthenticatedGeneration: generation, LastSeenStorageCommitID: storageCommitID,
	})
}

func checkpointPath(gitDirectory string) string {
	return filepath.Join(gitDirectory, "cloak", "state")
}

func validCheckpoint(checkpoint Checkpoint) bool {
	repositoryID, err := hex.DecodeString(checkpoint.RepositoryID)
	return checkpoint.Version == checkpointVersion && len(repositoryID) == 16 && err == nil &&
		checkpoint.HighestAuthenticatedGeneration > 0 && validGitObjectID(checkpoint.LastSeenStorageCommitID)
}
