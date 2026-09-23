package localstate

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/txchen/git-remote-cloak/internal/domain"
)

// The child competes with a parent paused inside continuity validation. Using
// another process ensures a Go mutex alone cannot satisfy the contract.
func TestCheckpointWriterProcess(t *testing.T) {
	directory := os.Getenv("CLOAK_CHECKPOINT_TEST_DIRECTORY")
	if directory == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(directory, "writer-ready"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var err error
	if os.Getenv("CLOAK_CHECKPOINT_TEST_OPERATION") == "replace" {
		err = ReplaceCheckpoint(directory, domain.RepositoryID{2}, 1, strings.Repeat("c", 40))
	} else {
		err = ObserveCheckpoint(directory, domain.RepositoryID{1}, 3, strings.Repeat("c", 40), "", func(_, _ string) bool { return true })
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointWritersSerializeAcrossProcesses(t *testing.T) {
	for _, operation := range []string{"observe", "replace"} {
		t.Run(operation, func(t *testing.T) {
			directory := t.TempDir()
			if err := ObserveCheckpoint(directory, domain.RepositoryID{1}, 1, strings.Repeat("a", 40), "", nil); err != nil {
				t.Fatal(err)
			}
			validating, resume, olderDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(resume) }) }
			defer release()
			go func() {
				olderDone <- ObserveCheckpoint(directory, domain.RepositoryID{1}, 2, strings.Repeat("b", 40), "", func(_, _ string) bool {
					close(validating)
					<-resume
					return true
				})
			}()
			select {
			case <-validating:
			case err := <-olderDone:
				t.Fatalf("writer failed before validation: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("writer did not reach validation")
			}
			child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCheckpointWriterProcess$")
			child.Env = append(os.Environ(), "CLOAK_CHECKPOINT_TEST_DIRECTORY="+directory, "CLOAK_CHECKPOINT_TEST_OPERATION="+operation)
			var output bytes.Buffer
			child.Stdout, child.Stderr = &output, &output
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- child.Wait() }()
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(directory, "writer-ready")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("child writer did not start")
				}
				time.Sleep(10 * time.Millisecond)
			}
			finished := false
			select {
			case err := <-done:
				finished = true
				t.Errorf("competing writer finished during validation: %v\n%s", err, &output)
			case <-time.After(200 * time.Millisecond):
			}
			release()
			if err := <-olderDone; err != nil {
				t.Fatal(err)
			}
			if !finished {
				select {
				case err := <-done:
					if err != nil {
						t.Fatalf("child writer: %v\n%s", err, &output)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("checkpoint lock was not released")
				}
			}
			checkpoint, exists, err := LoadCheckpoint(directory)
			if err != nil || !exists || checkpoint.LastSeenStorageCommitID != strings.Repeat("c", 40) {
				t.Fatalf("newer checkpoint was overwritten: %+v, exists=%v, err=%v", checkpoint, exists, err)
			}
		})
	}
}

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
