package localstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/txchen/git-remote-cloak/internal/domain"
)

func TestRollbackCheckpointFailsClosedForRegressionSubstitutionAndReversal(t *testing.T) {
	repositoryID := domain.RepositoryID{1, 2, 3, 4}
	firstRef := strings.Repeat("1", 40)
	secondRef := strings.Repeat("2", 40)

	for _, test := range []struct {
		name            string
		generation      uint64
		storageCommitID string
		previous        string
		continues       bool
		want            string
	}{
		{name: "generation regression", generation: 6, storageCommitID: secondRef, previous: firstRef, continues: true, want: "generation regressed"},
		{name: "same-generation substitution", generation: 7, storageCommitID: secondRef, previous: firstRef, continues: false, want: "generation was substituted"},
		{name: "unexplained history reversal", generation: 8, storageCommitID: secondRef, previous: strings.Repeat("3", 40), continues: false, want: "History reversal"},
	} {
		t.Run(test.name, func(t *testing.T) {
			gitDirectory := t.TempDir()
			if err := ObserveCheckpoint(gitDirectory, repositoryID, 7, firstRef, strings.Repeat("0", 40), nil); err != nil {
				t.Fatal(err)
			}
			err := ObserveCheckpoint(gitDirectory, repositoryID, test.generation, test.storageCommitID, test.previous,
				func(_, _ string) bool { return test.continues })
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("checkpoint error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRollbackCheckpointAcceptsExplainedStorageHistoryContinuation(t *testing.T) {
	gitDirectory := t.TempDir()
	repositoryID := domain.RepositoryID{9, 8, 7, 6}
	firstRef := strings.Repeat("a", 40)
	secondRef := strings.Repeat("b", 40)
	if err := ObserveCheckpoint(gitDirectory, repositoryID, 3, firstRef, strings.Repeat("0", 40), nil); err != nil {
		t.Fatal(err)
	}
	if err := ObserveCheckpoint(gitDirectory, repositoryID, 5, secondRef, strings.Repeat("c", 40), func(previous, current string) bool {
		return previous == firstRef && current == secondRef
	}); err != nil {
		t.Fatal(err)
	}
	checkpoint, exists, err := LoadCheckpoint(gitDirectory)
	if err != nil || !exists || checkpoint.HighestAuthenticatedGeneration != 5 || checkpoint.LastSeenStorageCommitID != secondRef {
		t.Fatalf("checkpoint = %+v exists=%v err=%v", checkpoint, exists, err)
	}
}

func TestDamagedRollbackCheckpointIsNotSilentlyDiscarded(t *testing.T) {
	gitDirectory := t.TempDir()
	path := filepath.Join(gitDirectory, "cloak", "state")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("damaged trusted state\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadCheckpoint(gitDirectory); err == nil {
		t.Fatal("damaged trusted checkpoint was treated as absent")
	}
}
