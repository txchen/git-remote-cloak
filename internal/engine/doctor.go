package engine

import (
	"encoding/hex"
	"errors"
	"os"
	"sort"
	"strings"

	"github.com/txchen/git-remote-cloak/internal/domain"
	"github.com/txchen/git-remote-cloak/internal/gitdb"
	"github.com/txchen/git-remote-cloak/internal/localstate"
	"github.com/txchen/git-remote-cloak/internal/storage"
)

// DiagnosticCheck is one safe, machine-readable doctor result.
type DiagnosticCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// DoctorReport contains only public Ciphertext Repository metadata and safe
// diagnostic categories. It deliberately excludes Logical Refs and object IDs.
type DoctorReport struct {
	OK                bool                         `json:"ok"`
	RepositoryID      string                       `json:"repository_id,omitempty"`
	Generation        uint64                       `json:"generation,omitempty"`
	StorageCommitID   string                       `json:"storage_commit_id,omitempty"`
	Freshness         string                       `json:"freshness"`
	ProviderError     string                       `json:"provider_error,omitempty"`
	Checks            []DiagnosticCheck            `json:"checks"`
	CiphertextObjects []CiphertextObjectDiagnostic `json:"ciphertext_objects,omitempty"`
}

// CiphertextObjectDiagnostic identifies only public opaque ciphertext and its
// public size; neither field reveals its Protected Plaintext.
type CiphertextObjectDiagnostic struct {
	Identifier string `json:"identifier"`
	Size       int    `json:"size,omitempty"`
	Status     string `json:"status"`
}

const (
	doctorBootstrapCheck = iota
	doctorManifestCheck
	doctorCiphertextCheck
	doctorPackIndexCheck
	doctorLFSCheck
	doctorLogicalRefsCheck
	doctorObjectGraphCheck
	doctorRollbackCheck
)

var doctorCheckNames = []string{
	"bootstrap_header_authentication",
	"encrypted_manifest_authentication",
	"ciphertext_availability_and_integrity",
	"pack_index_consistency",
	"lfs_backed_content",
	"logical_ref_targets",
	"recovered_git_object_graph",
	"rollback_checkpoint",
}

