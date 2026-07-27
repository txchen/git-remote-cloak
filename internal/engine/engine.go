// Package engine coordinates repository-level operations behind command frontends.
package engine

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/txchen/git-remote-cloak/internal/domain"
	cloakformat "github.com/txchen/git-remote-cloak/internal/format"
	"github.com/txchen/git-remote-cloak/internal/gitdb"
	"github.com/txchen/git-remote-cloak/internal/localstate"
	"github.com/txchen/git-remote-cloak/internal/storage"
)

// Engine is the repository-level interface shared by human and Git adapters.
type Engine struct {
	formats           *cloakformat.Registry
	localGitDirectory string
}

// New returns a production Repository Engine.
func New() *Engine { return &Engine{formats: cloakformat.NewRegistry()} }

// NewWithLocalState returns an Engine that may reuse Secret-free persistent
// state owned by gitDirectory.
func NewWithLocalState(gitDirectory string) *Engine {
	return &Engine{formats: cloakformat.NewRegistry(), localGitDirectory: gitDirectory}
}

// Initialize publishes or verifies an empty Ciphertext Repository and configures a remote.
func (engine *Engine) Initialize(workspace, remoteName, repositoryURL, defaultBranch string, secret domain.RecoverySecret) error {
	if remoteName == "" || repositoryURL == "" {
		return errors.New("remote name and Repository Host URL are required")
	}
	if _, err := git(workspace, nil, "rev-parse", "--git-dir"); err != nil {
		return errors.New("init must run inside a Git repository")
	}
	logicalHEAD, err := git(workspace, nil, "symbolic-ref", "HEAD")
	if err != nil {
		if defaultBranch == "" {
			return errors.New("detached HEAD requires --default-branch")
		}
		logicalHEAD = []byte("refs/heads/" + strings.TrimPrefix(defaultBranch, "refs/heads/"))
	}
	logicalHEADName := strings.TrimSpace(string(logicalHEAD))
	if defaultBranch != "" {
		logicalHEADName = "refs/heads/" + strings.TrimPrefix(defaultBranch, "refs/heads/")
	}
	branchName := strings.TrimPrefix(logicalHEADName, "refs/heads/")
	if _, err := git(workspace, nil, "check-ref-format", "--branch", branchName); err != nil {
		return errors.New("Logical HEAD is not a valid Git branch name")
	}
	wantedURL := "cloak::" + repositoryURL
	configuredURL, configuredErr := git(workspace, nil, "remote", "get-url", remoteName)
	if configuredErr == nil && strings.TrimSpace(string(configuredURL)) != wantedURL {
		return errors.New("existing remote configuration does not match Cloak identity")
	}
	configuredRepositoryID, repositoryIDErr := git(workspace, nil, "config", "--get", "remote."+remoteName+".cloakRepositoryID")
	hasConfiguredRepositoryID := repositoryIDErr == nil
	transport, err := storage.OpenGit(repositoryURL)
	if err != nil {
		return err
	}
	defer transport.Close()
	refs, err := transport.Refs()
	if err != nil {
		return err
	}
	if len(refs) > 0 && (configuredErr != nil || !hasConfiguredRepositoryID) {
		return errors.New("existing Ciphertext Repository requires a matching configured remote and Repository ID")
	}
	var repository cloakformat.EmptyRepository
	var storageCommitID string
	var generation uint64
	switch {
	case len(refs) == 0:
		if hasConfiguredRepositoryID {
			return errors.New("recorded Repository ID does not match empty Repository Host")
		}
		if _, err := rand.Read(repository.RepositoryID[:]); err != nil {
			return fmt.Errorf("generate Repository ID: %w", err)
		}
		objectFormat, err := git(workspace, nil, "rev-parse", "--show-object-format")
		if err != nil {
			return fmt.Errorf("read Git object format: %w", err)
		}
		repository.LogicalHEAD = domain.LogicalHEAD(logicalHEADName)
		repository.ObjectFormat = strings.TrimSpace(string(objectFormat))
		encoded, err := engine.formats.EncodeEmpty(secret, repository)
		if err != nil {
			return err
		}
		expectedStorageCommitID, err := transport.Current()
		if err != nil {
			return err
		}
		storageCommitID, err = transport.PublishSnapshot(expectedStorageCommitID, encoded.Bootstrap, map[string][]byte{encoded.ManifestLocator: encoded.Manifest})
		if err != nil {
			return err
		}
		generation = 1
	case len(refs) == 1 && refs[0] == storage.StorageRef:
		decoded, currentStorageCommitID, err := engine.decodeTransportSnapshot(secret, transport)
		if err != nil {
			return errors.New("existing Ciphertext Repository has another Cloak identity")
		}
		repository = cloakformat.EmptyRepository{
			RepositoryID: decoded.Repository.RepositoryID, LogicalHEAD: decoded.Repository.LogicalHEAD,
			ObjectFormat: decoded.Repository.ObjectFormat,
		}
		storageCommitID = currentStorageCommitID
		generation = decoded.Repository.Generation
		if repository.LogicalHEAD != domain.LogicalHEAD(logicalHEADName) {
			return errors.New("existing Ciphertext Repository Logical HEAD does not match")
		}
	default:
		return errors.New("Repository Host contains foreign refs")
	}
	if hasConfiguredRepositoryID && strings.TrimSpace(string(configuredRepositoryID)) != hex.EncodeToString(repository.RepositoryID[:]) {
		return errors.New("recorded Repository ID does not match Ciphertext Repository")
	}
	if configuredErr != nil {
		if _, err := git(workspace, nil, "remote", "add", remoteName, wantedURL); err != nil {
			return fmt.Errorf("configure Cloak remote: %w", err)
		}
	}
	if _, err := git(workspace, nil, "config", "remote."+remoteName+".cloakRepositoryID", hex.EncodeToString(repository.RepositoryID[:])); err != nil {
		return fmt.Errorf("record public Repository ID: %w", err)
	}
	if engine.localGitDirectory != "" {
		if err := localstate.ObserveCheckpoint(engine.localGitDirectory, repository.RepositoryID, generation, storageCommitID, "", transport.StorageHistoryContinues); err != nil {
			return err
		}
	}
	return nil
}

