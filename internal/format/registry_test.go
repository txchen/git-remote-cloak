package format_test

import (
	"bytes"
	"encoding/hex"
	"os"
	"regexp"
	"strings"
	"testing"

	cloakformat "github.com/txchen/git-remote-cloak/internal/format"
)

var (
	testSecret = [32]byte{
		0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
		0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f,
		0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17,
		0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f,
	}
	testRepositoryID = [16]byte{
		0xf0, 0xe1, 0xd2, 0xc3, 0xb4, 0xa5, 0x96, 0x87,
		0x78, 0x69, 0x5a, 0x4b, 0x3c, 0x2d, 0x1e, 0x0f,
	}
)

func TestV1EmptySnapshotRoundTripsThroughRegistry(t *testing.T) {
	registry := cloakformat.NewRegistry()
	want := cloakformat.EmptyRepository{
		RepositoryID: testRepositoryID,
		LogicalHEAD:  "refs/heads/main",
		ObjectFormat: "sha1",
	}
	encoded, err := registry.EncodeEmpty(testSecret, want)
	if err != nil {
		t.Fatalf("encode empty snapshot: %v", err)
	}

	got, err := registry.DecodeEmpty(testSecret, encoded.Bootstrap, encoded.Manifest)
	if err != nil {
		t.Fatalf("decode empty snapshot: %v", err)
	}
	if got != want {
		t.Fatalf("decoded empty repository = %+v, want %+v", got, want)
	}
}

func TestV1PackPayloadRoundTripsWithOpaqueAuthenticatedRecords(t *testing.T) {
	registry := cloakformat.NewRegistry()
	input := cloakformat.SnapshotInput{
		Repository: cloakformat.Repository{
			RepositoryID: testRepositoryID, Generation: 2, LogicalHEAD: "refs/heads/main",
			ObjectFormat: "sha1", PreviousStorageRef: strings.Repeat("a", 40),
			LogicalRefs: map[string]string{"refs/heads/main": strings.Repeat("1", 40)},
		},
		Packs: []cloakformat.PackPayload{{
			Pack: []byte("PACK\x00binary pack payload"), ObjectIDs: []string{strings.Repeat("1", 40)},
		}},
	}
	encoded, err := registry.EncodeSnapshot(testSecret, input)
	if err != nil {
		t.Fatalf("encode snapshot: %v", err)
	}
	identifier := regexp.MustCompile(`^[a-z2-7]{52}$`)
	if len(encoded.Objects) != 3 {
		t.Fatalf("encrypted object count = %d, want manifest, index, and chunk", len(encoded.Objects))
	}
	for locator := range encoded.Objects {
		if !identifier.MatchString(locator) {
			t.Fatalf("opaque identifier %q is not lowercase unpadded SHA-256 Base32", locator)
		}
	}
	decoded, err := registry.DecodeSnapshot(testSecret, encoded.Bootstrap, encoded.Objects)
	if err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if len(decoded.Packs) != 1 || !bytes.Equal(decoded.Packs[0].Pack, input.Packs[0].Pack) {
		t.Fatal("Pack Payload did not round-trip exactly")
	}
	if decoded.Repository.LogicalRefs["refs/heads/main"] != strings.Repeat("1", 40) {
		t.Fatalf("Logical Ref did not round-trip: %+v", decoded.Repository.LogicalRefs)
	}
}