// Doctor authenticates and reconstructs the current snapshot without writing
// local trusted state or changing the Repository Host.
func (engine *Engine) Doctor(repositoryURL string, secret domain.RecoverySecret) (DoctorReport, error) {
	report := newDoctorReport()
	transport, err := storage.OpenGit(repositoryURL)
	if err != nil {
		failDoctorCheck(&report, doctorBootstrapCheck, "Repository Host could not be read safely")
		report.ProviderError = safeProviderError(err)
		return report, errors.New("doctor could not read Repository Host")
	}
	defer transport.Close()
	refs, err := transport.Refs()
	if err != nil || len(refs) != 1 || refs[0] != storage.StorageRef {
		failDoctorCheck(&report, doctorBootstrapCheck, "Repository Host does not expose exactly one Storage Ref")
		if err != nil {
			report.ProviderError = safeProviderError(err)
		}
		return report, errors.New("doctor found an invalid public ref surface")
	}
	storageCommitID, err := transport.Current()
	if err != nil {
		failDoctorCheck(&report, doctorBootstrapCheck, "Storage Ref target commit could not be read")
		report.ProviderError = safeProviderError(err)
		return report, errors.New("doctor could not read Storage Ref")
	}
	report.StorageCommitID = storageCommitID
	bootstrap, err := transport.ReadBootstrapAt(storageCommitID)
	if err != nil {
		failDoctorCheck(&report, doctorBootstrapCheck, "Bootstrap Header could not be read")
		report.ProviderError = safeProviderError(err)
		return report, errors.New("doctor could not read Bootstrap Header")
	}
	ciphertextObjects := make(map[string]CiphertextObjectDiagnostic)
	var providerError error
	decoded, err := engine.formats.DecodeSnapshotFrom(secret, bootstrap, func(identifier string) ([]byte, error) {
		contents, readErr := transport.ReadObject(storageCommitID, identifier)
		diagnostic := CiphertextObjectDiagnostic{Identifier: identifier, Status: "ok", Size: len(contents)}
		if readErr != nil {
			diagnostic.Status = "missing_or_damaged"
			if providerError == nil {
				providerError = readErr
			}
		}
		ciphertextObjects[identifier] = diagnostic
		return contents, readErr
	})
	report.CiphertextObjects = sortedCiphertextDiagnostics(ciphertextObjects)
	if providerError != nil {
		report.ProviderError = safeProviderError(providerError)
	}
	if err != nil {
		classifySnapshotDiagnostic(&report, err)
		return report, errors.New("doctor found invalid Ciphertext Snapshot data")
	}
	report.RepositoryID = hex.EncodeToString(decoded.Repository.RepositoryID[:])
	report.Generation = decoded.Repository.Generation
	for index := doctorBootstrapCheck; index <= doctorPackIndexCheck; index++ {
		report.Checks[index].Status = "ok"
	}
	state := gitdb.State{
		LogicalHEAD: decoded.Repository.LogicalHEAD, ObjectFormat: decoded.Repository.ObjectFormat,
		LogicalRefs: decoded.Repository.LogicalRefs,
	}
	if err := gitdb.ValidateLogicalRepository(state, decoded.Packs); err != nil {
		classifyLogicalDiagnostic(&report, err)
		return report, errors.New("doctor found an invalid Logical Repository graph")
	}
	for index := doctorLFSCheck; index <= doctorObjectGraphCheck; index++ {
		report.Checks[index].Status = "ok"
	}
	_, checkpointExists, checkpointErr := localstate.LoadCheckpoint(engine.localGitDirectory)
	if checkpointErr != nil {
		failDoctorCheck(&report, doctorRollbackCheck, "Trusted Rollback Checkpoint is damaged or unreadable")
		report.Freshness = "trusted_checkpoint_unavailable"
		return report, errors.New("doctor could not validate trusted Rollback Checkpoint")
	}
	if checkpointExists {
		if err := localstate.CheckCheckpoint(engine.localGitDirectory, decoded.Repository.RepositoryID, decoded.Repository.Generation,
			storageCommitID, decoded.Repository.PreviousStorageRef, transport.StorageHistoryContinues); err != nil {
			failDoctorCheck(&report, doctorRollbackCheck, "Current authenticated snapshot contradicts trusted Rollback Checkpoint")
			report.Freshness = "failed_trusted_checkpoint"
			return report, errors.New("doctor found a suspected rollback")
		}
		report.Checks[doctorRollbackCheck].Status = "ok"
		report.Freshness = "checked_against_trusted_checkpoint"
	} else {
		report.Checks[doctorRollbackCheck].Status = "not_available"
	}
	report.OK = true
	return report, nil
}

func sortedCiphertextDiagnostics(objects map[string]CiphertextObjectDiagnostic) []CiphertextObjectDiagnostic {
	identifiers := make([]string, 0, len(objects))
	for identifier := range objects {
		identifiers = append(identifiers, identifier)
	}
	sort.Strings(identifiers)
	diagnostics := make([]CiphertextObjectDiagnostic, 0, len(identifiers))
	for _, identifier := range identifiers {
		diagnostics = append(diagnostics, objects[identifier])
	}
	return diagnostics
}

func newDoctorReport() DoctorReport {
	checks := make([]DiagnosticCheck, len(doctorCheckNames))
	for index, name := range doctorCheckNames {
		checks[index] = DiagnosticCheck{Name: name, Status: "not_run"}
	}
	return DoctorReport{Freshness: "unverifiable_without_trusted_checkpoint", Checks: checks}
}

