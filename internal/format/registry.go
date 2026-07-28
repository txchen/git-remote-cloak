// Package format selects and applies an exact Ciphertext Repository format.
package format

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/fxamacker/cbor/v2"
	"github.com/tink-crypto/tink-go/v2/aead"
	_ "github.com/tink-crypto/tink-go/v2/aead/aesgcmsiv"
	"github.com/tink-crypto/tink-go/v2/insecurecleartextkeyset"
	aesgcmsivpb "github.com/tink-crypto/tink-go/v2/proto/aes_gcm_siv_go_proto"
	tinkpb "github.com/tink-crypto/tink-go/v2/proto/tink_go_proto"
	"github.com/txchen/git-remote-cloak/internal/domain"
	"golang.org/x/crypto/hkdf"
	"google.golang.org/protobuf/proto"
)

const (
	FormatMajor         = 1
	FormatMinor         = 0
	BootstrapFraming    = 1
	CryptographicSuite  = 1
	DefaultChunkSize    = 32 * 1024 * 1024
	preambleSize        = 16
	maximumHeaderSize   = 64 * 1024
	maximumManifestSize = 1024 * 1024
	manifestKind        = "encrypted-manifest"
)

const cryptographicSuiteName = "aes-256-gcm-siv"

type keyPurpose string

const (
	headerAuthenticationPurpose  keyPurpose = "bootstrap-header-authentication"
	manifestEncryptionPurpose    keyPurpose = "manifest-encryption"
	packIndexEncryptionPurpose   keyPurpose = "pack-index-encryption"
	packPayloadEncryptionPurpose keyPurpose = "pack-payload-encryption"
	metadataIdentifierPurpose    keyPurpose = "stable-metadata-identifier"
)

var magic = [8]byte{'C', 'L', 'O', 'A', 'K', 0, 0, 0}

// EmptyRepository is the Logical Repository state carried by an empty Ciphertext Snapshot.
type EmptyRepository struct {
	RepositoryID domain.RepositoryID
	LogicalHEAD  domain.LogicalHEAD
	ObjectFormat string
}

// EncodedSnapshot contains a Bootstrap Header and Encrypted Manifest.
type EncodedSnapshot struct {
	Bootstrap           []byte
	Manifest            []byte
	ManifestLocator     string
	CiphertextObjects   map[string][]byte
	PackCiphertextSizes []uint64
}

// Capability reports one exact Ciphertext Repository reader and writer.
type Capability struct {
	Major              uint64   `json:"major"`
	Minor              uint64   `json:"minor"`
	Read               bool     `json:"read"`
	Write              bool     `json:"write"`
	CryptographicSuite string   `json:"cryptographic_suite"`
	RequiredFeatures   []string `json:"required_features"`
}

// Registry selects supported readers and writers from a bounded preamble.
type Registry struct {
	encode  cbor.EncMode
	decode  cbor.DecMode
	newAEAD func([32]byte) (aeadPrimitive, error)
}

type aeadPrimitive interface {
	Encrypt([]byte, []byte) ([]byte, error)
	Decrypt([]byte, []byte) ([]byte, error)
}

type bootstrapHeader struct {
	RepositoryID       []byte `cbor:"1,keyasint"`
	FormatMajor        uint64 `cbor:"2,keyasint"`
	FormatMinor        uint64 `cbor:"3,keyasint"`
	CryptographicSuite uint64 `cbor:"4,keyasint"`
	ChunkSize          uint64 `cbor:"5,keyasint"`
	Generation         uint64 `cbor:"6,keyasint"`
	ManifestLocator    string `cbor:"7,keyasint"`
	HeaderMAC          []byte `cbor:"8,keyasint"`
}

type unsignedBootstrapHeader struct {
	RepositoryID       []byte `cbor:"1,keyasint"`
	FormatMajor        uint64 `cbor:"2,keyasint"`
	FormatMinor        uint64 `cbor:"3,keyasint"`
	CryptographicSuite uint64 `cbor:"4,keyasint"`
	ChunkSize          uint64 `cbor:"5,keyasint"`
	Generation         uint64 `cbor:"6,keyasint"`
	ManifestLocator    string `cbor:"7,keyasint"`
}

type emptyManifest struct {
	Generation       uint64            `cbor:"1,keyasint"`
	ObjectFormat     string            `cbor:"2,keyasint"`
	LogicalHEAD      string            `cbor:"3,keyasint"`
	LogicalRefs      map[string][]byte `cbor:"4,keyasint"`
	PackPayloads     []string          `cbor:"5,keyasint"`
	RequiredFeatures []uint64          `cbor:"6,keyasint"`
}