// SetHead atomically changes the encrypted Logical HEAD.
func (engine *Engine) SetHead(workspace, remoteName, branch string, secret domain.RecoverySecret) error {
	if remoteName == "" || branch == "" {
		return errors.New("remote name and Logical HEAD branch are required")
	}
	logicalHEAD := "refs/heads/" + strings.TrimPrefix(branch, "refs/heads/")
	if _, err := git(workspace, nil, "check-ref-format", "--branch", strings.TrimPrefix(logicalHEAD, "refs/heads/")); err != nil {
		return errors.New("Logical HEAD is not a valid Git branch name")
	}
	configuredURL, err := git(workspace, nil, "remote", "get-url", remoteName)
	if err != nil {
		return fmt.Errorf("read Cloak remote %s: %w", remoteName, err)
	}
	repositoryURL, found := strings.CutPrefix(strings.TrimSpace(string(configuredURL)), "cloak::")
	if !found || repositoryURL == "" {
		return errors.New("configured remote is not a Cloak remote")
	}
	operationLock, err := localstate.AcquireOperationLock(engine.localGitDirectory)
	if err != nil {
		return err
	}
	defer operationLock.Close()
	transport, err := storage.OpenGit(repositoryURL)
	if err != nil {
		return err
	}
	defer transport.Close()
	current, storageCommitID, err := engine.decodeTransportSnapshot(secret, transport)
	if err != nil {
		return err
	}
	repository := current.Repository
	repository.Generation++
	repository.PreviousStorageRef = storageCommitID
	repository.LogicalHEAD = domain.LogicalHEAD(logicalHEAD)
	encoded, err := engine.formats.EncodeSnapshot(secret, cloakformat.SnapshotInput{Repository: repository, Packs: current.Packs})
	if err != nil {
		return err
	}
	candidate, err := engine.formats.DecodeSnapshot(secret, encoded.Bootstrap, encoded.CiphertextObjects)
	if err != nil {
		return fmt.Errorf("validate Logical HEAD snapshot: %w", err)
	}
	if candidate.Repository.LogicalHEAD != repository.LogicalHEAD || !maps.Equal(candidate.Repository.LogicalRefs, repository.LogicalRefs) {
		return errors.New("candidate Ciphertext Snapshot does not preserve the intended Logical HEAD state")
	}
	publishedStorageCommitID, err := transport.PublishSnapshot(storageCommitID, encoded.Bootstrap, encoded.CiphertextObjects)
	if err != nil {
		return err
	}
	return localstate.ObserveCheckpoint(engine.localGitDirectory, candidate.Repository.RepositoryID, candidate.Repository.Generation,
		publishedStorageCommitID, candidate.Repository.PreviousStorageRef, transport.StorageHistoryContinues)
}

