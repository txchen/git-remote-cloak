package format

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/txchen/git-remote-cloak/internal/domain"
	"golang.org/x/crypto/hkdf"
)

const (
	packIndexKind = "encrypted-pack-index"
	packChunkKind = "encrypted-pack-chunk"
)

// SnapshotState is the authenticated Logical Repository metadata carried by one Ciphertext Snapshot.
type SnapshotState struct {
	RepositoryID       domain.RepositoryID
	Generation         uint64
	LogicalHEAD        domain.LogicalHEAD
	ObjectFormat       string
	LogicalRefs        map[string]string
	PreviousStorageRef string
}

// PackPayload is a self-contained native Git pack and its independently known object set.
type PackPayload struct {
	Pack      []byte
	ObjectIDs []string
	encoded   *encodedPackPayload
}

type encodedPackPayload struct {
	location snapshotPayloadLocation
	objects  map[string][]byte
}

// DecodedSnapshot is a fully authenticated logical state and its native packs.
type DecodedSnapshot struct {
	Repository SnapshotState
	Packs      []PackPayload
}

// SnapshotInput describes one complete candidate Ciphertext Snapshot.
type SnapshotInput struct {
	Repository SnapshotState
	Packs      []PackPayload
}

type snapshotBootstrapHeader struct {
	RepositoryID       []byte `cbor:"1,keyasint"`
	FormatMajor        uint64 `cbor:"2,keyasint"`
	FormatMinor        uint64 `cbor:"3,keyasint"`
	CryptographicSuite uint64 `cbor:"4,keyasint"`
	ChunkSize          uint64 `cbor:"5,keyasint"`
	Generation         uint64 `cbor:"6,keyasint"`
	ManifestLocator    string `cbor:"7,keyasint"`
	HeaderMAC          []byte `cbor:"8,keyasint"`
	PreviousStorageRef string `cbor:"9,keyasint,omitempty"`
}

type snapshotUnsignedHeader struct {
	RepositoryID       []byte `cbor:"1,keyasint"`
	FormatMajor        uint64 `cbor:"2,keyasint"`
	FormatMinor        uint64 `cbor:"3,keyasint"`
	CryptographicSuite uint64 `cbor:"4,keyasint"`
	ChunkSize          uint64 `cbor:"5,keyasint"`
	Generation         uint64 `cbor:"6,keyasint"`
	ManifestLocator    string `cbor:"7,keyasint"`
	PreviousStorageRef string `cbor:"9,keyasint,omitempty"`
}

type snapshotManifest struct {
	Generation       uint64                    `cbor:"1,keyasint"`
	ObjectFormat     string                    `cbor:"2,keyasint"`
	LogicalHEAD      string                    `cbor:"3,keyasint"`
	LogicalRefs      map[string][]byte         `cbor:"4,keyasint"`
	PackPayloads     []snapshotPayloadLocation `cbor:"5,keyasint"`
	RequiredFeatures []uint64                  `cbor:"6,keyasint"`
}

type snapshotPayloadLocation struct {
	Identity                 []byte `cbor:"1,keyasint"`
	IndexLocator             string `cbor:"2,keyasint"`
	AuthenticationGeneration uint64 `cbor:"3,keyasint,omitempty"`
}

type snapshotPackIndex struct {
	PayloadIdentity []byte   `cbor:"1,keyasint"`
	ObjectIDs       [][]byte `cbor:"2,keyasint"`
	ChunkLocators   []string `cbor:"3,keyasint"`
	PackSHA256      []byte   `cbor:"4,keyasint"`
	PackLength      uint64   `cbor:"5,keyasint"`
}

