package format

import (
	"bytes"
	"testing"

	"github.com/txchen/git-remote-cloak/internal/domain"
)

func TestV1ReaderAcceptsLegacyGenerationBoundPackRecords(t *testing.T) {
	registry := NewRegistry()
	secret := domain.RecoverySecret{1, 2, 3}
	repository := SnapshotState{
		RepositoryID: domain.RepositoryID{4, 5, 6},
		Generation:   2,
	}
	payloadIdentity := bytes.Repeat([]byte{7}, 16)
	plaintext := []byte("legacy Pack Payload record")
	key, err := derivePayloadKey(secret, repository.RepositoryID, packIndexEncryptionPurpose, payloadIdentity)
	if err != nil {
		t.Fatal(err)
	}
	primitive, err := registry.newAEAD(key)
	if err != nil {
		t.Fatal(err)
	}
	associatedData, err := registry.snapshotAssociatedData(repository, encryptedRecord{
		generation: repository.Generation, purpose: packIndexEncryptionPurpose,
		kind: packIndexKind, payloadIdentity: payloadIdentity, final: true,
	}, len(plaintext))
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := primitive.Encrypt(plaintext, associatedData)
	if err != nil {
		t.Fatal(err)
	}
	got, authenticationGeneration, err := registry.decryptPackIndexRecord(secret, repository, snapshotPayloadLocation{
		Identity: payloadIdentity,
	}, ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if authenticationGeneration != repository.Generation {
		t.Fatalf("legacy authentication generation = %d, want %d", authenticationGeneration, repository.Generation)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("legacy plaintext = %q, want %q", got, plaintext)
	}
}