// Recover atomically creates and validates a complete Recovered Repository.
func (engine *Engine) Recover(repositoryURL, destination string, secret domain.RecoverySecret) error {
	decoded, err := engine.readSnapshot(repositoryURL, secret)
	if err != nil {
		return err
	}
	if len(decoded.Repository.LogicalRefs) > 0 {
		return engine.recoverDecoded(repositoryURL, destination, decoded)
	}
	if destination == "" {
		destination = defaultDestination(repositoryURL)
	}
	return localstate.PublishDirectory(destination, func(temporary string) error {
		branch := strings.TrimPrefix(string(decoded.Repository.LogicalHEAD), "refs/heads/")
		if _, err := git(filepath.Dir(temporary), nil, "init", "--object-format="+decoded.Repository.ObjectFormat, "-b", branch, temporary); err != nil {
			return fmt.Errorf("initialize Recovered Repository: %w", err)
		}
		if _, err := git(temporary, nil, "remote", "add", "origin", "cloak::"+repositoryURL); err != nil {
			return fmt.Errorf("configure recovered origin: %w", err)
		}
		if _, err := git(temporary, nil, "fsck", "--full"); err != nil {
			return fmt.Errorf("validate Recovered Repository: %w", err)
		}
		if err := recordRecoveredCheckpoint(temporary, decoded); err != nil {
			return err
		}
		return nil
	})
}

// RecoverHistorical explicitly reconstructs one retained authenticated
// Storage History generation into a separate local repository.
func (engine *Engine) RecoverHistorical(repositoryURL, storageCommitID, destination string, secret domain.RecoverySecret) error {
	if destination == "" {
		return errors.New("historical recovery requires a separate destination")
	}
	if !validStorageCommitID(storageCommitID) {
		return errors.New("historical recovery requires a complete Storage Ref object ID")
	}
	transport, err := storage.OpenGit(repositoryURL)
	if err != nil {
		return err
	}
	defer transport.Close()
	currentStorageCommitID, err := transport.Current()
	if err != nil {
		return err
	}
	if !transport.StorageHistoryContinues(storageCommitID, currentStorageCommitID) {
		return errors.New("requested Storage Ref is not retained in current Storage History")
	}
	decoded, err := engine.decodeTransportSnapshotAt(secret, transport, storageCommitID, false)
	if err != nil {
		return fmt.Errorf("authenticate historical Ciphertext Snapshot: %w", err)
	}
	return engine.recoverDecoded(repositoryURL, destination, authenticatedSnapshot{
		DecodedSnapshot: decoded, StorageCommitID: storageCommitID,
	})
}

// RecoverEmpty preserves the ticket #10 empty-repository interface.
func (engine *Engine) RecoverEmpty(repositoryURL, destination string, secret domain.RecoverySecret) error {
	return engine.Recover(repositoryURL, destination, secret)
}

// RecoverForGitClone atomically prepares refs and objects while leaving worktree checkout to Git.
func (engine *Engine) RecoverForGitClone(repositoryURL, destination string, secret domain.RecoverySecret) error {
	decoded, err := engine.readSnapshot(repositoryURL, secret)
	if err != nil {
		return err
	}
	if len(decoded.Repository.LogicalRefs) == 0 {
		return engine.RecoverEmpty(repositoryURL, destination, secret)
	}
	if destination == "" {
		destination = defaultDestination(repositoryURL)
	}
	return localstate.PublishDirectory(destination, func(temporary string) error {
		state := gitdb.State{LogicalHEAD: decoded.Repository.LogicalHEAD, ObjectFormat: decoded.Repository.ObjectFormat, LogicalRefs: decoded.Repository.LogicalRefs}
		if err := gitdb.ValidateLogicalRepository(state, decoded.Packs); err != nil {
			return err
		}
		if err := gitdb.RestoreForClone(temporary, state, decoded.Packs); err != nil {
			return err
		}
		if _, err := git(temporary, nil, "remote", "add", "origin", "cloak::"+repositoryURL); err != nil {
			return fmt.Errorf("configure recovered origin: %w", err)
		}
		if err := recordRecoveredCheckpoint(temporary, decoded); err != nil {
			return err
		}
		return nil
	})
}