type snapshotRecordContext struct {
	Protocol        string `cbor:"1,keyasint"`
	FormatMajor     uint64 `cbor:"2,keyasint"`
	FormatMinor     uint64 `cbor:"3,keyasint"`
	Suite           uint64 `cbor:"4,keyasint"`
	RepositoryID    []byte `cbor:"5,keyasint"`
	RecordKind      string `cbor:"6,keyasint"`
	Generation      uint64 `cbor:"7,keyasint"`
	Final           bool   `cbor:"8,keyasint"`
	PlaintextLen    uint64 `cbor:"9,keyasint"`
	PayloadIdentity []byte `cbor:"10,keyasint,omitempty"`
	ChunkIndex      uint64 `cbor:"11,keyasint,omitempty"`
}

type encryptedRecord struct {
	generation      uint64
	purpose         keyPurpose
	kind            string
	payloadIdentity []byte
	chunkIndex      uint64
	final           bool
}

// EncodeSnapshot maps one complete non-empty Logical Repository into a Ciphertext Snapshot.
func (r *Registry) EncodeSnapshot(secret domain.RecoverySecret, input SnapshotInput) (EncodedSnapshot, error) {
	repository := input.Repository
	if err := validateSnapshotRepository(repository); err != nil {
		return EncodedSnapshot{}, err
	}
	if len(repository.LogicalRefs) > 0 && len(input.Packs) == 0 {
		return EncodedSnapshot{}, errors.New("non-empty snapshot requires a Pack Payload")
	}
	if len(repository.LogicalRefs) == 0 && len(input.Packs) > 0 {
		return EncodedSnapshot{}, errors.New("empty snapshot must not retain a Pack Payload")
	}
	objects := make(map[string][]byte)
	payloadLocations := make([]snapshotPayloadLocation, 0, len(input.Packs))
	for _, payload := range input.Packs {
		location, encryptedObjects, err := r.encodePackPayload(secret, repository, payload)
		if err != nil {
			return EncodedSnapshot{}, err
		}
		payloadLocations = append(payloadLocations, location)
		for locator, ciphertext := range encryptedObjects {
			objects[locator] = ciphertext
		}
	}
	logicalRefs := make(map[string][]byte, len(repository.LogicalRefs))
	for name, objectID := range repository.LogicalRefs {
		decoded, err := hex.DecodeString(objectID)
		if err != nil {
			return EncodedSnapshot{}, fmt.Errorf("Logical Ref %s has an invalid object ID", name)
		}
		logicalRefs[name] = decoded
	}
	manifestPlaintext, err := r.encode.Marshal(snapshotManifest{
		Generation: repository.Generation, ObjectFormat: repository.ObjectFormat,
		LogicalHEAD: string(repository.LogicalHEAD), LogicalRefs: logicalRefs,
		PackPayloads: payloadLocations, RequiredFeatures: []uint64{},
	})
	if err != nil {
		return EncodedSnapshot{}, fmt.Errorf("encode Encrypted Manifest: %w", err)
	}
	manifest, err := r.encryptRecord(secret, repository, encryptedRecord{
		generation: repository.Generation, purpose: manifestEncryptionPurpose, kind: manifestKind, final: true,
	}, manifestPlaintext)
	if err != nil {
		return EncodedSnapshot{}, err
	}
	manifestLocator := opaqueContentIdentifier(manifest)
	objects[manifestLocator] = manifest
	bootstrap, err := r.encodeSnapshotBootstrap(secret, repository, manifestLocator)
	if err != nil {
		return EncodedSnapshot{}, err
	}
	return EncodedSnapshot{Bootstrap: bootstrap, Manifest: manifest, ManifestLocator: manifestLocator, CiphertextObjects: objects}, nil
}