func TestV1SnapshotRejectsMissingCorruptedAndSubstitutedEncryptedObjects(t *testing.T) {
	registry := cloakformat.NewRegistry()
	input := cloakformat.SnapshotInput{
		Repository: cloakformat.Repository{
			RepositoryID: testRepositoryID, Generation: 2, LogicalHEAD: "refs/heads/main",
			ObjectFormat: "sha1", PreviousStorageRef: strings.Repeat("a", 40),
			LogicalRefs: map[string]string{"refs/heads/main": strings.Repeat("1", 40)},
		},
		Packs: []cloakformat.PackPayload{{Pack: []byte("PACK-one"), ObjectIDs: []string{strings.Repeat("1", 40)}}},
	}
	encoded, err := registry.EncodeSnapshot(testSecret, input)
	if err != nil {
		t.Fatal(err)
	}
	locators := make([]string, 0, len(encoded.Objects))
	for locator := range encoded.Objects {
		locators = append(locators, locator)
	}

	missing := cloneObjects(encoded.Objects)
	delete(missing, locators[0])
	if _, err := registry.DecodeSnapshot(testSecret, encoded.Bootstrap, missing); err == nil {
		t.Fatal("snapshot with missing encrypted object decoded successfully")
	}

	corrupted := cloneObjects(encoded.Objects)
	corrupted[locators[0]][len(corrupted[locators[0]])-1] ^= 1
	if _, err := registry.DecodeSnapshot(testSecret, encoded.Bootstrap, corrupted); err == nil {
		t.Fatal("snapshot with corrupted encrypted object decoded successfully")
	}

	substituted := cloneObjects(encoded.Objects)
	substituted[locators[0]] = bytes.Clone(encoded.Objects[locators[1]])
	if _, err := registry.DecodeSnapshot(testSecret, encoded.Bootstrap, substituted); err == nil {
		t.Fatal("snapshot with substituted encrypted object decoded successfully")
	}
}

