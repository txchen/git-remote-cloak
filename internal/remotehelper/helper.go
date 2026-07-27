// Package remotehelper adapts Git's remote-helper protocol to the Repository Engine.
package remotehelper

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/txchen/git-remote-cloak/internal/domain"
	"github.com/txchen/git-remote-cloak/internal/engine"
)

// Run serves one non-interactive Git remote-helper session.
func Run(repositoryURL string, recoverySecret domain.RecoverySecret, input io.Reader, output io.Writer) error {
	repositoryEngine := engine.New()
	repository, err := repositoryEngine.Inspect(repositoryURL, recoverySecret)
	if err != nil {
		return err
	}
	if len(repository.LogicalRefs) == 0 {
		if err := secureEmptyGitCloneScaffold(); err != nil {
			return err
		}
	} else {
		if err := atomicallyRecoverGitClone(repositoryEngine, repositoryURL, recoverySecret); err != nil {
			return err
		}
	}
	if err := restoreMissingLogicalHEAD(repository.LogicalHEAD); err != nil {
		return err
	}
	reader := bufio.NewScanner(input)
	writer := bufio.NewWriter(output)
	inFetchBatch := false
	pushes := make([]engine.RefUpdate, 0, 1)
	for reader.Scan() {
		command := reader.Text()
		switch {
		case command == "capabilities":
			if _, err := fmt.Fprint(writer, "option\nfetch\npush\nobject-format\n\n"); err != nil {
				return err
			}
		case command == "list" || command == "list for-push":
			if _, err := fmt.Fprintf(writer, ":object-format %s\n", repository.ObjectFormat); err != nil {
				return err
			}
			refNames := make([]string, 0, len(repository.LogicalRefs))
			for name := range repository.LogicalRefs {
				refNames = append(refNames, name)
			}
			sort.Strings(refNames)
			for _, name := range refNames {
				if _, err := fmt.Fprintf(writer, "%s %s\n", repository.LogicalRefs[name], name); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintf(writer, "@%s HEAD\n\n", repository.LogicalHEAD); err != nil {
				return err
			}
		case strings.HasPrefix(command, "option "):
			if _, err := fmt.Fprint(writer, "unsupported\n"); err != nil {
				return err
			}
		case command == "" && inFetchBatch:
			inFetchBatch = false
			gitDirectory, err := currentGitDirectory()
			if err != nil {
				return err
			}
			if err := repositoryEngine.FetchInto(repositoryURL, gitDirectory, recoverySecret); err != nil {
				return err
			}
			if err := restoreLogicalHEAD(repository.LogicalHEAD); err != nil {
				return err
			}
			if _, err := fmt.Fprint(writer, "\n"); err != nil {
				return err
			}
		case command == "" && len(pushes) > 0:
			gitDirectory, err := currentGitDirectory()
			if err == nil {
				err = repositoryEngine.PublishRefs(repositoryURL, gitDirectory, pushes, recoverySecret)
			}
			if err != nil {
				for _, push := range pushes {
					if _, writeErr := fmt.Fprintf(writer, "error %s %s\n", push.Destination, singleLine(err.Error())); writeErr != nil {
						return writeErr
					}
				}
				_, _ = fmt.Fprint(writer, "\n")
				return writer.Flush()
			}
			for _, push := range pushes {
				if _, err := fmt.Fprintf(writer, "ok %s\n", push.Destination); err != nil {
					return err
				}
			}
			_, _ = fmt.Fprint(writer, "\n")
			return writer.Flush()
		case command == "":
			if len(repository.LogicalRefs) == 0 {
				if err := atomicallyRecoverGitClone(repositoryEngine, repositoryURL, recoverySecret); err != nil {
					return err
				}
			}
			return nil
		case strings.HasPrefix(command, "fetch "):
			inFetchBatch = true
		case strings.HasPrefix(command, "push "):
			push, err := parseRefUpdate(strings.TrimPrefix(command, "push "))
			if err != nil {
				return err
			}
			pushes = append(pushes, push)
		default:
			return fmt.Errorf("unsupported remote-helper command")
		}
		if err := writer.Flush(); err != nil {
			return err
		}
	}
	return reader.Err()
}

func secureEmptyGitCloneScaffold() error {
	gitDirectory, explicitlySet := os.LookupEnv("GIT_DIR")
	if !explicitlySet {
		return nil
	}
	absoluteGitDirectory, err := filepath.Abs(gitDirectory)
	if err != nil || filepath.Base(absoluteGitDirectory) != ".git" {
		return err
	}
	destination := filepath.Dir(absoluteGitDirectory)
	entries, err := os.ReadDir(destination)
	if err != nil {
		return fmt.Errorf("inspect empty Git clone scaffold: %w", err)
	}
	if len(entries) == 1 && entries[0].Name() == ".git" && entries[0].IsDir() {
		if err := os.Chmod(destination, 0o700); err != nil {
			return fmt.Errorf("secure empty Git clone scaffold: %w", err)
		}
	}
	return nil
}

func parseRefUpdate(refspec string) (engine.RefUpdate, error) {
	push := engine.RefUpdate{}
	if strings.HasPrefix(refspec, "+") {
		push.Force = true
		refspec = strings.TrimPrefix(refspec, "+")
	}
	var found bool
	source, destination, found := strings.Cut(refspec, ":")
	push.Source = domain.LogicalRefName(source)
	push.Destination = domain.LogicalRefName(destination)
	if !found || push.Destination == "" {
		return engine.RefUpdate{}, errors.New("push refspec requires a destination")
	}
	return push, nil
}

func currentGitDirectory() (string, error) {
	if configured, found := os.LookupEnv("GIT_DIR"); found {
		absolute, err := filepath.Abs(configured)
		if err != nil {
			return "", err
		}
		return absolute, nil
	}
	command := exec.Command("git", "rev-parse", "--absolute-git-dir")
	output, err := command.Output()
	if err != nil {
		return "", errors.New("remote helper could not locate the local Git object database")
	}
	return strings.TrimSpace(string(output)), nil
}

func singleLine(message string) string {
	return strings.Join(strings.Fields(message), " ")
}

func atomicallyRecoverGitClone(repositoryEngine *engine.Engine, repositoryURL string, recoverySecret domain.RecoverySecret) error {
	gitDirectory, explicitlySet := os.LookupEnv("GIT_DIR")
	if !explicitlySet {
		return nil
	}
	absoluteGitDirectory, err := filepath.Abs(gitDirectory)
	if err != nil {
		return err
	}
	if filepath.Base(absoluteGitDirectory) != ".git" {
		return nil
	}
	destination := filepath.Dir(absoluteGitDirectory)
	entries, err := os.ReadDir(destination)
	if err != nil {
		return fmt.Errorf("inspect Git clone scaffold: %w", err)
	}
	if len(entries) != 1 || entries[0].Name() != ".git" || !entries[0].IsDir() {
		return nil
	}
	parent := filepath.Dir(destination)
	scaffold, err := os.MkdirTemp(parent, ".cloak-git-scaffold-")
	if err != nil {
		return fmt.Errorf("reserve Git clone scaffold path: %w", err)
	}
	if err := os.Remove(scaffold); err != nil {
		return fmt.Errorf("prepare Git clone scaffold path: %w", err)
	}
	if err := os.Rename(destination, scaffold); err != nil {
		return fmt.Errorf("isolate Git clone scaffold: %w", err)
	}
	restoreScaffold := true
	defer func() {
		if restoreScaffold {
			_ = os.Rename(scaffold, destination)
		}
	}()
	if err := repositoryEngine.RecoverForGitClone(repositoryURL, destination, recoverySecret); err != nil {
		return err
	}
	restoreScaffold = false
	if err := os.RemoveAll(scaffold); err != nil {
		return fmt.Errorf("remove replaced Git clone scaffold: %w", err)
	}
	return nil
}

func restoreMissingLogicalHEAD(logicalHEAD domain.LogicalHEAD) error {
	probe := exec.Command("git", "symbolic-ref", "-q", "HEAD")
	probe.Stderr = io.Discard
	if err := probe.Run(); err == nil {
		return nil
	}
	return restoreLogicalHEAD(logicalHEAD)
}

func restoreLogicalHEAD(logicalHEAD domain.LogicalHEAD) error {
	command := exec.Command("git", "symbolic-ref", "HEAD", string(logicalHEAD))
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("restore Logical HEAD: %s", strings.TrimSpace(string(output)))
	}
	return nil
}