func (r *Registry) encodePackPayload(secret domain.RecoverySecret, repository SnapshotState, payload PackPayload) (snapshotPayloadLocation, map[string][]byte, error) {
	if payload.encoded != nil {
		objects := make(map[string][]byte, len(payload.encoded.objects))
		for locator, ciphertext := range payload.encoded.objects {
			objects[locator] = bytes.Clone(ciphertext)
		}
		return payload.encoded.location, objects, nil
	}
	if len(payload.Pack) == 0 || len(payload.ObjectIDs) == 0 {
		return snapshotPayloadLocation{}, nil, errors.New("Pack Payload and object index must not be empty")
	}
	identity := make([]byte, 16)
	if _, err := rand.Read(identity); err != nil {
		return snapshotPayloadLocation{}, nil, fmt.Errorf("generate Pack Payload identity: %w", err)
	}
	objects := make(map[string][]byte)
	chunkLocators := make([]string, 0, (len(payload.Pack)+DefaultChunkSize-1)/DefaultChunkSize)
	for offset, index := 0, uint64(0); offset < len(payload.Pack); index++ {
		end := offset + DefaultChunkSize
		if end > len(payload.Pack) {
			end = len(payload.Pack)
		}
		final := end == len(payload.Pack)
		chunk, err := r.encryptRecord(secret, repository, encryptedRecord{
			purpose: packPayloadEncryptionPurpose, kind: packChunkKind,
			payloadIdentity: identity, chunkIndex: index, final: final,
		}, payload.Pack[offset:end])
		if err != nil {
			return snapshotPayloadLocation{}, nil, err
		}
		locator := opaqueContentIdentifier(chunk)
		objects[locator] = chunk
		chunkLocators = append(chunkLocators, locator)
		offset = end
	}
	objectIDs := make([][]byte, 0, len(payload.ObjectIDs))
	for _, objectID := range payload.ObjectIDs {
		decoded, err := hex.DecodeString(objectID)
		if err != nil {
			return snapshotPayloadLocation{}, nil, fmt.Errorf("Pack Payload index contains an invalid object ID")
		}
		objectIDs = append(objectIDs, decoded)
	}
	sort.Slice(objectIDs, func(i, j int) bool { return bytes.Compare(objectIDs[i], objectIDs[j]) < 0 })
	digest := sha256.Sum256(payload.Pack)
	indexPlaintext, err := r.encode.Marshal(snapshotPackIndex{
		PayloadIdentity: identity, ObjectIDs: objectIDs, ChunkLocators: chunkLocators,
		PackSHA256: digest[:], PackLength: uint64(len(payload.Pack)),
	})
	if err != nil {
		return snapshotPayloadLocation{}, nil, fmt.Errorf("encode Encrypted Pack Index: %w", err)
	}
	index, err := r.encryptRecord(secret, repository, encryptedRecord{
		purpose: packIndexEncryptionPurpose, kind: packIndexKind, payloadIdentity: identity, final: true,
	}, indexPlaintext)
	if err != nil {
		return snapshotPayloadLocation{}, nil, err
	}
	indexLocator := opaqueContentIdentifier(index)
	objects[indexLocator] = index
	return snapshotPayloadLocation{Identity: identity, IndexLocator: indexLocator}, objects, nil
}

func (r *Registry) encodeSnapshotBootstrap(secret domain.RecoverySecret, repository SnapshotState, manifestLocator string) ([]byte, error) {
	unsigned := snapshotUnsignedHeader{
		RepositoryID: repository.RepositoryID[:], FormatMajor: FormatMajor, FormatMinor: FormatMinor,
		CryptographicSuite: CryptographicSuite, ChunkSize: DefaultChunkSize, Generation: repository.Generation,
		ManifestLocator: manifestLocator, PreviousStorageRef: repository.PreviousStorageRef,
	}
	unsignedBytes, err := r.encode.Marshal(unsigned)
	if err != nil {
		return nil, err
	}
	header := snapshotBootstrapHeader{
		RepositoryID: unsigned.RepositoryID, FormatMajor: unsigned.FormatMajor, FormatMinor: unsigned.FormatMinor,
		CryptographicSuite: unsigned.CryptographicSuite, ChunkSize: unsigned.ChunkSize, Generation: unsigned.Generation,
		ManifestLocator: unsigned.ManifestLocator, HeaderMAC: make([]byte, sha256.Size), PreviousStorageRef: unsigned.PreviousStorageRef,
	}
	headerBytes, err := r.encode.Marshal(header)
	if err != nil {
		return nil, err
	}
	preamble, err := encodePreamble(len(headerBytes))
	if err != nil {
		return nil, err
	}
	key, err := deriveKey(secret, repository.RepositoryID, headerAuthenticationPurpose)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, key[:])
	mac.Write(preamble)
	mac.Write(unsignedBytes)
	header.HeaderMAC = mac.Sum(nil)
	headerBytes, err = r.encode.Marshal(header)
	if err != nil {
		return nil, err
	}
	return append(bytes.Clone(preamble), headerBytes...), nil
}