type recordContext struct {
	Protocol     string `cbor:"1,keyasint"`
	FormatMajor  uint64 `cbor:"2,keyasint"`
	FormatMinor  uint64 `cbor:"3,keyasint"`
	Suite        uint64 `cbor:"4,keyasint"`
	RepositoryID []byte `cbor:"5,keyasint"`
	RecordKind   string `cbor:"6,keyasint"`
	Generation   uint64 `cbor:"7,keyasint"`
	Final        bool   `cbor:"8,keyasint"`
	PlaintextLen uint64 `cbor:"9,keyasint"`
}

// NewRegistry returns the exact v1 reader and writer registry.
func NewRegistry() *Registry {
	encode, err := cbor.CanonicalEncOptions().EncMode()
	if err != nil {
		panic(err)
	}
	decode, err := (cbor.DecOptions{
		DupMapKey:        cbor.DupMapKeyEnforcedAPF,
		IndefLength:      cbor.IndefLengthForbidden,
		MaxArrayElements: 1024,
		MaxMapPairs:      1024,
		MaxNestedLevels:  16,
		TagsMd:           cbor.TagsForbidden,
	}).DecMode()
	if err != nil {
		panic(err)
	}
	return &Registry{encode: encode, decode: decode, newAEAD: newAESGCMSIV}
}

// Capabilities returns the exact formats registered for reading and writing.
func (r *Registry) Capabilities() []Capability {
	return []Capability{{
		Major:              FormatMajor,
		Minor:              FormatMinor,
		Read:               true,
		Write:              true,
		CryptographicSuite: cryptographicSuiteName,
		RequiredFeatures:   []string{},
	}}
}

// EncodeEmpty creates a complete authenticated v1 snapshot with no Logical Refs.
func (r *Registry) EncodeEmpty(secret domain.RecoverySecret, repository EmptyRepository) (EncodedSnapshot, error) {
	if err := validateEmptyRepository(repository); err != nil {
		return EncodedSnapshot{}, err
	}
	manifestPlaintext, err := r.encode.Marshal(emptyManifest{
		Generation:       1,
		ObjectFormat:     repository.ObjectFormat,
		LogicalHEAD:      string(repository.LogicalHEAD),
		LogicalRefs:      map[string][]byte{},
		PackPayloads:     []string{},
		RequiredFeatures: []uint64{},
	})
	if err != nil {
		return EncodedSnapshot{}, fmt.Errorf("encode empty manifest: %w", err)
	}
	manifestKey, err := deriveKey(secret, repository.RepositoryID, manifestEncryptionPurpose)
	if err != nil {
		return EncodedSnapshot{}, err
	}
	primitive, err := r.newAEAD(manifestKey)
	if err != nil {
		return EncodedSnapshot{}, err
	}
	associatedData, err := r.manifestAssociatedData(repository.RepositoryID, len(manifestPlaintext))
	if err != nil {
		return EncodedSnapshot{}, err
	}
	manifest, err := primitive.Encrypt(manifestPlaintext, associatedData)
	if err != nil {
		return EncodedSnapshot{}, fmt.Errorf("encrypt empty manifest: %w", err)
	}
	manifestLocator := opaqueContentIdentifier(manifest)
	unsigned := unsignedBootstrapHeader{
		RepositoryID:       repository.RepositoryID[:],
		FormatMajor:        FormatMajor,
		FormatMinor:        FormatMinor,
		CryptographicSuite: CryptographicSuite,
		ChunkSize:          DefaultChunkSize,
		Generation:         1,
		ManifestLocator:    manifestLocator,
	}
	unsignedBytes, err := r.encode.Marshal(unsigned)
	if err != nil {
		return EncodedSnapshot{}, fmt.Errorf("encode bootstrap header: %w", err)
	}
	header := bootstrapHeader{
		RepositoryID:       unsigned.RepositoryID,
		FormatMajor:        unsigned.FormatMajor,
		FormatMinor:        unsigned.FormatMinor,
		CryptographicSuite: unsigned.CryptographicSuite,
		ChunkSize:          unsigned.ChunkSize,
		Generation:         unsigned.Generation,
		ManifestLocator:    unsigned.ManifestLocator,
		HeaderMAC:          make([]byte, sha256.Size),
	}
	headerBytes, err := r.encode.Marshal(header)
	if err != nil {
		return EncodedSnapshot{}, fmt.Errorf("size bootstrap header: %w", err)
	}
	preamble, err := encodePreamble(len(headerBytes))
	if err != nil {
		return EncodedSnapshot{}, err
	}
	headerKey, err := deriveKey(secret, repository.RepositoryID, headerAuthenticationPurpose)
	if err != nil {
		return EncodedSnapshot{}, err
	}
	authenticator := hmac.New(sha256.New, headerKey[:])
	authenticator.Write(preamble)
	authenticator.Write(unsignedBytes)
	header.HeaderMAC = authenticator.Sum(nil)
	headerBytes, err = r.encode.Marshal(header)
	if err != nil {
		return EncodedSnapshot{}, fmt.Errorf("encode authenticated bootstrap header: %w", err)
	}
	bootstrap := append(bytes.Clone(preamble), headerBytes...)
	return EncodedSnapshot{Bootstrap: bootstrap, Manifest: manifest, ManifestLocator: manifestLocator}, nil
}

