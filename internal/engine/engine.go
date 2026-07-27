// Package engine coordinates repository-level operations behind command frontends.
package engine

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/txchen/git-remote-cloak/internal/domain"
	cloakformat "github.com/txchen/git-remote-cloak/internal/format"
	"github.com/txchen/git-remote-cloak/internal/gitdb"
	"github.com/txchen/git-remote-cloak/internal/localstate"
	"github.com/txchen/git-remote-cloak/internal/storage"
)

// Engine is the repository-level interface shared by human and Git adapters.
type Engine struct {
	formats *cloakformat.Registry
}

// New returns a production Repository Engine.
func New() *Engine { return &Engine{formats: cloakformat.NewRegistry()} }

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
	transport, err := storage.OpenLocalBare(repositoryURL)
	if err != nil {
		return err
	}
	refs, err := transport.Refs()
	if err != nil {
		return err
	}
	if len(refs) > 0 && (configuredErr != nil || !hasConfiguredRepositoryID) {
		return errors.New("existing Ciphertext Repository requires a matching configured remote and Repository ID")
	}
	var repository cloakformat.EmptyRepository
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
		if err := transport.PublishEmpty(encoded.Bootstrap, encoded.Manifest, encoded.ManifestLocator); err != nil {
			return err
		}
	case len(refs) == 1 && refs[0] == storage.StorageRef:
		snapshot, err := transport.Read()
		if err != nil {
			return err
		}
		decoded, err := engine.decodeSnapshot(secret, snapshot)
		if err != nil {
			return errors.New("existing Ciphertext Repository has another Cloak identity")
		}
		repository = cloakformat.EmptyRepository{
			RepositoryID: decoded.Repository.RepositoryID, LogicalHEAD: decoded.Repository.LogicalHEAD,
			ObjectFormat: decoded.Repository.ObjectFormat,
		}
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
	return nil
}

// RecoverEmpty atomically creates an ordinary empty Recovered Repository.
func (engine *Engine) RecoverEmpty(repositoryURL, destination string, secret domain.RecoverySecret) error {
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
		return nil
	})
}