// DecodeSnapshot authenticates all metadata and reconstructs every native Pack Payload.
func (r *Registry) DecodeSnapshot(secret domain.RecoverySecret, bootstrap []byte, objects map[string][]byte) (DecodedSnapshot, error) {
	return r.DecodeSnapshotFrom(secret, bootstrap, func(locator string) ([]byte, error) {
		object, exists := objects[locator]
		if !exists {
			return nil, errors.New("encrypted object is missing")
		}
		return object, nil
	})
}

// DecodeSnapshotFrom authenticates the Bootstrap Header before resolving referenced ciphertext objects.
func (r *Registry) DecodeSnapshotFrom(secret domain.RecoverySecret, bootstrap []byte, resolve func(string) ([]byte, error)) (DecodedSnapshot, error) {
	headerBytes, err := probePreamble(bootstrap)
	if err != nil {
		return DecodedSnapshot{}, err
	}
	var header snapshotBootstrapHeader
	if err := r.decode.Unmarshal(headerBytes, &header); err != nil {
		return DecodedSnapshot{}, fmt.Errorf("decode Bootstrap Header: %w", err)
	}
	canonical, err := r.encode.Marshal(header)
	if err != nil || !bytes.Equal(canonical, headerBytes) {
		return DecodedSnapshot{}, errors.New("Bootstrap Header is not canonical CBOR")
	}
	if len(header.RepositoryID) != 16 || header.Generation < 1 || header.FormatMajor != FormatMajor || header.FormatMinor != FormatMinor || header.CryptographicSuite != CryptographicSuite || header.ChunkSize != DefaultChunkSize {
		return DecodedSnapshot{}, errors.New("Bootstrap Header disagrees with supported v1 format")
	}
	var repositoryID domain.RepositoryID
	copy(repositoryID[:], header.RepositoryID)
	unsigned, err := r.encode.Marshal(snapshotUnsignedHeader{
		RepositoryID: header.RepositoryID, FormatMajor: header.FormatMajor, FormatMinor: header.FormatMinor,
		CryptographicSuite: header.CryptographicSuite, ChunkSize: header.ChunkSize, Generation: header.Generation,
		ManifestLocator: header.ManifestLocator, PreviousStorageRef: header.PreviousStorageRef,
	})
	if err != nil {
		return DecodedSnapshot{}, err
	}
	key, err := deriveKey(secret, repositoryID, headerAuthenticationPurpose)
	if err != nil {
		return DecodedSnapshot{}, err
	}
	mac := hmac.New(sha256.New, key[:])
	mac.Write(bootstrap[:preambleSize])
	mac.Write(unsigned)
	if !hmac.Equal(header.HeaderMAC, mac.Sum(nil)) {
		return DecodedSnapshot{}, errors.New("Bootstrap Header authentication failed")
	}
	manifestCiphertext, err := resolve(header.ManifestLocator)
	if err != nil || opaqueContentIdentifier(manifestCiphertext) != header.ManifestLocator {
		return DecodedSnapshot{}, errors.New("Encrypted Manifest is missing or has the wrong locator")
	}
	if len(manifestCiphertext) < 28 || len(manifestCiphertext)-28 > maximumManifestSize {
		return DecodedSnapshot{}, errors.New("Encrypted Manifest exceeds the v1 size limit")
	}
	repository := SnapshotState{RepositoryID: repositoryID, Generation: header.Generation, PreviousStorageRef: header.PreviousStorageRef}
	manifestPlaintext, err := r.decryptRecord(secret, repository, encryptedRecord{
		generation: repository.Generation, purpose: manifestEncryptionPurpose, kind: manifestKind, final: true,
	}, manifestCiphertext)
	if err != nil {
		return DecodedSnapshot{}, errors.New("Encrypted Manifest authentication failed")
	}
	var manifest snapshotManifest
	if err := r.decodeCanonical(manifestPlaintext, &manifest, "Encrypted Manifest"); err != nil {
		return DecodedSnapshot{}, err
	}
	if manifest.Generation != repository.Generation || len(manifest.RequiredFeatures) != 0 {
		return DecodedSnapshot{}, errors.New("Encrypted Manifest disagrees with Bootstrap Header")
	}
	repository.LogicalHEAD = domain.LogicalHEAD(manifest.LogicalHEAD)
	repository.ObjectFormat = manifest.ObjectFormat
	repository.LogicalRefs = make(map[string]string, len(manifest.LogicalRefs))
	for name, objectID := range manifest.LogicalRefs {
		repository.LogicalRefs[name] = hex.EncodeToString(objectID)
	}
	if repository.Generation == 1 {
		if len(repository.LogicalRefs) != 0 || len(manifest.PackPayloads) != 0 || repository.RepositoryID == ([16]byte{}) ||
			!strings.HasPrefix(string(repository.LogicalHEAD), "refs/heads/") || repository.ObjectFormat != "sha1" && repository.ObjectFormat != "sha256" {
			return DecodedSnapshot{}, errors.New("invalid empty Ciphertext Snapshot")
		}
		return DecodedSnapshot{Repository: repository, Packs: []PackPayload{}}, nil
	}
	if err := validateSnapshotRepository(repository); err != nil {
		return DecodedSnapshot{}, err
	}
	decoded := DecodedSnapshot{Repository: repository, Packs: make([]PackPayload, 0, len(manifest.PackPayloads))}
	for _, location := range manifest.PackPayloads {
		payload, err := r.decodePackPayload(secret, repository, location, resolve)
		if err != nil {
			return DecodedSnapshot{}, err
		}
		decoded.Packs = append(decoded.Packs, payload)
	}
	if len(repository.LogicalRefs) > 0 && len(decoded.Packs) == 0 {
		return DecodedSnapshot{}, errors.New("non-empty Logical Repository has no Pack Payload")
	}
	return decoded, nil
}