// DecodeEmpty authenticates and recovers ticket 10's empty Logical Repository.
func (r *Registry) DecodeEmpty(secret domain.RecoverySecret, bootstrap, manifest []byte) (EmptyRepository, error) {
	headerBytes, err := probePreamble(bootstrap)
	if err != nil {
		return EmptyRepository{}, err
	}
	var header bootstrapHeader
	if err := r.decode.Unmarshal(headerBytes, &header); err != nil {
		return EmptyRepository{}, fmt.Errorf("decode bootstrap header: %w", err)
	}
	canonicalHeader, err := r.encode.Marshal(header)
	if err != nil || !bytes.Equal(canonicalHeader, headerBytes) {
		return EmptyRepository{}, errors.New("bootstrap header is not canonical CBOR")
	}
	if len(header.RepositoryID) != 16 || header.FormatMajor != FormatMajor || header.FormatMinor != FormatMinor ||
		header.CryptographicSuite != CryptographicSuite || header.ChunkSize != DefaultChunkSize || header.Generation != 1 {
		return EmptyRepository{}, errors.New("bootstrap header disagrees with supported v1 format")
	}
	var repositoryID [16]byte
	copy(repositoryID[:], header.RepositoryID)
	unsignedBytes, err := r.encode.Marshal(unsignedBootstrapHeader{
		RepositoryID:       header.RepositoryID,
		FormatMajor:        header.FormatMajor,
		FormatMinor:        header.FormatMinor,
		CryptographicSuite: header.CryptographicSuite,
		ChunkSize:          header.ChunkSize,
		Generation:         header.Generation,
		ManifestLocator:    header.ManifestLocator,
	})
	if err != nil {
		return EmptyRepository{}, fmt.Errorf("encode bootstrap authentication input: %w", err)
	}
	headerKey, err := deriveKey(secret, repositoryID, headerAuthenticationPurpose)
	if err != nil {
		return EmptyRepository{}, err
	}
	authenticator := hmac.New(sha256.New, headerKey[:])
	authenticator.Write(bootstrap[:preambleSize])
	authenticator.Write(unsignedBytes)
	if !hmac.Equal(header.HeaderMAC, authenticator.Sum(nil)) {
		return EmptyRepository{}, errors.New("bootstrap header authentication failed")
	}
	if header.ManifestLocator != opaqueContentIdentifier(manifest) {
		return EmptyRepository{}, errors.New("encrypted manifest locator mismatch")
	}
	if len(manifest) < 28 {
		return EmptyRepository{}, errors.New("encrypted manifest is truncated")
	}
	plaintextLength := len(manifest) - 12 - 16
	if plaintextLength > maximumManifestSize {
		return EmptyRepository{}, errors.New("encrypted manifest exceeds v1 size limit")
	}
	associatedData, err := r.manifestAssociatedData(repositoryID, plaintextLength)
	if err != nil {
		return EmptyRepository{}, err
	}
	manifestKey, err := deriveKey(secret, repositoryID, manifestEncryptionPurpose)
	if err != nil {
		return EmptyRepository{}, err
	}
	primitive, err := r.newAEAD(manifestKey)
	if err != nil {
		return EmptyRepository{}, err
	}
	manifestPlaintext, err := primitive.Decrypt(manifest, associatedData)
	if err != nil {
		return EmptyRepository{}, errors.New("encrypted manifest authentication failed")
	}
	var decoded emptyManifest
	if err := r.decode.Unmarshal(manifestPlaintext, &decoded); err != nil {
		return EmptyRepository{}, fmt.Errorf("decode encrypted manifest: %w", err)
	}
	canonicalManifest, err := r.encode.Marshal(decoded)
	if err != nil || !bytes.Equal(canonicalManifest, manifestPlaintext) {
		return EmptyRepository{}, errors.New("encrypted manifest is not canonical CBOR")
	}
	if decoded.Generation != 1 || len(decoded.LogicalRefs) != 0 || len(decoded.PackPayloads) != 0 || len(decoded.RequiredFeatures) != 0 {
		return EmptyRepository{}, errors.New("manifest is not a supported empty v1 snapshot")
	}
	repository := EmptyRepository{RepositoryID: repositoryID, LogicalHEAD: domain.LogicalHEAD(decoded.LogicalHEAD), ObjectFormat: decoded.ObjectFormat}
	if err := validateEmptyRepository(repository); err != nil {
		return EmptyRepository{}, err
	}
	return repository, nil
}

