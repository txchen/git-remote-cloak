package localstate

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/txchen/git-remote-cloak/internal/domain"
	"golang.org/x/crypto/hkdf"
)

const transactionVersion = 1

// MaintenanceOperation names an authenticated crash-journal workflow.
type MaintenanceOperation string

// CompactionOperation identifies Snapshot Rebuild publication journals.
const CompactionOperation MaintenanceOperation = "compaction"

// MigrationOperation identifies Format Migration publication journals.
const MigrationOperation MaintenanceOperation = "migration"

func (operation MaintenanceOperation) isSupported() bool {
	return operation == "" || operation == CompactionOperation || operation == MigrationOperation
}

// Transaction records only an opaque authenticated intent and public Storage
// History identities. It contains no Recovery Secret, derived key, Logical Ref
// name, or plaintext Git object identity.
type Transaction struct {
	Version                 int                  `json:"version"`
	IntentID                string               `json:"intent_id"`
	StartingStorageCommitID string               `json:"starting_storage_commit_id"`
	PreparedStorageCommitID string               `json:"prepared_storage_commit_id"`
	AuthenticationTag       string               `json:"authentication_tag"`
	Operation               MaintenanceOperation `json:"operation,omitempty"`
}

// TransactionIntentID authenticates a canonical logical transaction without
// exposing its Protected Plaintext in persistent state.
func TransactionIntentID(secret domain.RecoverySecret, repositoryID domain.RepositoryID, canonicalIntent []byte) (string, error) {
	key, err := journalKey(secret, repositoryID, "intent")
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key[:])
	mac.Write(canonicalIntent)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// LoadTransaction returns a valid journal for one opaque intent. Invalid
// journal state is isolated by removal and treated as safely rebuildable.
func LoadTransaction(gitDirectory, intentID string, secret domain.RecoverySecret, repositoryID domain.RepositoryID) (Transaction, bool) {
	if gitDirectory == "" || !validHexID(intentID) {
		return Transaction{}, false
	}
	path := transactionPath(gitDirectory, intentID)
	contents, err := os.ReadFile(path)
	if err != nil {
		return Transaction{}, false
	}
	var transaction Transaction
	if json.Unmarshal(contents, &transaction) != nil || transaction.Version != transactionVersion ||
		transaction.IntentID != intentID || !validGitObjectID(transaction.StartingStorageCommitID) ||
		!validGitObjectID(transaction.PreparedStorageCommitID) || !validHexID(transaction.AuthenticationTag) ||
		!validTransactionAuthentication(transaction, secret, repositoryID) {
		_ = os.Remove(path)
		return Transaction{}, false
	}
	return transaction, true
}

// StoreTransaction atomically persists a prepared publication journal.
func StoreTransaction(gitDirectory string, transaction Transaction, secret domain.RecoverySecret, repositoryID domain.RepositoryID) error {
	if gitDirectory == "" {
		return nil
	}
	if os.Getenv("CLOAK_TEST_FAULT") == "local-journal-write" {
		return errors.New("injected local journal write failure")
	}
	transaction.Version = transactionVersion
	if !validHexID(transaction.IntentID) || !validGitObjectID(transaction.StartingStorageCommitID) ||
		!validGitObjectID(transaction.PreparedStorageCommitID) || !transaction.Operation.isSupported() {
		return errors.New("invalid crash journal state")
	}
	authenticationTag, err := transactionAuthentication(transaction, secret, repositoryID)
	if err != nil {
		return err
	}
	transaction.AuthenticationTag = authenticationTag
	contents, err := json.Marshal(transaction)
	if err != nil {
		return err
	}
	contents = append(contents, '\n')
	return atomicWrite(transactionPath(gitDirectory, transaction.IntentID), contents)
}

// RemoveTransaction forgets a completed or safely rebuildable transaction.
func RemoveTransaction(gitDirectory, intentID string) error {
	if gitDirectory == "" || !validHexID(intentID) {
		return nil
	}
	err := os.Remove(transactionPath(gitDirectory, intentID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ReconcileTransactions removes invalid journals and publications whose
// prepared Storage commit is now authoritative.
func ReconcileTransactions(gitDirectory string, secret domain.RecoverySecret, repositoryID domain.RepositoryID, currentStorageCommitID string, storageHistoryContains func(string) bool) {
	if gitDirectory == "" {
		return
	}
	directory := filepath.Join(gitDirectory, "cloak", "transactions")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			_ = os.RemoveAll(filepath.Join(directory, entry.Name()))
			continue
		}
		intentID := strings.TrimSuffix(entry.Name(), ".json")
		transaction, valid := LoadTransaction(gitDirectory, intentID, secret, repositoryID)
		published := valid && (transaction.PreparedStorageCommitID == currentStorageCommitID || storageHistoryContains != nil && storageHistoryContains(transaction.PreparedStorageCommitID))
		if !valid || published && transaction.Operation != CompactionOperation {
			_ = os.Remove(filepath.Join(directory, entry.Name()))
		}
	}
}

// ConsumePublishedTransaction recognizes and removes one authenticated
// operation whose prepared commit is now authoritative.
func ConsumePublishedTransaction(gitDirectory string, secret domain.RecoverySecret, repositoryID domain.RepositoryID, currentStorageCommitID string, operation MaintenanceOperation, storageHistoryContains func(string) bool) bool {
	if gitDirectory == "" {
		return false
	}
	directory := filepath.Join(gitDirectory, "cloak", "transactions")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		intentID := strings.TrimSuffix(entry.Name(), ".json")
		transaction, valid := LoadTransaction(gitDirectory, intentID, secret, repositoryID)
		published := valid && (transaction.PreparedStorageCommitID == currentStorageCommitID || storageHistoryContains != nil && storageHistoryContains(transaction.PreparedStorageCommitID))
		if published && transaction.Operation == operation {
			_ = RemoveTransaction(gitDirectory, intentID)
			return true
		}
	}
	return false
}

func journalKey(secret domain.RecoverySecret, repositoryID domain.RepositoryID, purpose string) ([32]byte, error) {
	info := []byte("git-remote-cloak/v1/aes-256-gcm-siv/crash-journal-" + purpose)
	reader := hkdf.New(sha256.New, secret[:], repositoryID[:], info)
	var key [32]byte
	if _, err := io.ReadFull(reader, key[:]); err != nil {
		return [32]byte{}, fmt.Errorf("derive crash journal %s key: %w", purpose, err)
	}
	return key, nil
}

func transactionAuthentication(transaction Transaction, secret domain.RecoverySecret, repositoryID domain.RepositoryID) (string, error) {
	key, err := journalKey(secret, repositoryID, "authentication")
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key[:])
	fmt.Fprintf(mac, "%d\x00%s\x00%s\x00%s", transaction.Version, transaction.IntentID, transaction.StartingStorageCommitID, transaction.PreparedStorageCommitID)
	if transaction.Operation != "" {
		fmt.Fprintf(mac, "\x00%s", transaction.Operation)
	}
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func validTransactionAuthentication(transaction Transaction, secret domain.RecoverySecret, repositoryID domain.RepositoryID) bool {
	want, err := transactionAuthentication(transaction, secret, repositoryID)
	if err != nil {
		return false
	}
	got, err := hex.DecodeString(transaction.AuthenticationTag)
	if err != nil {
		return false
	}
	wantBytes, _ := hex.DecodeString(want)
	return hmac.Equal(got, wantBytes)
}

func transactionPath(gitDirectory, intentID string) string {
	return filepath.Join(gitDirectory, "cloak", "transactions", intentID+".json")
}

func validHexID(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func validGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}