// RecoverBare restores a validated temporary bare Logical Repository for Git protocol service.
func (engine *Engine) RecoverBare(repositoryURL, destination string, secret domain.RecoverySecret) error {
	decoded, err := engine.readSnapshot(repositoryURL, secret)
	if err != nil {
		return err
	}
	if len(decoded.Repository.LogicalRefs) == 0 {
		_, err := makeEmptyBare(destination, decoded.Repository)
		return err
	}
	return gitdb.Restore(destination, true, gitdb.State{
		LogicalHEAD: decoded.Repository.LogicalHEAD, ObjectFormat: decoded.Repository.ObjectFormat,
		LogicalRefs: decoded.Repository.LogicalRefs,
	}, decoded.Packs)
}

// Publish reads the complete Logical Repository state and atomically publishes it.
func (engine *Engine) Publish(repositoryURL, logicalGitDirectory string, secret domain.RecoverySecret) error {
	transport, err := storage.OpenGit(repositoryURL)
	if err != nil {
		return err
	}
	defer transport.Close()
	current, storageCommitID, err := engine.decodeTransportSnapshot(secret, transport)
	if err != nil {
		return err
	}
	return engine.publishCurrent(transport, current, storageCommitID, logicalGitDirectory, secret)
}

func (engine *Engine) publishCurrent(transport *storage.Git, current cloakformat.DecodedSnapshot, storageCommitID, logicalGitDirectory string, secret domain.RecoverySecret) error {
	state, err := gitdb.ReadState(logicalGitDirectory)
	if err != nil {
		return err
	}
	if state.LogicalHEAD != current.Repository.LogicalHEAD || state.ObjectFormat != current.Repository.ObjectFormat {
		return errors.New("pushed Logical Repository identity does not match initialized Ciphertext Repository")
	}
	reachableObjectIDs, err := gitdb.ReachableObjectIDs(logicalGitDirectory)
	if err != nil {
		return err
	}
	liveObjects := make(map[string]struct{}, len(reachableObjectIDs))
	for _, objectID := range reachableObjectIDs {
		liveObjects[objectID] = struct{}{}
	}
	packs := make([]cloakformat.PackPayload, 0, len(current.Packs)+1)
	coveredObjects := make(map[string]struct{})
	for _, payload := range current.Packs {
		live := false
		for _, objectID := range payload.ObjectIDs {
			if _, exists := liveObjects[objectID]; exists {
				live = true
			}
		}
		if !live {
			continue
		}
		packs = append(packs, payload)
		for _, objectID := range payload.ObjectIDs {
			coveredObjects[objectID] = struct{}{}
		}
	}
	newObjectIDs := make([]string, 0)
	for _, objectID := range reachableObjectIDs {
		if _, exists := coveredObjects[objectID]; !exists {
			newObjectIDs = append(newObjectIDs, objectID)
		}
	}
	if err := gitdb.RejectLFSPointers(logicalGitDirectory, newObjectIDs); err != nil {
		return err
	}
	if len(newObjectIDs) > 0 {
		payload, err := gitdb.CreatePackForObjects(logicalGitDirectory, newObjectIDs)
		if err != nil {
			return err
		}
		packs = append(packs, payload)
	}
	repository := cloakformat.SnapshotState{
		RepositoryID: current.Repository.RepositoryID, Generation: current.Repository.Generation + 1,
		LogicalHEAD: state.LogicalHEAD, ObjectFormat: state.ObjectFormat, LogicalRefs: state.LogicalRefs,
		PreviousStorageRef: storageCommitID,
	}
	encoded, err := engine.formats.EncodeSnapshot(secret, cloakformat.SnapshotInput{
		Repository: repository, Packs: packs,
	})
	if err != nil {
		return err
	}
	// Validate the exact candidate logical state before uploading any ciphertext.
	candidate, err := engine.formats.DecodeSnapshot(secret, encoded.Bootstrap, encoded.CiphertextObjects)
	if err != nil {
		return fmt.Errorf("validate candidate Ciphertext Snapshot: %w", err)
	}
	temporaryRoot, err := os.MkdirTemp("", "git-remote-cloak-candidate-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporaryRoot)
	temporary := filepath.Join(temporaryRoot, "repository.git")
	candidateState := gitdb.State{
		LogicalHEAD: candidate.Repository.LogicalHEAD, ObjectFormat: candidate.Repository.ObjectFormat,
		LogicalRefs: candidate.Repository.LogicalRefs,
	}
	if err := gitdb.Restore(temporary, true, candidateState, candidate.Packs); err != nil {
		return fmt.Errorf("validate candidate Logical Repository: %w", err)
	}
	if candidateState.LogicalHEAD != state.LogicalHEAD || candidateState.ObjectFormat != state.ObjectFormat || !maps.Equal(candidateState.LogicalRefs, state.LogicalRefs) {
		return errors.New("candidate Ciphertext Snapshot does not preserve the intended Logical Repository state")
	}
	candidateObjectIDs, err := gitdb.ReachableObjectIDs(temporary)
	if err != nil {
		return err
	}
	if strings.Join(candidateObjectIDs, "\n") != strings.Join(reachableObjectIDs, "\n") {
		return errors.New("candidate Ciphertext Snapshot does not preserve the intended reachable Git objects")
	}
	preparedStorageCommit, err := transport.PrepareSnapshot(storageCommitID, encoded.Bootstrap, encoded.CiphertextObjects)
	if err != nil {
		return err
	}
	intentID, err := localstate.TransactionIntentID(secret, current.Repository.RepositoryID, canonicalTransactionIntent(state))
	if err != nil {
		return err
	}
	journal := localstate.Transaction{
		IntentID:                intentID,
		StartingStorageCommitID: storageCommitID,
		PreparedStorageCommitID: preparedStorageCommit,
	}
	if err := localstate.StoreTransaction(engine.localGitDirectory, journal, secret, current.Repository.RepositoryID); err != nil {
		return fmt.Errorf("persist crash journal before publication: %w", err)
	}
	if err := transport.PublishPrepared(storageCommitID, preparedStorageCommit); err != nil {
		return err
	}
	if err := localstate.ObserveCheckpoint(engine.localGitDirectory, candidate.Repository.RepositoryID, candidate.Repository.Generation, preparedStorageCommit, candidate.Repository.PreviousStorageRef, transport.StorageHistoryContinues); err != nil {
		return fmt.Errorf("record confirmed publication checkpoint: %w", err)
	}
	if err := localstate.RemoveTransaction(engine.localGitDirectory, intentID); err != nil {
		return fmt.Errorf("remove completed crash journal: %w", err)
	}
	return nil
}

// FetchInto imports authenticated native packs into an existing Git object database.
func (engine *Engine) FetchInto(repositoryURL, gitDirectory string, secret domain.RecoverySecret) error {
	if err := gitdb.RejectPromisorState(gitDirectory); err != nil {
		return err
	}
	decoded, err := engine.readSnapshot(repositoryURL, secret)
	if err != nil {
		return err
	}
	state := gitdb.State{
		LogicalHEAD: decoded.Repository.LogicalHEAD, ObjectFormat: decoded.Repository.ObjectFormat,
		LogicalRefs: decoded.Repository.LogicalRefs,
	}
	if err := gitdb.ValidateLogicalRepository(state, decoded.Packs); err != nil {
		return err
	}
	return gitdb.Import(gitDirectory, decoded.Packs)
}

// RefUpdate is one requested Logical Ref change in an atomic push transaction.
type RefUpdate struct {
	Source         domain.LogicalRefName
	Destination    domain.LogicalRefName
	Force          bool
	ExpectedOld    string
	HasExpectedOld bool
}

// PublishRef applies one direct remote-helper ref update.
func (engine *Engine) PublishRef(repositoryURL, sourceGitDirectory, sourceRef, destinationRef string, force bool, secret domain.RecoverySecret) error {
	return engine.PublishRefs(repositoryURL, sourceGitDirectory, []RefUpdate{{
		Source: domain.LogicalRefName(sourceRef), Destination: domain.LogicalRefName(destinationRef), Force: force,
	}}, secret)
}

// PublishRefs applies every requested Logical Ref change and publishes one snapshot or none.
func (engine *Engine) PublishRefs(repositoryURL, sourceGitDirectory string, updates []RefUpdate, secret domain.RecoverySecret) error {
	if len(updates) == 0 {
		return errors.New("push transaction contains no Logical Ref updates")
	}
	if err := gitdb.RejectPromisorState(sourceGitDirectory); err != nil {
		return err
	}
	lockDirectory := engine.localGitDirectory
	if lockDirectory == "" {
		lockDirectory = sourceGitDirectory
	}
	operationLock, err := localstate.AcquireOperationLock(lockDirectory)
	if err != nil {
		return err
	}
	defer operationLock.Close()

	var concurrentErr error
	for attempt := 1; attempt <= 3; attempt++ {
		concurrentErr = engine.publishRefAttempt(repositoryURL, sourceGitDirectory, updates, secret)
		if concurrentErr == nil {
			return nil
		}
		if !errors.Is(concurrentErr, storage.ErrConcurrentUpdate) {
			return concurrentErr
		}
	}
	return fmt.Errorf("compatible concurrent publication did not succeed after 3 attempts: %w", concurrentErr)
}

func (engine *Engine) publishRefAttempt(repositoryURL, sourceGitDirectory string, updates []RefUpdate, secret domain.RecoverySecret) error {
	transport, err := storage.OpenGit(repositoryURL)
	if err != nil {
		return err
	}
	defer transport.Close()
	current, storageCommitID, err := engine.decodeTransportSnapshot(secret, transport)
	if err != nil {
		return err
	}
	temporaryRoot, err := os.MkdirTemp("", "git-remote-cloak-receive-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporaryRoot)
	temporary := filepath.Join(temporaryRoot, "repository.git")
	if len(current.Repository.LogicalRefs) == 0 {
		if _, err := makeEmptyBare(temporary, current.Repository); err != nil {
			return err
		}
	} else {
		if err := gitdb.Restore(temporary, true, gitdb.State{
			LogicalHEAD: current.Repository.LogicalHEAD, ObjectFormat: current.Repository.ObjectFormat,
			LogicalRefs: current.Repository.LogicalRefs,
		}, current.Packs); err != nil {
			return err
		}
	}
	for _, update := range updates {
		source, destination := string(update.Source), string(update.Destination)
		if !update.Destination.IsSupported() || source != "" && !strings.HasPrefix(source, "refs/") {
			return errors.New("push requires branch or tag refs")
		}
		if update.HasExpectedOld && current.Repository.LogicalRefs[destination] != update.ExpectedOld {
			return fmt.Errorf("stale force-with-lease for Logical Ref %s", destination)
		}
		if source == "" {
			if _, err := git(temporary, nil, "update-ref", "-d", destination); err != nil {
				return fmt.Errorf("delete Logical Ref %s: %w", destination, err)
			}
			continue
		}
		refspec := source + ":" + destination
		if update.Force {
			refspec = "+" + refspec
		}
		command := exec.Command("git", "--git-dir="+temporary, "fetch", "--no-tags", sourceGitDirectory, refspec)
		command.Env = append(cleanGitEnvironment(os.Environ()), "GIT_CONFIG_NOSYSTEM=1")
		if output, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("receive pushed Logical Ref: %s", strings.TrimSpace(string(output)))
		}
	}
	return engine.publishCurrent(transport, current, storageCommitID, temporary, secret)
}

// InspectEmpty authenticates and returns the logical state for a remote-helper adapter.
func (engine *Engine) InspectEmpty(repositoryURL string, secret domain.RecoverySecret) (cloakformat.EmptyRepository, error) {
	decoded, err := engine.readSnapshot(repositoryURL, secret)
	if err != nil {
		return cloakformat.EmptyRepository{}, err
	}
	if len(decoded.Repository.LogicalRefs) != 0 {
		return cloakformat.EmptyRepository{}, errors.New("Ciphertext Snapshot is not empty")
	}
	return cloakformat.EmptyRepository{
		RepositoryID: decoded.Repository.RepositoryID, LogicalHEAD: decoded.Repository.LogicalHEAD,
		ObjectFormat: decoded.Repository.ObjectFormat,
	}, nil
}

// Inspect authenticates and returns the current logical state.
func (engine *Engine) Inspect(repositoryURL string, secret domain.RecoverySecret) (cloakformat.SnapshotState, error) {
	decoded, err := engine.readSnapshot(repositoryURL, secret)
	return decoded.Repository, err
}

type authenticatedSnapshot struct {
	cloakformat.DecodedSnapshot
	StorageCommitID string
}

func (engine *Engine) readSnapshot(repositoryURL string, secret domain.RecoverySecret) (authenticatedSnapshot, error) {
	transport, err := storage.OpenGit(repositoryURL)
	if err != nil {
		return authenticatedSnapshot{}, err
	}
	defer transport.Close()
	refs, err := transport.Refs()
	if err != nil {
		return authenticatedSnapshot{}, err
	}
	if len(refs) != 1 || refs[0] != storage.StorageRef {
		return authenticatedSnapshot{}, errors.New("Repository Host does not expose exactly one Storage Ref")
	}
	decoded, storageCommitID, err := engine.decodeTransportSnapshot(secret, transport)
	return authenticatedSnapshot{DecodedSnapshot: decoded, StorageCommitID: storageCommitID}, err
}

func (engine *Engine) decodeTransportSnapshot(secret domain.RecoverySecret, transport *storage.Git) (cloakformat.DecodedSnapshot, string, error) {
	bootstrap, storageCommitID, err := transport.ReadBootstrap()
	if err != nil {
		return cloakformat.DecodedSnapshot{}, "", err
	}
	decoded, err := engine.decodeTransportSnapshotBytes(secret, transport, bootstrap, storageCommitID, true)
	return decoded, storageCommitID, err
}

func (engine *Engine) decodeTransportSnapshotAt(secret domain.RecoverySecret, transport *storage.Git, storageCommitID string, observe bool) (cloakformat.DecodedSnapshot, error) {
	bootstrap, err := transport.ReadBootstrapAt(storageCommitID)
	if err != nil {
		return cloakformat.DecodedSnapshot{}, err
	}
	return engine.decodeTransportSnapshotBytes(secret, transport, bootstrap, storageCommitID, observe)
}

func (engine *Engine) decodeTransportSnapshotBytes(secret domain.RecoverySecret, transport *storage.Git, bootstrap []byte, storageCommitID string, observe bool) (cloakformat.DecodedSnapshot, error) {
	cache := localstate.NewCache(engine.localGitDirectory)
	downloaded := make(map[string][]byte)
	decoded, err := engine.formats.DecodeSnapshotFrom(secret, bootstrap, func(locator string) ([]byte, error) {
		if cached, found := cache.ReadObject(locator); found {
			return cached, nil
		}
		contents, err := transport.ReadObject(storageCommitID, locator)
		if err == nil {
			downloaded[locator] = contents
		}
		return contents, err
	})
	if err != nil {
		return cloakformat.DecodedSnapshot{}, err
	}
	if observe {
		state := gitdb.State{
			LogicalHEAD: decoded.Repository.LogicalHEAD, ObjectFormat: decoded.Repository.ObjectFormat,
			LogicalRefs: decoded.Repository.LogicalRefs,
		}
		if err := gitdb.ValidateLogicalRepository(state, decoded.Packs); err != nil {
			return cloakformat.DecodedSnapshot{}, err
		}
		if err := localstate.ObserveCheckpoint(engine.localGitDirectory, decoded.Repository.RepositoryID, decoded.Repository.Generation, storageCommitID, decoded.Repository.PreviousStorageRef, transport.StorageHistoryContinues); err != nil {
			return cloakformat.DecodedSnapshot{}, err
		}
	}
	// Cache failure can reduce performance but cannot change recoverability.
	_ = cache.StoreSnapshot(storageCommitID, bootstrap, downloaded)
	localstate.ReconcileTransactions(engine.localGitDirectory, secret, decoded.Repository.RepositoryID, storageCommitID, transport.ContainsStorageCommit)
	return decoded, nil
}

func canonicalTransactionIntent(state gitdb.State) []byte {
	refNames := make([]string, 0, len(state.LogicalRefs))
	for name := range state.LogicalRefs {
		refNames = append(refNames, name)
	}
	sort.Strings(refNames)
	var canonical strings.Builder
	canonical.WriteString("logical-head\x00")
	canonical.WriteString(string(state.LogicalHEAD))
	canonical.WriteString("\x00object-format\x00")
	canonical.WriteString(state.ObjectFormat)
	for _, name := range refNames {
		canonical.WriteString("\x00ref\x00")
		canonical.WriteString(name)
		canonical.WriteByte(0)
		canonical.WriteString(state.LogicalRefs[name])
	}
	return []byte(canonical.String())
}

func (engine *Engine) recoverDecoded(repositoryURL, destination string, decoded authenticatedSnapshot) error {
	if destination == "" {
		destination = defaultDestination(repositoryURL)
	}
	return localstate.PublishDirectory(destination, func(temporary string) error {
		state := gitdb.State{LogicalHEAD: decoded.Repository.LogicalHEAD, ObjectFormat: decoded.Repository.ObjectFormat, LogicalRefs: decoded.Repository.LogicalRefs}
		if err := gitdb.Restore(temporary, false, state, decoded.Packs); err != nil {
			return err
		}
		if _, err := git(temporary, nil, "remote", "add", "origin", "cloak::"+repositoryURL); err != nil {
			return fmt.Errorf("configure recovered origin: %w", err)
		}
		if err := recordRecoveredCheckpoint(temporary, decoded); err != nil {
			return err
		}
		return nil
	})
}

func recordRecoveredCheckpoint(repositoryDirectory string, decoded authenticatedSnapshot) error {
	return localstate.ObserveCheckpoint(filepath.Join(repositoryDirectory, ".git"), decoded.Repository.RepositoryID,
		decoded.Repository.Generation, decoded.StorageCommitID, decoded.Repository.PreviousStorageRef, nil)
}

func makeEmptyBare(destination string, repository cloakformat.SnapshotState) (string, error) {
	branch := strings.TrimPrefix(string(repository.LogicalHEAD), "refs/heads/")
	command := exec.Command("git", "init", "--bare", "--object-format="+repository.ObjectFormat, "-b", branch, destination)
	if output, err := command.CombinedOutput(); err != nil {
		return "", fmt.Errorf("initialize empty Logical Repository: %s", strings.TrimSpace(string(output)))
	}
	return destination, nil
}

func defaultDestination(repositoryURL string) string {
	base := filepath.Base(strings.TrimSuffix(repositoryURL, "/"))
	return strings.TrimSuffix(base, ".git")
}

func validStorageCommitID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func git(directory string, stdin []byte, arguments ...string) ([]byte, error) {
	command := exec.Command("git", arguments...)
	command.Dir = directory
	command.Env = append(cleanGitEnvironment(os.Environ()), "GIT_CONFIG_NOSYSTEM=1")
	if stdin != nil {
		command.Stdin = strings.NewReader(string(stdin))
	}
	output, err := command.Output()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("git %s: %s", strings.Join(arguments, " "), strings.TrimSpace(string(exitError.Stderr)))
		}
		return nil, err
	}
	return output, nil
}

func cleanGitEnvironment(environment []string) []string {
	blocked := map[string]struct{}{
		"GIT_DIR":                          {},
		"GIT_WORK_TREE":                    {},
		"GIT_INDEX_FILE":                   {},
		"GIT_OBJECT_DIRECTORY":             {},
		"GIT_ALTERNATE_OBJECT_DIRECTORIES": {},
	}
	clean := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if _, found := blocked[name]; !found {
			clean = append(clean, entry)
		}
	}
	return clean
}