func (r *Registry) manifestAssociatedData(repositoryID domain.RepositoryID, plaintextLength int) ([]byte, error) {
	data, err := r.encode.Marshal(recordContext{
		Protocol:     "git-remote-cloak",
		FormatMajor:  FormatMajor,
		FormatMinor:  FormatMinor,
		Suite:        CryptographicSuite,
		RepositoryID: repositoryID[:],
		RecordKind:   manifestKind,
		Generation:   1,
		Final:        true,
		PlaintextLen: uint64(plaintextLength),
	})
	if err != nil {
		return nil, fmt.Errorf("encode manifest associated data: %w", err)
	}
	return data, nil
}

func encodePreamble(headerLength int) ([]byte, error) {
	if headerLength <= 0 || headerLength > maximumHeaderSize {
		return nil, fmt.Errorf("bootstrap header length %d exceeds limit", headerLength)
	}
	preamble := make([]byte, preambleSize)
	copy(preamble, magic[:])
	preamble[8] = BootstrapFraming
	preamble[9] = FormatMajor
	preamble[10] = FormatMinor
	preamble[11] = 0
	binary.BigEndian.PutUint32(preamble[12:], uint32(headerLength))
	return preamble, nil
}

func probePreamble(bootstrap []byte) ([]byte, error) {
	if len(bootstrap) < preambleSize || !bytes.Equal(bootstrap[:8], magic[:]) {
		return nil, errors.New("unknown bootstrap framing")
	}
	if bootstrap[8] != BootstrapFraming {
		return nil, errors.New("unsupported bootstrap framing version")
	}
	if bootstrap[9] != FormatMajor {
		return nil, errors.New("unsupported repository format major version")
	}
	if bootstrap[10] != FormatMinor {
		return nil, errors.New("unsupported repository format minor version")
	}
	if bootstrap[11] != 0 {
		return nil, errors.New("unknown required repository feature")
	}
	headerLength := int(binary.BigEndian.Uint32(bootstrap[12:16]))
	if headerLength <= 0 || headerLength > maximumHeaderSize || len(bootstrap) != preambleSize+headerLength {
		return nil, errors.New("invalid bounded bootstrap header length")
	}
	return bootstrap[preambleSize:], nil
}

func validateEmptyRepository(repository EmptyRepository) error {
	if repository.RepositoryID == ([16]byte{}) {
		return errors.New("Repository ID must not be zero")
	}
	if repository.LogicalHEAD == "" || len(repository.LogicalHEAD) > 1024 || !strings.HasPrefix(string(repository.LogicalHEAD), "refs/heads/") {
		return errors.New("Logical HEAD must name a branch")
	}
	if repository.ObjectFormat != "sha1" && repository.ObjectFormat != "sha256" {
		return errors.New("unsupported Git object format")
	}
	return nil
}

func deriveKey(secret domain.RecoverySecret, repositoryID domain.RepositoryID, purpose keyPurpose) ([32]byte, error) {
	info := []byte("git-remote-cloak/v1/aes-256-gcm-siv/" + string(purpose))
	reader := hkdf.New(sha256.New, secret[:], repositoryID[:], info)
	var key [32]byte
	if _, err := io.ReadFull(reader, key[:]); err != nil {
		return [32]byte{}, fmt.Errorf("derive %s key: %w", purpose, err)
	}
	return key, nil
}

func newAESGCMSIV(key [32]byte) (aeadPrimitive, error) {
	serializedKey, err := proto.Marshal(&aesgcmsivpb.AesGcmSivKey{Version: 0, KeyValue: key[:]})
	if err != nil {
		return nil, fmt.Errorf("serialize AES-GCM-SIV key: %w", err)
	}
	handle := insecurecleartextkeyset.KeysetHandle(&tinkpb.Keyset{
		PrimaryKeyId: 1,
		Key: []*tinkpb.Keyset_Key{{
			KeyData: &tinkpb.KeyData{
				TypeUrl:         "type.googleapis.com/google.crypto.tink.AesGcmSivKey",
				Value:           serializedKey,
				KeyMaterialType: tinkpb.KeyData_SYMMETRIC,
			},
			Status:           tinkpb.KeyStatusType_ENABLED,
			KeyId:            1,
			OutputPrefixType: tinkpb.OutputPrefixType_RAW,
		}},
	})
	primitive, err := aead.New(handle)
	if err != nil {
		return nil, fmt.Errorf("create Tink AES-GCM-SIV primitive: %w", err)
	}
	return primitive, nil
}

func opaqueContentIdentifier(ciphertext []byte) string {
	digest := sha256.Sum256(ciphertext)
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(digest[:]))
}
