package acceptance_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const testMnemonic = "cloak-v1:abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon art"

func TestOwnerInitializesAndRecoversEmptyCiphertextRepository(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	repositoryHost := filepath.Join(root, "host.git")
	recovered := filepath.Join(root, "recovered")
	mustGit(t, root, "init", "--bare", repositoryHost)
	mustGit(t, root, "init", "-b", "main", workspace)
	beforeHead := mustGit(t, workspace, "symbolic-ref", "HEAD")
	beforeObjects := mustGit(t, workspace, "count-objects", "-v")

	initCommand := exec.Command(binary, "init", "backup", repositoryHost)
	initCommand.Dir = workspace
	initCommand.Env = append(withoutEnvironment(os.Environ(), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE"), "CLOAK_RECOVERY_SECRET="+testMnemonic)
	output, err := initCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("init failed: %v\n%s", err, output)
	}
	if strings.Contains(string(output), testMnemonic) {
		t.Fatal("non-interactive init printed the Recovery Mnemonic")
	}

	if got := mustGit(t, repositoryHost, "for-each-ref", "--format=%(refname)"); got != "refs/heads/cloak-storage\n" {
		t.Fatalf("Repository Host refs = %q, want only Storage Ref", got)
	}
	if got := mustGit(t, workspace, "symbolic-ref", "HEAD"); got != beforeHead {
		t.Fatalf("Logical HEAD changed from %q to %q", beforeHead, got)
	}
	if got := mustGit(t, workspace, "count-objects", "-v"); got != beforeObjects {
		t.Fatalf("local Git object state changed:\nbefore: %s\nafter: %s", beforeObjects, got)
	}
	if got := mustGit(t, workspace, "remote", "get-url", "backup"); got != "cloak::"+repositoryHost+"\n" {
		t.Fatalf("configured remote URL = %q", got)
	}

	cloneCommand := exec.Command(binary, "clone", repositoryHost, recovered)
	cloneCommand.Env = append(withoutEnvironment(os.Environ(), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE"), "CLOAK_RECOVERY_SECRET="+testMnemonic)
	output, err = cloneCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("clone failed: %v\n%s", err, output)
	}
	if got := mustGit(t, recovered, "remote", "get-url", "origin"); got != "cloak::"+repositoryHost+"\n" {
		t.Fatalf("recovered origin URL = %q", got)
	}
	if got := mustGit(t, recovered, "symbolic-ref", "HEAD"); got != "refs/heads/main\n" {
		t.Fatalf("recovered Logical HEAD = %q", got)
	}
	if got := mustGit(t, recovered, "for-each-ref", "--format=%(refname)"); got != "" {
		t.Fatalf("recovered empty repository has refs: %q", got)
	}
	info, err := os.Stat(recovered)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("recovered repository permissions = %o, want 700", info.Mode().Perm())
	}
}

func TestGitCloneThroughRemoteHelperRecoversEmptyRepository(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	repositoryHost := filepath.Join(root, "host.git")
	recovered := filepath.Join(root, "recovered")
	mustGit(t, root, "init", "--bare", repositoryHost)
	mustGit(t, root, "init", "-b", "main", workspace)

	initCommand := exec.Command(binary, "init", "backup", repositoryHost)
	initCommand.Dir = workspace
	initCommand.Env = append(withoutEnvironment(os.Environ(), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE"), "CLOAK_RECOVERY_SECRET="+testMnemonic)
	if output, err := initCommand.CombinedOutput(); err != nil {
		t.Fatalf("init failed: %v\n%s", err, output)
	}

	cloneCommand := exec.Command("git", "clone", "cloak::"+repositoryHost, recovered)
	cloneCommand.Dir = root
	cloneCommand.Env = append(withoutEnvironment(os.Environ(), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE"),
		"CLOAK_RECOVERY_SECRET="+testMnemonic,
		"PATH="+filepath.Dir(binary)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GIT_CONFIG_NOSYSTEM=1",
	)
	output, err := cloneCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("git clone through remote helper failed: %v\n%s", err, output)
	}
	entries, readErr := os.ReadDir(recovered)
	if readErr != nil {
		t.Fatalf("successful git clone did not create destination: %v\n%s", readErr, output)
	}
	if len(entries) == 0 {
		t.Fatalf("successful git clone created an empty destination without .git:\n%s", output)
	}
	if _, err := os.Stat(filepath.Join(recovered, ".git")); err != nil {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("successful git clone destination entries = %v, missing .git: %v\n%s", names, err, output)
	}
	if got := mustGit(t, recovered, "remote", "get-url", "origin"); got != "cloak::"+repositoryHost+"\n" {
		t.Fatalf("recovered origin URL = %q", got)
	}
	if got := mustGit(t, recovered, "symbolic-ref", "HEAD"); got != "refs/heads/main\n" {
		t.Fatalf("recovered Logical HEAD = %q", got)
	}
	info, err := os.Stat(recovered)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("remote-helper recovered repository permissions = %o, want 700", info.Mode().Perm())
	}
}

func TestFailedCloneDoesNotExposePartialRepository(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	host := filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", workspace)
	mustInit(t, binary, workspace, host, testMnemonic)

	for _, test := range []struct {
		name    string
		command func(string) *exec.Cmd
	}{
		{
			name: "human command",
			command: func(destination string) *exec.Cmd {
				return exec.Command(binary, "clone", host, destination)
			},
		},
		{
			name: "remote helper",
			command: func(destination string) *exec.Cmd {
				command := exec.Command("git", "clone", "cloak::"+host, destination)
				command.Env = append(os.Environ(), "PATH="+filepath.Dir(binary)+string(os.PathListSeparator)+os.Getenv("PATH"))
				return command
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			destination := filepath.Join(root, strings.ReplaceAll(test.name, " ", "-"))
			command := test.command(destination)
			command.Dir = root
			command.Env = append(withoutEnvironment(command.Env, "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE"), "CLOAK_RECOVERY_SECRET="+otherMnemonic)
			if output, err := command.CombinedOutput(); err == nil {
				t.Fatalf("clone with wrong Recovery Secret succeeded:\n%s", output)
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatalf("failed clone left a partial destination: %v", err)
			}
		})
	}
}

func mustGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	command.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Cloak Test",
		"GIT_AUTHOR_EMAIL=cloak@example.invalid",
		"GIT_COMMITTER_NAME=Cloak Test",
		"GIT_COMMITTER_EMAIL=cloak@example.invalid",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return string(output)
}
