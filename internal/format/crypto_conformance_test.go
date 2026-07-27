package format

import (
	"bytes"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

func TestAES256GCMSIVDecryptsRFC8452VectorWithRawFraming(t *testing.T) {
	keyBytes := decodeHex(t, "0100000000000000000000000000000000000000000000000000000000000000")
	var key [32]byte
	copy(key[:], keyBytes)
	primitive, err := newAESGCMSIV(key)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext := decodeHex(t, "030000000000000000000000c2ef328e5c71c83b843122130f7364b761e0b97427e3df28")
	want := decodeHex(t, "0100000000000000")

	got, err := primitive.Decrypt(ciphertext, nil)
	if err != nil {
		t.Fatalf("decrypt RFC 8452 vector: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("plaintext = %x, want %x", got, want)
	}
}

func TestHKDFSeparatesEveryV1PurposeAndRepositoryContext(t *testing.T) {
	secret := [32]byte{1, 2, 3}
	repositoryID := [16]byte{4, 5, 6}
	purposes := []keyPurpose{
		headerAuthenticationPurpose,
		manifestEncryptionPurpose,
		packIndexEncryptionPurpose,
		packPayloadEncryptionPurpose,
		metadataIdentifierPurpose,
	}
	keys := make(map[[32]byte]string)
	for _, purpose := range purposes {
		key, err := deriveKey(secret, repositoryID, purpose)
		if err != nil {
			t.Fatal(err)
		}
		if previous, exists := keys[key]; exists {
			t.Fatalf("purposes %q and %q derived the same key", previous, purpose)
		}
		keys[key] = string(purpose)
	}
	otherRepositoryID := repositoryID
	otherRepositoryID[0] ^= 1
	otherKey, err := deriveKey(secret, otherRepositoryID, purposes[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := keys[otherKey]; exists {
		t.Fatal("different Repository ID derived an existing purpose key")
	}
}

func TestV1WriterMatchesCanonicalManifestAndAssociatedDataVectors(t *testing.T) {
	registry := NewRegistry()
	manifestCiphertext := readConformanceVector(t, "testdata/v1-empty-manifest.hex")
	wantManifest := readConformanceVector(t, "testdata/v1-empty-manifest-plaintext.hex")
	wantAssociatedData := readConformanceVector(t, "testdata/v1-empty-manifest-associated-data.hex")
	registry.newAEAD = func([32]byte) (aeadPrimitive, error) {
		return &conformanceAEAD{
			t:                  t,
			ciphertext:         manifestCiphertext,
			wantPlaintext:      wantManifest,
			wantAssociatedData: wantAssociatedData,
		}, nil
	}
	recoverySecret := [32]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31}
	repositoryID := [16]byte{0xf0, 0xe1, 0xd2, 0xc3, 0xb4, 0xa5, 0x96, 0x87, 0x78, 0x69, 0x5a, 0x4b, 0x3c, 0x2d, 0x1e, 0x0f}
	encoded, err := registry.EncodeEmpty(recoverySecret, EmptyRepository{RepositoryID: repositoryID, LogicalHEAD: "refs/heads/main", ObjectFormat: "sha1"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded.Manifest, manifestCiphertext) {
		t.Fatalf("canonical Encrypted Manifest = %x, want %x", encoded.Manifest, manifestCiphertext)
	}
	if want := readConformanceVector(t, "testdata/v1-empty-bootstrap.hex"); !bytes.Equal(encoded.Bootstrap, want) {
		t.Fatalf("canonical Bootstrap Header = %x, want %x", encoded.Bootstrap, want)
	}
	preamble, err := encodePreamble(123)
	if err != nil {
		t.Fatal(err)
	}
	if want := readConformanceVector(t, "testdata/v1-bootstrap-preamble.hex"); !bytes.Equal(preamble, want) {
		t.Fatalf("canonical Bootstrap Preamble = %x, want %x", preamble, want)
	}
}

type conformanceAEAD struct {
	t                  *testing.T
	ciphertext         []byte
	wantPlaintext      []byte
	wantAssociatedData []byte
}

func (primitive *conformanceAEAD) Encrypt(plaintext, associatedData []byte) ([]byte, error) {
	primitive.t.Helper()
	if !bytes.Equal(plaintext, primitive.wantPlaintext) {
		primitive.t.Fatalf("writer plaintext = %x, want %x", plaintext, primitive.wantPlaintext)
	}
	if !bytes.Equal(associatedData, primitive.wantAssociatedData) {
		primitive.t.Fatalf("writer associated data = %x, want %x", associatedData, primitive.wantAssociatedData)
	}
	return bytes.Clone(primitive.ciphertext), nil
}

func (primitive *conformanceAEAD) Decrypt([]byte, []byte) ([]byte, error) {
	primitive.t.Fatal("writer conformance primitive was asked to decrypt")
	return nil, nil
}

func TestV1AssociatedDataRejectsFinalFormatSuiteAndRecordKindSubstitution(t *testing.T) {
	registry := NewRegistry()
	recoverySecret := [32]byte{1, 2, 3}
	repositoryID := [16]byte{4, 5, 6}
	key, err := deriveKey(recoverySecret, repositoryID, manifestEncryptionPurpose)
	if err != nil {
		t.Fatal(err)
	}
	primitive, err := newAESGCMSIV(key)
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("authenticated record")
	base := recordContext{
		Protocol: "git-remote-cloak", FormatMajor: 1, FormatMinor: 0, Suite: 1,
		RepositoryID: repositoryID[:], RecordKind: manifestKind, Generation: 1,
		Final: true, PlaintextLen: uint64(len(plaintext)),
	}
	associatedData, err := registry.encode.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := primitive.Encrypt(plaintext, associatedData)
	if err != nil {
		t.Fatal(err)
	}
	variants := []recordContext{base, base, base, base}
	variants[0].Final = false
	variants[1].FormatMajor = 2
	variants[2].Suite = 2
	variants[3].RecordKind = "encrypted-pack-index"
	for _, variant := range variants {
		substituted, err := registry.encode.Marshal(variant)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := primitive.Decrypt(ciphertext, substituted); err == nil {
			t.Fatalf("record decrypted under substituted context: %+v", variant)
		}
	}
}

func decodeHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func readConformanceVector(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return decodeHex(t, strings.TrimSpace(string(contents)))
}
