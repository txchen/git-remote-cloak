// Package remotehelper adapts Git's remote-helper protocol to the Repository Engine.
package remotehelper

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/txchen/git-remote-cloak/internal/domain"
	"github.com/txchen/git-remote-cloak/internal/engine"
)

// Run serves one non-interactive Git remote-helper session.
func Run(repositoryURL string, recoverySecret domain.RecoverySecret, input io.Reader, output io.Writer) error {
	repositoryEngine := engine.New()
	repository, err := repositoryEngine.InspectEmpty(repositoryURL, recoverySecret)
	if err != nil {
		return err
	}
	if err := atomicallyRecoverGitClone(repositoryEngine, repositoryURL, recoverySecret); err != nil {
		return err
	}
	if err := restoreMissingLogicalHEAD(repository.LogicalHEAD); err != nil {
		return err
	}
	reader := bufio.NewScanner(input)
	writer := bufio.NewWriter(output)
	inFetchBatch := false
	gitProtocol := ""
	for reader.Scan() {
		command := reader.Text()
		switch {
		case command == "capabilities":
			if _, err := fmt.Fprint(writer, "option\nstateless-connect\n\n"); err != nil {
				return err
			}
		case command == "list" || command == "list for-push":
			if _, err := fmt.Fprintf(writer, "@%s HEAD\n\n", repository.LogicalHEAD); err != nil {
				return err
			}
		case strings.HasPrefix(command, "option git-protocol "):
			gitProtocol = strings.TrimPrefix(command, "option git-protocol ")
			if _, err := fmt.Fprint(writer, "ok\n"); err != nil {
				return err
			}
		case strings.HasPrefix(command, "option "):
			if _, err := fmt.Fprint(writer, "unsupported\n"); err != nil {
				return err
			}
		case command == "" && inFetchBatch:
			inFetchBatch = false
			if err := restoreLogicalHEAD(repository.LogicalHEAD); err != nil {
				return err
			}
			if _, err := fmt.Fprint(writer, "\n"); err != nil {
				return err
			}
		case command == "":
			return nil
		case strings.HasPrefix(command, "fetch "):
			inFetchBatch = true
		case command == "connect git-upload-pack":
			if _, err := fmt.Fprint(writer, "\n"); err != nil {
				return err
			}
			if err := writer.Flush(); err != nil {
				return err
			}
			return serveEmptyUploadPack(repository.LogicalHEAD, repository.ObjectFormat, gitProtocol, input, output)
		case command == "stateless-connect git-upload-pack":
			if _, err := fmt.Fprint(writer, "\n"); err != nil {
				return err
			}
			if err := writer.Flush(); err != nil {
				return err
			}
			return serveEmptyStatelessUploadPack(repository.LogicalHEAD, repository.ObjectFormat, input, output)
		default:
			return fmt.Errorf("unsupported remote-helper command")
		}
		if err := writer.Flush(); err != nil {
			return err
		}
	}
	return reader.Err()
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
	if err := repositoryEngine.RecoverEmpty(repositoryURL, destination, recoverySecret); err != nil {
		return err
	}
	restoreScaffold = false
	if err := os.RemoveAll(scaffold); err != nil {
		return fmt.Errorf("remove replaced Git clone scaffold: %w", err)
	}
	return nil
}

func serveEmptyStatelessUploadPack(logicalHEAD domain.LogicalHEAD, objectFormat string, input io.Reader, output io.Writer) error {
	temporary, err := makeEmptyBareRepository(logicalHEAD, objectFormat)
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	advertise := exec.Command("git", "upload-pack", "--advertise-refs", temporary)
	advertise.Env = append(os.Environ(), "GIT_PROTOCOL=version=2")
	advertisement, err := advertise.Output()
	if err != nil {
		return fmt.Errorf("advertise empty Recovered Repository: %w", err)
	}
	if _, err := output.Write(advertisement); err != nil {
		return err
	}
	for {
		request, err := readPacketMessage(input)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		uploadPack := exec.Command("git", "upload-pack", "--stateless-rpc", temporary)
		uploadPack.Env = append(os.Environ(), "GIT_PROTOCOL=version=2")
		uploadPack.Stdin = bytes.NewReader(request)
		response, err := uploadPack.Output()
		if err != nil {
			return fmt.Errorf("serve empty Recovered Repository request: %w", err)
		}
		if _, err := output.Write(response); err != nil {
			return err
		}
		if _, err := io.WriteString(output, "0002"); err != nil {
			return err
		}
	}
}

func readPacketMessage(input io.Reader) ([]byte, error) {
	var message bytes.Buffer
	header := make([]byte, 4)
	for {
		if _, err := io.ReadFull(input, header); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil, io.EOF
			}
			return nil, err
		}
		message.Write(header)
		if string(header) == "0000" {
			return message.Bytes(), nil
		}
		if string(header) == "0001" || string(header) == "0002" {
			continue
		}
		var packetLength int
		if _, err := fmt.Sscanf(string(header), "%04x", &packetLength); err != nil || packetLength < 4 || packetLength > 65520 {
			return nil, fmt.Errorf("invalid packet line from Git")
		}
		payload := make([]byte, packetLength-4)
		if _, err := io.ReadFull(input, payload); err != nil {
			return nil, err
		}
		message.Write(payload)
	}
}

func serveEmptyUploadPack(logicalHEAD domain.LogicalHEAD, objectFormat, gitProtocol string, input io.Reader, output io.Writer) error {
	temporary, err := makeEmptyBareRepository(logicalHEAD, objectFormat)
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	uploadPack := exec.Command("git", "upload-pack", temporary)
	if gitProtocol != "" {
		uploadPack.Env = append(os.Environ(), "GIT_PROTOCOL="+gitProtocol)
	}
	uploadPack.Stdin = input
	uploadPack.Stdout = output
	uploadPack.Stderr = os.Stderr
	if err := uploadPack.Run(); err != nil {
		return fmt.Errorf("serve temporary Recovered Repository: %w", err)
	}
	return nil
}

func makeEmptyBareRepository(logicalHEAD domain.LogicalHEAD, objectFormat string) (string, error) {
	temporary, err := os.MkdirTemp("", "git-remote-cloak-upload-pack-")
	if err != nil {
		return "", fmt.Errorf("create temporary Recovered Repository: %w", err)
	}
	if err := os.Chmod(temporary, 0o700); err != nil {
		os.RemoveAll(temporary)
		return "", err
	}
	branch := strings.TrimPrefix(string(logicalHEAD), "refs/heads/")
	initialize := exec.Command("git", "init", "--bare", "--object-format="+objectFormat, "-b", branch, temporary)
	if result, err := initialize.CombinedOutput(); err != nil {
		os.RemoveAll(temporary)
		return "", fmt.Errorf("initialize temporary Recovered Repository: %s", strings.TrimSpace(string(result)))
	}
	return temporary, nil
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
