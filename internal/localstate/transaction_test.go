package localstate

import (
	"os"
	"strings"
	"testing"

	"github.com/txchen/git-remote-cloak/internal/domain"
)

func TestReconcileTransactionsKeepsCompactionJournalWithoutHistoryLookup(t *testing.T) {
	gitDirectory := t.TempDir()
	secret := domain.RecoverySecret{1}
	repositoryID := domain.RepositoryID{2}
	current := strings.Repeat("c", 40)
	prepared := strings.Repeat("b", 40)
	intentID := strings.Repeat("a", 64)
	if err := StoreTransaction(gitDirectory, Transaction{
		IntentID: intentID, StartingStorageCommitID: strings.Repeat("0", 40),
		PreparedStorageCommitID: prepared, Operation: CompactionOperation,
	}, secret, repositoryID); err != nil {
		t.Fatal(err)
	}
	lookups := 0
	contains := func(string) bool { lookups++; return true }
	ReconcileTransactions(gitDirectory, secret, repositoryID, current, contains)
	if lookups != 0 {
		t.Fatalf("compaction journal performed %d unnecessary history lookups", lookups)
	}
	if _, valid := LoadTransaction(gitDirectory, intentID, secret, repositoryID); !valid {
		t.Fatal("compaction journal was removed before an explicit retry")
	}
	if !ConsumePublishedTransaction(gitDirectory, secret, repositoryID, current, CompactionOperation, contains) {
		t.Fatal("compaction retry did not recognize the preserved journal")
	}
	if _, err := os.Stat(transactionPath(gitDirectory, intentID)); !os.IsNotExist(err) {
		t.Fatalf("consumed compaction journal remains: %v", err)
	}
}

func TestReconcileTransactionsRemovesPublishedOrdinaryJournal(t *testing.T) {
	gitDirectory := t.TempDir()
	secret := domain.RecoverySecret{1}
	repositoryID := domain.RepositoryID{2}
	intentID := strings.Repeat("a", 64)
	prepared := strings.Repeat("b", 40)
	if err := StoreTransaction(gitDirectory, Transaction{
		IntentID: intentID, StartingStorageCommitID: strings.Repeat("0", 40),
		PreparedStorageCommitID: prepared,
	}, secret, repositoryID); err != nil {
		t.Fatal(err)
	}
	lookups := 0
	ReconcileTransactions(gitDirectory, secret, repositoryID, strings.Repeat("c", 40), func(id string) bool {
		lookups++
		return id == prepared
	})
	if lookups != 1 {
		t.Fatalf("ordinary journal performed %d history lookups, want 1", lookups)
	}
	if _, err := os.Stat(transactionPath(gitDirectory, intentID)); !os.IsNotExist(err) {
		t.Fatalf("published ordinary journal remains: %v", err)
	}
}