func (r *Registry) decodePackPayload(secret domain.RecoverySecret, repository SnapshotState, location snapshotPayloadLocation, resolve func(string) ([]byte, error)) (PackPayload, error) {
	if len(location.Identity) != 16 || location.IndexLocator == "" || location.AuthenticationGeneration > repository.Generation {
		return PackPayload{}, errors.New("malformed Encrypted Pack Index location")
	}
	indexCiphertext, err := resolve(location.IndexLocator)
	if err != nil || opaqueContentIdentifier(indexCiphertext) != location.IndexLocator {
		return PackPayload{}, errors.New("Encrypted Pack Index is missing or has the wrong locator")
	}
	if len(indexCiphertext) < 28 || len(indexCiphertext)-28 > maximumManifestSize {
		return PackPayload{}, errors.New("Encrypted Pack Index exceeds the v1 size limit")
	}
	indexPlaintext, authenticationGeneration, err := r.decryptPackIndexRecord(secret, repository, location, indexCiphertext)
	if err != nil {
		return PackPayload{}, errors.New("Encrypted Pack Index authentication failed")
	}
	var index snapshotPackIndex
	if err := r.decodeCanonical(indexPlaintext, &index, "Encrypted Pack Index"); err != nil {
		return PackPayload{}, err
	}
	if !bytes.Equal(index.PayloadIdentity, location.Identity) || len(index.ObjectIDs) == 0 || len(index.ChunkLocators) == 0 || len(index.PackSHA256) != sha256.Size || index.PackLength == 0 {
		return PackPayload{}, errors.New("malformed Encrypted Pack Index")
	}
	var pack bytes.Buffer
	encodedObjects := map[string][]byte{location.IndexLocator: bytes.Clone(indexCiphertext)}
	for chunkIndex, locator := range index.ChunkLocators {
		ciphertext, err := resolve(locator)
		if err != nil || opaqueContentIdentifier(ciphertext) != locator {
			return PackPayload{}, errors.New("Encrypted Pack Chunk is missing or has the wrong locator")
		}
		if len(ciphertext) < 28 || len(ciphertext)-28 > DefaultChunkSize {
			return PackPayload{}, errors.New("Encrypted Pack Chunk exceeds the fixed plaintext size")
		}
		encodedObjects[locator] = bytes.Clone(ciphertext)
		final := chunkIndex == len(index.ChunkLocators)-1
		plaintext, err := r.decryptRecord(secret, repository, encryptedRecord{
			generation: authenticationGeneration, purpose: packPayloadEncryptionPurpose, kind: packChunkKind,
			payloadIdentity: location.Identity, chunkIndex: uint64(chunkIndex), final: final,
		}, ciphertext)
		if err != nil {
			return PackPayload{}, errors.New("Encrypted Pack Chunk authentication failed")
		}
		if len(plaintext) > DefaultChunkSize || (!final && len(plaintext) != DefaultChunkSize) {
			return PackPayload{}, errors.New("Encrypted Pack Chunk violates the fixed plaintext size")
		}
		pack.Write(plaintext)
	}
	if uint64(pack.Len()) != index.PackLength {
		return PackPayload{}, errors.New("Pack Payload length disagrees with Encrypted Pack Index")
	}
	digest := sha256.Sum256(pack.Bytes())
	if !hmac.Equal(digest[:], index.PackSHA256) {
		return PackPayload{}, errors.New("Pack Payload checksum disagrees with Encrypted Pack Index")
	}
	objectIDs := make([]string, len(index.ObjectIDs))
	objectIDLength := 20
	if repository.ObjectFormat == "sha256" {
		objectIDLength = 32
	}
	for i, objectID := range index.ObjectIDs {
		if len(objectID) != objectIDLength {
			return PackPayload{}, errors.New("Encrypted Pack Index contains an invalid object ID")
		}
		objectIDs[i] = hex.EncodeToString(objectID)
	}
	location.AuthenticationGeneration = authenticationGeneration
	return PackPayload{
		Pack: pack.Bytes(), ObjectIDs: objectIDs,
		encoded: &encodedPackPayload{location: location, objects: encodedObjects},
	}, nil
}

