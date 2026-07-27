package acceptance_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestInteractiveInitDisplaysMnemonicOnceAndRequiresConfirmation(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	repositoryHost := filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", repositoryHost)
	mustGit(t, root, "init", "-b", "main", workspace)

	command := exec.Command(binary, "init", "backup", repositoryHost)
	command.Dir = workspace
	command.Env = withoutEnvironment(os.Environ(), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE")
	terminal, err := pty.Start(command)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	transcript := readPTYUntil(t, terminal, "type SAVED:")
	if strings.Count(transcript, "cloak-v1:") != 1 {
		t.Fatalf("Recovery Mnemonic appeared %d times before confirmation:\n%s", strings.Count(transcript, "cloak-v1:"), transcript)
	}
	mnemonicPattern := regexp.MustCompile(`cloak-v1:(?:[a-z]+ ){23}[a-z]+`)
	mnemonic := mnemonicPattern.FindString(transcript)
	if mnemonic == "" {
		t.Fatalf("no 24-word Recovery Mnemonic in output:\n%s", transcript)
	}
	if _, err := terminal.WriteString("SAVED\n"); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("interactive init failed: %v\n%s", err, transcript)
	}
	if got := mustGit(t, repositoryHost, "for-each-ref", "--format=%(refname)"); got != "refs/heads/cloak-storage\n" {
		t.Fatalf("Repository Host refs = %q", got)
	}

	recovered := filepath.Join(root, "recovered")
	clone := exec.Command(binary, "clone", repositoryHost, recovered)
	clone.Env = append(withoutEnvironment(os.Environ(), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE"), "CLOAK_RECOVERY_SECRET="+mnemonic)
	if output, err := clone.CombinedOutput(); err != nil {
		t.Fatalf("generated Recovery Mnemonic could not recover repository: %v\n%s", err, output)
	}
}

func TestInitAcceptsSecretFileSourcesAndWarnsForBroadPermissions(t *testing.T) {
	for _, test := range []struct {
		name      string
		arguments func(string, string) []string
		environ   func(string) []string
	}{
		{
			name:      "environment-named file",
			arguments: func(workspace, host string) []string { return []string{"init", "backup", host} },
			environ:   func(path string) []string { return []string{"CLOAK_RECOVERY_SECRET_FILE=" + path} },
		},
		{
			name: "explicit file",
			arguments: func(workspace, host string) []string {
				return []string{"init", "backup", host, "--secret-file", filepath.Join(workspace, "secret")}
			},
			environ: func(path string) []string { return nil },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			binary := buildBinary(t)
			root := t.TempDir()
			workspace := filepath.Join(root, "workspace")
			host := filepath.Join(root, "host.git")
			mustGit(t, root, "init", "--bare", host)
			mustGit(t, root, "init", "-b", "main", workspace)
			secretPath := filepath.Join(workspace, "secret")
			if err := os.WriteFile(secretPath, []byte(testMnemonic+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(binary, test.arguments(workspace, host)...)
			command.Dir = workspace
			command.Env = append(withoutEnvironment(os.Environ(), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE"), test.environ(secretPath)...)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("init failed: %v\n%s", err, output)
			}
			if !bytes.Contains(output, []byte("warning: Recovery Secret file is readable by group or others")) {
				t.Fatalf("missing broad-permission warning:\n%s", output)
			}
			if bytes.Contains(output, []byte(testMnemonic)) {
				t.Fatal("command disclosed Recovery Mnemonic")
			}
		})
	}
}

func TestNonInteractiveInitRejectsMissingAmbiguousAndLiteralSecretsWithoutMutation(t *testing.T) {
	for _, test := range []struct {
		name      string
		arguments func(string) []string
		environ   []string
	}{
		{name: "missing", arguments: func(host string) []string { return []string{"init", "backup", host} }},
		{
			name: "ambiguous",
			arguments: func(host string) []string {
				return []string{"init", "backup", host, "--secret-file", filepath.Join(filepath.Dir(host), "secret")}
			},
			environ: []string{"CLOAK_RECOVERY_SECRET=" + testMnemonic},
		},
		{name: "literal", arguments: func(host string) []string { return []string{"init", "backup", host, "--secret", testMnemonic} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			binary := buildBinary(t)
			root := t.TempDir()
			workspace := filepath.Join(root, "workspace")
			host := filepath.Join(root, "host.git")
			mustGit(t, root, "init", "--bare", host)
			mustGit(t, root, "init", "-b", "main", workspace)
			if err := os.WriteFile(filepath.Join(root, "secret"), []byte(testMnemonic), 0o600); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(binary, test.arguments(host)...)
			command.Dir = workspace
			command.Stdin = strings.NewReader("")
			command.Env = append(withoutEnvironment(os.Environ(), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE"), test.environ...)
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("init unexpectedly succeeded:\n%s", output)
			}
			if bytes.Contains(output, []byte(testMnemonic)) {
				t.Fatal("error disclosed Recovery Mnemonic")
			}
			if got := mustGit(t, host, "for-each-ref", "--format=%(refname)"); got != "" {
				t.Fatalf("rejected init mutated Repository Host refs: %q", got)
			}
		})
	}
}

func TestInitRejectsInvalidSecretInputsWithoutMutation(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(string) (arguments []string, environment []string)
	}{
		{
			name: "invalid mnemonic",
			prepare: func(host string) ([]string, []string) {
				return []string{"init", "backup", host}, []string{"CLOAK_RECOVERY_SECRET=cloak-v1:not a valid mnemonic"}
			},
		},
		{
			name: "empty file",
			prepare: func(host string) ([]string, []string) {
				path := filepath.Join(filepath.Dir(host), "empty-secret")
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					panic(err)
				}
				return []string{"init", "backup", host, "--secret-file", path}, nil
			},
		},
		{
			name: "directory",
			prepare: func(host string) ([]string, []string) {
				path := filepath.Join(filepath.Dir(host), "secret-directory")
				if err := os.Mkdir(path, 0o700); err != nil {
					panic(err)
				}
				return []string{"init", "backup", host, "--secret-file", path}, nil
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			binary := buildBinary(t)
			root := t.TempDir()
			workspace := filepath.Join(root, "workspace")
			host := filepath.Join(root, "host.git")
			mustGit(t, root, "init", "--bare", host)
			mustGit(t, root, "init", "-b", "main", workspace)
			arguments, environment := test.prepare(host)
			command := exec.Command(binary, arguments...)
			command.Dir = workspace
			command.Env = append(withoutEnvironment(os.Environ(), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE"), environment...)
			if output, err := command.CombinedOutput(); err == nil {
				t.Fatalf("init unexpectedly succeeded:\n%s", output)
			}
			if got := mustGit(t, host, "for-each-ref", "--format=%(refname)"); got != "" {
				t.Fatalf("invalid secret input mutated Repository Host: %q", got)
			}
		})
	}
}

func readPTYUntil(t *testing.T, terminal *os.File, marker string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var output strings.Builder
	buffer := make([]byte, 512)
	for !strings.Contains(output.String(), marker) {
		if err := terminal.SetReadDeadline(deadline); err != nil {
			t.Fatal(err)
		}
		count, err := terminal.Read(buffer)
		if count > 0 {
			output.Write(buffer[:count])
		}
		if err != nil {
			t.Fatalf("reading interactive output: %v\n%s", err, output.String())
		}
	}
	return output.String()
}