func TestV1PackPayloadSplitsAtFixedMaximumPlaintextChunkSize(t *testing.T) {
	registry := cloakformat.NewRegistry()
	pack := bytes.Repeat([]byte{0xa5}, cloakformat.DefaultChunkSize+1)
	encoded, err := registry.EncodeSnapshot(testSecret, cloakformat.SnapshotInput{
		Repository: cloakformat.Repository{
			RepositoryID: testRepositoryID, Generation: 2, LogicalHEAD: "refs/heads/main",
			ObjectFormat: "sha1", PreviousStorageRef: strings.Repeat("a", 40),
			LogicalRefs: map[string]string{"refs/heads/main": strings.Repeat("1", 40)},
		},
		Packs: []cloakformat.PackPayload{{Pack: pack, ObjectIDs: []string{strings.Repeat("1", 40)}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded.Objects) != 4 {
		t.Fatalf("encrypted object count = %d, want manifest, index, and two chunks", len(encoded.Objects))
	}
	decoded, err := registry.DecodeSnapshot(testSecret, encoded.Bootstrap, encoded.Objects)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded.Packs[0].Pack, pack) {
		t.Fatal("multi-chunk Pack Payload did not round-trip exactly")
	}
}

func cloneObjects(objects map[string][]byte) map[string][]byte {
	cloned := make(map[string][]byte, len(objects))
	for locator, contents := range objects {
		cloned[locator] = bytes.Clone(contents)
	}
	return cloned
}

func TestV1BootstrapUsesCanonicalPreamble(t *testing.T) {
	registry := cloakformat.NewRegistry()
	encoded, err := registry.EncodeEmpty(testSecret, cloakformat.EmptyRepository{
		RepositoryID: testRepositoryID,
		LogicalHEAD:  "refs/heads/main",
		ObjectFormat: "sha1",
	})
	if err != nil {
		t.Fatalf("encode empty snapshot: %v", err)
	}

	wantPrefix := []byte{
		'C', 'L', 'O', 'A', 'K', 0, 0, 0,
		1, // bootstrap framing
		1, // format major
		0, // format minor
		0, // required feature count
	}
	if !bytes.Equal(encoded.Bootstrap[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("bootstrap preamble prefix = %x, want %x", encoded.Bootstrap[:len(wantPrefix)], wantPrefix)
	}
}

func TestRegistryFailsClosedForUnsupportedPreamble(t *testing.T) {
	registry := cloakformat.NewRegistry()
	encoded, err := registry.EncodeEmpty(testSecret, cloakformat.EmptyRepository{
		RepositoryID: testRepositoryID,
		LogicalHEAD:  "refs/heads/main",
		ObjectFormat: "sha1",
	})
	if err != nil {
		t.Fatalf("encode empty snapshot: %v", err)
	}

	tests := []struct {
		name   string
		offset int
		value  byte
	}{
		{name: "unknown framing", offset: 8, value: 2},
		{name: "unsupported major", offset: 9, value: 2},
		{name: "unknown required feature", offset: 11, value: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bootstrap := bytes.Clone(encoded.Bootstrap)
			bootstrap[test.offset] = test.value
			if _, err := registry.DecodeEmpty(testSecret, bootstrap, encoded.Manifest); err == nil {
				t.Fatal("DecodeEmpty succeeded, want fail-closed error")
			}
		})
	}
}

func TestV1SnapshotRejectsTamperingTruncationAndCrossContextSubstitution(t *testing.T) {
	registry := cloakformat.NewRegistry()
	repository := cloakformat.EmptyRepository{
		RepositoryID: testRepositoryID,
		LogicalHEAD:  "refs/heads/main",
		ObjectFormat: "sha1",
	}
	encoded, err := registry.EncodeEmpty(testSecret, repository)
	if err != nil {
		t.Fatalf("encode empty snapshot: %v", err)
	}

	tampered := bytes.Clone(encoded.Manifest)
	tampered[len(tampered)-1] ^= 1
	if _, err := registry.DecodeEmpty(testSecret, encoded.Bootstrap, tampered); err == nil {
		t.Fatal("tampered manifest decoded successfully")
	}
	if _, err := registry.DecodeEmpty(testSecret, encoded.Bootstrap, encoded.Manifest[:len(encoded.Manifest)-1]); err == nil {
		t.Fatal("truncated manifest decoded successfully")
	}

	other := repository
	other.RepositoryID[0] ^= 1
	otherEncoded, err := registry.EncodeEmpty(testSecret, other)
	if err != nil {
		t.Fatalf("encode other snapshot: %v", err)
	}
	if _, err := registry.DecodeEmpty(testSecret, encoded.Bootstrap, otherEncoded.Manifest); err == nil {
		t.Fatal("manifest from another repository context decoded successfully")
	}
}

func TestV1EncryptionUsesRandomNonceAndRawTinkFraming(t *testing.T) {
	registry := cloakformat.NewRegistry()
	repository := cloakformat.EmptyRepository{
		RepositoryID: testRepositoryID,
		LogicalHEAD:  "refs/heads/main",
		ObjectFormat: "sha1",
	}
	first, err := registry.EncodeEmpty(testSecret, repository)
	if err != nil {
		t.Fatalf("first encoding: %v", err)
	}
	second, err := registry.EncodeEmpty(testSecret, repository)
	if err != nil {
		t.Fatalf("second encoding: %v", err)
	}

	if bytes.Equal(first.Manifest[:12], second.Manifest[:12]) {
		t.Fatal("two encryptions reused the same 96-bit nonce")
	}
	if len(first.Manifest) < 28 {
		t.Fatalf("manifest length = %d, want nonce and authentication tag", len(first.Manifest))
	}
}

func TestV1CanonicalGoldenEmptySnapshotDecodes(t *testing.T) {
	bootstrap := readHexVector(t, "testdata/v1-empty-bootstrap.hex")
	manifest := readHexVector(t, "testdata/v1-empty-manifest.hex")
	want := cloakformat.EmptyRepository{
		RepositoryID: testRepositoryID,
		LogicalHEAD:  "refs/heads/main",
		ObjectFormat: "sha1",
	}

	got, err := cloakformat.NewRegistry().DecodeEmpty(testSecret, bootstrap, manifest)
	if err != nil {
		t.Fatalf("decode canonical golden vector: %v", err)
	}
	if got != want {
		t.Fatalf("golden vector = %+v, want %+v", got, want)
	}
}

func readHexVector(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := hex.DecodeString(strings.TrimSpace(string(contents)))
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}
