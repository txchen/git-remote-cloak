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
		repository, err = engine.formats.DecodeEmpty(secret, snapshot.Bootstrap, snapshot.Manifest)
		if err != nil {
			return errors.New("existing Ciphertext Repository has another Cloak identity")
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
	transport, err := storage.OpenLocalBare(repositoryURL)
	if err != nil {
		return err
	}
	refs, err := transport.Refs()
	if err != nil {
		return err
	}
	if len(refs) != 1 || refs[0] != storage.StorageRef {
		return errors.New("Repository Host does not expose exactly one Storage Ref")
	}
	snapshot, err := transport.Read()
	if err != nil {
		return err
	}
	repository, err := engine.formats.DecodeEmpty(secret, snapshot.Bootstrap, snapshot.Manifest)
	if err != nil {
		return err
	}
	if destination == "" {
		destination = defaultDestination(repositoryURL)
	}
	return localstate.PublishDirectory(destination, func(temporary string) error {
		branch := strings.TrimPrefix(string(repository.LogicalHEAD), "refs/heads/")
		if _, err := git(filepath.Dir(temporary), nil, "init", "--object-format="+repository.ObjectFormat, "-b", branch, temporary); err != nil {
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