func classifySnapshotDiagnostic(report *DoctorReport, diagnostic error) {
	message := strings.ToLower(diagnostic.Error())
	switch {
	case strings.Contains(message, "bootstrap") || strings.Contains(message, "preamble"):
		failDoctorCheck(report, doctorBootstrapCheck, "Bootstrap Header is unsupported, malformed, or unauthenticated")
	case strings.Contains(message, "manifest"):
		report.Checks[doctorBootstrapCheck].Status = "ok"
		failDoctorCheck(report, doctorManifestCheck, "Encrypted Manifest is missing, damaged, or unauthenticated")
	case strings.Contains(message, "pack index"):
		setDoctorChecksOK(report, doctorBootstrapCheck, doctorPackIndexCheck)
		failDoctorCheck(report, doctorPackIndexCheck, "Encrypted Pack Index is missing, damaged, or inconsistent")
	case strings.Contains(message, "pack chunk"):
		setDoctorChecksOK(report, doctorBootstrapCheck, doctorCiphertextCheck)
		failDoctorCheck(report, doctorCiphertextCheck, "Encrypted Pack Chunk is missing, damaged, or unauthenticated")
	default:
		setDoctorChecksOK(report, doctorBootstrapCheck, doctorCiphertextCheck)
		failDoctorCheck(report, doctorCiphertextCheck, "Ciphertext objects are missing, damaged, or inconsistent")
	}
}

func classifyLogicalDiagnostic(report *DoctorReport, diagnostic error) {
	message := strings.ToLower(diagnostic.Error())
	switch {
	case strings.Contains(message, "git lfs"):
		failDoctorCheck(report, doctorLFSCheck, "Logical Repository depends on unsupported Git LFS-backed content")
	case strings.Contains(message, "logical ref"):
		report.Checks[doctorLFSCheck].Status = "ok"
		failDoctorCheck(report, doctorLogicalRefsCheck, "Logical Ref targets disagree with authenticated metadata")
	default:
		report.Checks[doctorLFSCheck].Status = "ok"
		report.Checks[doctorLogicalRefsCheck].Status = "ok"
		failDoctorCheck(report, doctorObjectGraphCheck, "Recovered Git object graph is missing, damaged, or inconsistent")
	}
}

func failDoctorCheck(report *DoctorReport, index int, detail string) {
	report.Checks[index].Status = "error"
	report.Checks[index].Detail = detail
}

func setDoctorChecksOK(report *DoctorReport, start, end int) {
	for index := start; index < end; index++ {
		report.Checks[index].Status = "ok"
	}
}

func safeProviderError(diagnostic error) string {
	message := strings.Join(strings.Fields(diagnostic.Error()), " ")
	valuesToRedact := make([]string, 0)
	for _, entry := range os.Environ() {
		_, value, found := strings.Cut(entry, "=")
		if found && value != "" {
			valuesToRedact = append(valuesToRedact, value)
		}
	}
	sort.Slice(valuesToRedact, func(left, right int) bool { return len(valuesToRedact[left]) > len(valuesToRedact[right]) })
	for _, value := range valuesToRedact {
		message = strings.ReplaceAll(message, value, "[REDACTED]")
	}
	return redactURLUserInfo(message)
}

func redactURLUserInfo(message string) string {
	searchFrom := 0
	for {
		schemeOffset := strings.Index(message[searchFrom:], "://")
		if schemeOffset < 0 {
			return message
		}
		credentialsStart := searchFrom + schemeOffset + 3
		remainder := message[credentialsStart:]
		atOffset := strings.IndexByte(remainder, '@')
		if atOffset < 0 {
			return message
		}
		if spaceOffset := strings.IndexAny(remainder, " \t\r\n"); spaceOffset >= 0 && spaceOffset < atOffset {
			searchFrom = credentialsStart
			continue
		}
		message = message[:credentialsStart] + "[REDACTED]" + remainder[atOffset:]
		searchFrom = credentialsStart + len("[REDACTED]") + 1
	}
}