func (r *Registry) encryptRecord(secret domain.RecoverySecret, repository SnapshotState, record encryptedRecord, plaintext []byte) ([]byte, error) {
	key, err := derivePayloadKey(secret, repository.RepositoryID, record.purpose, record.payloadIdentity)
	if err != nil {
		return nil, err
	}
	primitive, err := r.newAEAD(key)
	if err != nil {
		return nil, err
	}
	associatedData, err := r.snapshotAssociatedData(repository, record, len(plaintext))
	if err != nil {
		return nil, err
	}
	ciphertext, err := primitive.Encrypt(plaintext, associatedData)
	if err != nil {
		return nil, fmt.Errorf("encrypt %s: %w", record.kind, err)
	}
	return ciphertext, nil
}

func (r *Registry) decryptPackIndexRecord(secret domain.RecoverySecret, repository SnapshotState, location snapshotPayloadLocation, ciphertext []byte) ([]byte, uint64, error) {
	if location.AuthenticationGeneration != 0 {
		plaintext, err := r.decryptRecord(secret, repository, encryptedRecord{
			generation: location.AuthenticationGeneration, purpose: packIndexEncryptionPurpose,
			kind: packIndexKind, payloadIdentity: location.Identity, final: true,
		}, ciphertext)
		return plaintext, location.AuthenticationGeneration, err
	}
	plaintext, err := r.decryptRecord(secret, repository, encryptedRecord{
		purpose: packIndexEncryptionPurpose, kind: packIndexKind, payloadIdentity: location.Identity, final: true,
	}, ciphertext)
	if err == nil {
		return plaintext, 0, nil
	}
	plaintext, legacyErr := r.decryptRecord(secret, repository, encryptedRecord{
		generation: repository.Generation, purpose: packIndexEncryptionPurpose,
		kind: packIndexKind, payloadIdentity: location.Identity, final: true,
	}, ciphertext)
	if legacyErr == nil {
		return plaintext, repository.Generation, nil
	}
	return nil, 0, err
}