// Recover atomically creates and validates a complete Recovered Repository.
func (engine *Engine) Recover(repositoryURL, destination string, secret domain.RecoverySecret) error {
	return engine.RecoverEmpty(repositoryURL, destination, secret)
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
		if err := gitdb.RestoreForClone(temporary, state, decoded.Packs); err != nil {
			return err
		}
		if _, err := git(temporary, nil, "remote", "add", "origin", "cloak::"+repositoryURL); err != nil {
			return fmt.Errorf("configure recovered origin: %w", err)
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

// Publish reads one protected branch from a bare Logical Repository and atomically publishes it.
func (engine *Engine) Publish(repositoryURL, logicalGitDirectory string, secret domain.RecoverySecret) error {
	transport, err := storage.OpenLocalBare(repositoryURL)
	if err != nil {
		return err
	}
	snapshot, err := transport.Read()
	if err != nil {
		return err
	}
	current, err := engine.decodeSnapshot(secret, snapshot)
	if err != nil {
		return err
	}
	if len(current.Repository.LogicalRefs) != 0 {
		return errors.New("ticket #11 supports only the ordinary first push")
	}
	state, err := gitdb.ReadState(logicalGitDirectory)
	if err != nil {
		return err
	}
	if state.LogicalHEAD != current.Repository.LogicalHEAD || state.ObjectFormat != current.Repository.ObjectFormat {
		return errors.New("pushed Logical Repository identity does not match initialized Ciphertext Repository")
	}
	payload, err := gitdb.CreatePack(logicalGitDirectory)
	if err != nil {
		return err
	}
	repository := cloakformat.Repository{
		RepositoryID: current.Repository.RepositoryID, Generation: current.Repository.Generation + 1,
		LogicalHEAD: state.LogicalHEAD, ObjectFormat: state.ObjectFormat, LogicalRefs: state.LogicalRefs,
		PreviousStorageRef: snapshot.StorageID,
	}
	encoded, err := engine.formats.EncodeSnapshot(secret, cloakformat.SnapshotInput{
		Repository: repository, Packs: []cloakformat.PackPayload{payload},
	})
	if err != nil {
		return err
	}
	// Validate the exact candidate logical state before uploading any ciphertext.
	candidate, err := engine.formats.DecodeSnapshot(secret, encoded.Bootstrap, encoded.Objects)
	if err != nil {
		return fmt.Errorf("validate candidate Ciphertext Snapshot: %w", err)
	}
	temporaryRoot, err := os.MkdirTemp("", "git-remote-cloak-candidate-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporaryRoot)
	temporary := filepath.Join(temporaryRoot, "repository.git")
	if err := gitdb.Restore(temporary, true, state, candidate.Packs); err != nil {
		return fmt.Errorf("validate candidate Logical Repository: %w", err)
	}
	_, err = transport.PublishSnapshot(snapshot.StorageID, encoded.Bootstrap, encoded.Objects)
	return err
}

// FetchInto imports authenticated native packs into an existing Git object database.
func (engine *Engine) FetchInto(repositoryURL, gitDirectory string, secret domain.RecoverySecret) error {
	decoded, err := engine.readSnapshot(repositoryURL, secret)
	if err != nil {
		return err
	}
	return gitdb.Import(gitDirectory, decoded.Packs)
}

// PublishRef applies the ticket #11 direct remote-helper push to one protected branch.
func (engine *Engine) PublishRef(repositoryURL, sourceGitDirectory, sourceRef, destinationRef string, force bool, secret domain.RecoverySecret) error {
	if !strings.HasPrefix(sourceRef, "refs/") || !strings.HasPrefix(destinationRef, "refs/heads/") {
		return errors.New("ticket #11 requires one branch-to-branch push")
	}
	temporaryRoot, err := os.MkdirTemp("", "git-remote-cloak-receive-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporaryRoot)
	temporary := filepath.Join(temporaryRoot, "repository.git")
	if err := engine.RecoverBare(repositoryURL, temporary, secret); err != nil {
		return err
	}
	refspec := sourceRef + ":" + destinationRef
	if force {
		refspec = "+" + refspec
	}
	command := exec.Command("git", "--git-dir="+temporary, "fetch", "--no-tags", sourceGitDirectory, refspec)
	command.Env = append(cleanGitEnvironment(os.Environ()), "GIT_CONFIG_NOSYSTEM=1")
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("receive pushed Logical Ref: %s", strings.TrimSpace(string(output)))
	}
	return engine.Publish(repositoryURL, temporary, secret)
}

// InspectEmpty authenticates and returns the logical state for a remote-helper adapter.
func (engine *Engine) InspectEmpty(repositoryURL string, secret domain.RecoverySecret) (cloakformat.EmptyRepository, error) {
	transport, err := storage.OpenLocalBare(repositoryURL)
	if err != nil {
		return cloakformat.EmptyRepository{}, err
	}
	snapshot, err := transport.Read()
	if err != nil {
		return cloakformat.EmptyRepository{}, err
	}
	return engine.formats.DecodeEmpty(secret, snapshot.Bootstrap, snapshot.Manifest)
}

// Inspect authenticates and returns the current logical state.
func (engine *Engine) Inspect(repositoryURL string, secret domain.RecoverySecret) (cloakformat.Repository, error) {
	decoded, err := engine.readSnapshot(repositoryURL, secret)
	return decoded.Repository, err
}

func (engine *Engine) readSnapshot(repositoryURL string, secret domain.RecoverySecret) (cloakformat.DecodedSnapshot, error) {
	transport, err := storage.OpenLocalBare(repositoryURL)
	if err != nil {
		return cloakformat.DecodedSnapshot{}, err
	}
	refs, err := transport.Refs()
	if err != nil {
		return cloakformat.DecodedSnapshot{}, err
	}
	if len(refs) != 1 || refs[0] != storage.StorageRef {
		return cloakformat.DecodedSnapshot{}, errors.New("Repository Host does not expose exactly one Storage Ref")
	}
	snapshot, err := transport.Read()
	if err != nil {
		return cloakformat.DecodedSnapshot{}, err
	}
	return engine.decodeSnapshot(secret, snapshot)
}

func (engine *Engine) decodeSnapshot(secret domain.RecoverySecret, snapshot storage.Snapshot) (cloakformat.DecodedSnapshot, error) {
	if snapshot.Manifest != nil {
		empty, err := engine.formats.DecodeEmpty(secret, snapshot.Bootstrap, snapshot.Manifest)
		if err == nil {
			return cloakformat.DecodedSnapshot{Repository: cloakformat.Repository{
				RepositoryID: empty.RepositoryID, Generation: 1, LogicalHEAD: empty.LogicalHEAD,
				ObjectFormat: empty.ObjectFormat, LogicalRefs: map[string]string{},
			}}, nil
		}
	}
	return engine.formats.DecodeSnapshot(secret, snapshot.Bootstrap, snapshot.Objects)
}

func (engine *Engine) recoverDecoded(repositoryURL, destination string, decoded cloakformat.DecodedSnapshot) error {
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
		return nil
	})
}

func makeEmptyBare(destination string, repository cloakformat.Repository) (string, error) {
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
