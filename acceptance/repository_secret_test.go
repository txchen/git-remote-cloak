package acceptance_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creack/pty"
)

func TestRepositorySecretFollowsWorkingDirectoryAndMove(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	owners := []string{filepath.Join(root, "owner-a"), filepath.Join(root, "owner-b")}
	hosts := []string{filepath.Join(root, "host-a.git"), filepath.Join(root, "host-b.git")}
	keys := []string{testMnemonic, otherMnemonic}
	for i, owner := range owners {
		mustGit(t, root, "init", "--bare", hosts[i])
		mustGit(t, root, "init", "-b", "main", owner)
		writeAndCommit(t, owner, "file", owner, "initial")
		file := filepath.Join(root, "external-secret")
		if err := os.WriteFile(file, []byte(keys[i]), 0600); err != nil {
			t.Fatal(err)
		}
		mustRunWithRepositorySecret(t, binary, owner, binary, "init", "backup", hosts[i], "--secret-file", file)
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
		assertSavedSecret(t, owner, keys[i])
		mustRunWithRepositorySecret(t, binary, owner, "git", "push", "backup", "main")
		if err := os.Mkdir(filepath.Join(owner, "sub"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, i := range []int{0, 1, 0} {
		mustRunWithRepositorySecret(t, binary, filepath.Join(owners[i], "sub"), "git", "fetch", "backup")
		mustRunWithRepositorySecret(t, binary, owners[i], binary, "doctor", hosts[i], "--json")
	}
	moved := filepath.Join(root, "moved")
	if err := os.Rename(owners[0], moved); err != nil {
		t.Fatal(err)
	}
	owners[0] = moved
	mustRunWithRepositorySecret(t, binary, moved, binary, "cache", "clear")
	assertSavedSecret(t, moved, keys[0])
	mustRunWithRepositorySecret(t, binary, moved, binary, "set-head", "backup", "main")
	mustRunWithRepositorySecret(t, binary, moved, binary, "compact", "backup")
	linked := filepath.Join(root, "linked")
	mustGit(t, moved, "worktree", "add", "-b", "linked", linked)
	mustRunWithRepositorySecret(t, binary, linked, "git", "push", "backup", "linked")
	gitClone := filepath.Join(root, "git-clone")
	mustCloakGit(t, binary, root, "clone", "cloak::"+hosts[0], gitClone)
	assertSavedSecret(t, gitClone, testMnemonic)
	mustRunWithRepositorySecret(t, binary, gitClone, "git", "fetch", "origin")
	// v0.1.x clones had a checkpoint, but no saved Secret or remote identity.
	if err := os.Remove(filepath.Join(gitClone, ".git", "cloak", "secret")); err != nil {
		t.Fatal(err)
	}
	commandUpgrade := repositorySecretCommand(binary, gitClone, binary, "init", "origin", hosts[0])
	commandUpgrade.Env = append(commandUpgrade.Env, "CLOAK_RECOVERY_SECRET="+testMnemonic)
	if out, err := commandUpgrade.CombinedOutput(); err != nil {
		t.Fatalf("upgrade old clone: %v %s", err, out)
	}
	assertSavedSecret(t, gitClone, testMnemonic)
	mustRunWithRepositorySecret(t, binary, gitClone, "git", "fetch", "origin")
	// Clone must not silently borrow the surrounding repository's credential.
	command := repositorySecretCommand(binary, moved, binary, "clone", hosts[1], filepath.Join(root, "wrong-clone"))
	if out, err := command.CombinedOutput(); err == nil || !strings.Contains(string(out), "Recovery Secret") {
		t.Fatalf("clone reused ambient key: %v %s", err, out)
	}
	command = repositorySecretCommand(binary, moved, binary, "rekey", "backup", "--yes")
	if out, err := command.CombinedOutput(); err == nil || !strings.Contains(string(out), "new Recovery Secret") {
		t.Fatalf("Rekey reused active key: %v %s", err, out)
	}
	// An explicit override takes precedence without replacing the managed file.
	command = repositorySecretCommand(binary, moved, "git", "ls-remote", "backup")
	command.Env = append(command.Env, "CLOAK_RECOVERY_SECRET="+otherMnemonic)
	if out, err := command.CombinedOutput(); err == nil {
		t.Fatalf("wrong override accepted: %s", out)
	}
	assertSavedSecret(t, moved, keys[0])
}

func TestInteractiveCloneSavesSecretWithoutEcho(t *testing.T) {
	binary, root, owner, host, _ := rekeyFixture(t)
	destination := filepath.Join(root, "recovered")
	command := repositorySecretCommand(binary, owner, binary, "clone", host, destination)
	terminal, err := pty.Start(command)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	transcript := readPTYUntil(t, terminal, "input hidden):")
	if _, err := terminal.WriteString(testMnemonic + "\n"); err != nil {
		t.Fatal(err)
	}
	transcript += readPTYUntil(t, terminal, "Recovered Logical Repository.")
	waitForInteractiveCommand(t, command, transcript)
	if strings.Contains(transcript, testMnemonic) {
		t.Fatal("clone echoed Recovery Mnemonic")
	}
	assertSavedSecret(t, destination, testMnemonic)
	mustRunWithRepositorySecret(t, binary, destination, "git", "fetch", "origin")
	writeAndCommit(t, destination, "more", "new", "update")
	mustRunWithRepositorySecret(t, binary, destination, "git", "push", "origin", "main")
}

func TestManagedSecretFailsClosed(t *testing.T) {
	for _, scenario := range []string{"corrupt", "permissions", "symlink", "directory-symlink"} {
		t.Run(scenario, func(t *testing.T) {
			binary, root, owner, host, _ := rekeyFixture(t)
			path := filepath.Join(owner, ".git", "cloak", "secret")
			switch scenario {
			case "corrupt":
				if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
					t.Fatal(err)
				}
			case "permissions":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(root, "target")
				if err := os.Rename(path, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "directory-symlink":
				directory := filepath.Dir(path)
				target := filepath.Join(root, "state")
				if err := os.Rename(directory, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, directory); err != nil {
					t.Fatal(err)
				}
			}
			before := mustGit(t, host, "rev-parse", "refs/heads/cloak-storage")
			for _, args := range [][]string{{"fetch", "backup"}, {"push", "backup", "main"}} {
				out, err := repositorySecretCommand(binary, owner, "git", args...).CombinedOutput()
				if err == nil || !strings.Contains(string(out), "Recovery Secret") {
					t.Fatalf("unsafe secret accepted: %v %s", err, out)
				}
			}
			assertStorageRef(t, host, strings.TrimSpace(before))
		})
	}
}

func TestRekeyPersistsAndRecoversManagedSecret(t *testing.T) {
	for _, fault := range []string{"", "before-storage-ref", "lost-process-before-storage-ref", "lost-process-after-storage-ref", "after-storage-ref", "stale-storage-ref"} {
		t.Run(fault, func(t *testing.T) {
			binary, _, owner, host, _ := rekeyFixture(t)
			command := repositorySecretCommand(binary, owner, binary, "rekey", "backup", "--yes")
			command.Env = append(command.Env, "CLOAK_RECOVERY_SECRET="+otherMnemonic, "CLOAK_TEST_FAULT="+fault)
			out, err := command.CombinedOutput()
			published := fault == "" || fault == "lost-process-after-storage-ref" || fault == "after-storage-ref"
			if (fault == "" || fault == "after-storage-ref") && err != nil {
				t.Fatalf("Rekey: %v %s", err, out)
			}
			if !published {
				assertSavedSecret(t, owner, testMnemonic)
			}
			// Daily use resolves lost publication responses, or keeps the old key if unpublished.
			if fault == "stale-storage-ref" {
				if out, err := repositorySecretCommand(binary, owner, "git", "fetch", "backup").CombinedOutput(); err == nil {
					t.Fatalf("substituted generation accepted: %s", out)
				}
			} else {
				mustRunWithRepositorySecret(t, binary, owner, "git", "fetch", "backup")
			}
			if !published {
				mustRunWithRepositorySecret(t, binary, owner, binary, "rekey", "backup", "--yes")
			}
			assertSavedSecret(t, owner, otherMnemonic)
			// Restart recovery must update public identity metadata as well.
			mustRunWithRepositorySecret(t, binary, owner, binary, "init", "backup", host)
			mustRunWithRepositorySecret(t, binary, owner, "git", "push", "backup", "main")
			mustRunWithRepositorySecret(t, binary, owner, binary, "doctor", host)
			if _, err := os.Stat(filepath.Join(owner, ".git", "cloak", "secret.pending")); !os.IsNotExist(err) {
				t.Fatalf("pending secret survived successful Rekey: %v", err)
			}
		})
	}
}

func assertSavedSecret(t *testing.T, repository, mnemonic string) {
	t.Helper()
	path := filepath.Join(repository, ".git", "cloak", "secret")
	contents, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(contents)) != mnemonic {
		t.Fatalf("managed secret mismatch: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("secret permissions: %v %v", info, err)
	}
	info, err = os.Stat(filepath.Dir(path))
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("secret directory permissions: %v %v", info, err)
	}
}

func repositorySecretCommand(binary, directory, program string, arguments ...string) *exec.Cmd {
	command := exec.Command(program, arguments...)
	command.Dir = directory
	command.Env = withoutEnvironment(cloakGitEnvironment(binary), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE")
	return command
}

func mustRunWithRepositorySecret(t *testing.T, binary, directory, program string, arguments ...string) string {
	t.Helper()
	output, err := repositorySecretCommand(binary, directory, program, arguments...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", program, arguments, err, output)
	}
	return string(output)
}