func (r *Registry) decryptRecord(secret domain.RecoverySecret, repository SnapshotState, record encryptedRecord, ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < 28 {
		return nil, errors.New("encrypted record is truncated")
	}
	plaintextLength := len(ciphertext) - 28
	key, err := derivePayloadKey(secret, repository.RepositoryID, record.purpose, record.payloadIdentity)
	if err != nil {
		return nil, err
	}
	primitive, err := r.newAEAD(key)
	if err != nil {
		return nil, err
	}
	associatedData, err := r.snapshotAssociatedData(repository, record, plaintextLength)
	if err != nil {
		return nil, err
	}
	return primitive.Decrypt(ciphertext, associatedData)
}

func (r *Registry) snapshotAssociatedData(repository SnapshotState, record encryptedRecord, plaintextLength int) ([]byte, error) {
	return r.encode.Marshal(snapshotRecordContext{
		Protocol: "git-remote-cloak", FormatMajor: FormatMajor, FormatMinor: FormatMinor, Suite: CryptographicSuite,
		RepositoryID: repository.RepositoryID[:], RecordKind: record.kind, Generation: record.generation,
		Final: record.final, PlaintextLen: uint64(plaintextLength), PayloadIdentity: record.payloadIdentity, ChunkIndex: record.chunkIndex,
	})
}

func derivePayloadKey(secret domain.RecoverySecret, repositoryID domain.RepositoryID, purpose keyPurpose, payloadIdentity []byte) ([32]byte, error) {
	if len(payloadIdentity) == 0 {
		return deriveKey(secret, repositoryID, purpose)
	}
	info := []byte("git-remote-cloak/v1/aes-256-gcm-siv/" + string(purpose) + "/" + hex.EncodeToString(payloadIdentity))
	reader := hkdf.New(sha256.New, secret[:], repositoryID[:], info)
	var key [32]byte
	if _, err := io.ReadFull(reader, key[:]); err != nil {
		return [32]byte{}, err
	}
	return key, nil
}

func (r *Registry) decodeCanonical(data []byte, target any, name string) error {
	if err := r.decode.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode %s: %w", name, err)
	}
	canonical, err := r.encode.Marshal(target)
	if err != nil || !bytes.Equal(canonical, data) {
		return fmt.Errorf("%s is not canonical CBOR", name)
	}
	return nil
}

func validateSnapshotRepository(repository SnapshotState) error {
	if repository.RepositoryID == ([16]byte{}) || repository.Generation < 2 {
		return errors.New("invalid non-empty Ciphertext Snapshot identity or generation")
	}
	if repository.LogicalHEAD == "" || !strings.HasPrefix(string(repository.LogicalHEAD), "refs/heads/") || repository.ObjectFormat != "sha1" && repository.ObjectFormat != "sha256" {
		return errors.New("invalid Logical Repository metadata")
	}
	if len(repository.LogicalRefs) == 0 {
		return nil
	}
	objectIDBytes := 20
	if repository.ObjectFormat == "sha256" {
		objectIDBytes = 32
	}
	for name, objectID := range repository.LogicalRefs {
		if !domain.LogicalRefName(name).IsSupported() || len(name) > 1024 || len(objectID) != objectIDBytes*2 {
			return errors.New("invalid Logical Ref metadata")
		}
	}
	return nil
}
