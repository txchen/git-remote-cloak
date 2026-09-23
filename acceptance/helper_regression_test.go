package acceptance_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelperPreservesLocalHEAD(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	owner, host, recovered := filepath.Join(root, "owner"), filepath.Join(root, "host.git"), filepath.Join(root, "recovered")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", owner)
	writeAndCommit(t, owner, "base.txt", "base\n", "base")
	mustInit(t, binary, owner, host, testMnemonic)
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	mustCloakGit(t, binary, root, "clone", "cloak::"+host, recovered)
	mustGit(t, recovered, "switch", "-c", "feature")
	writeAndCommit(t, recovered, "local.txt", "local\n", "local")
	for _, detached := range []bool{false, true} {
		t.Run(map[bool]string{false: "branch", true: "detached"}[detached], func(t *testing.T) {
			mustGit(t, recovered, "switch", "feature")
			if detached {
				mustGit(t, recovered, "checkout", "--detach")
			}
			beforeHEAD, err := os.ReadFile(filepath.Join(recovered, ".git", "HEAD"))
			if err != nil {
				t.Fatal(err)
			}
			beforeIndex := mustGit(t, recovered, "write-tree")
			beforeStatus := mustGit(t, recovered, "status", "--porcelain")
			writeAndCommit(t, owner, "remote.txt", string(beforeHEAD), "remote update")
			mustCloakGit(t, binary, owner, "push", "backup", "main")
			for _, args := range [][]string{{"ls-remote", "origin"}, {"fetch", "origin"}} {
				mustCloakGit(t, binary, recovered, args...)
				afterHEAD, err := os.ReadFile(filepath.Join(recovered, ".git", "HEAD"))
				if err != nil || string(afterHEAD) != string(beforeHEAD) {
					t.Fatalf("%v changed HEAD from %q to %q: %v", args, beforeHEAD, afterHEAD, err)
				}
				if got := mustGit(t, recovered, "write-tree"); got != beforeIndex {
					t.Fatal("helper changed index")
				}
				if got := mustGit(t, recovered, "status", "--porcelain"); got != beforeStatus {
					t.Fatal("helper changed worktree status")
				}
			}
		})
	}
}

func TestEmptyWorkspaceEnforcesRollbackCheckpoint(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	owner, host := filepath.Join(root, "owner"), filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", owner)
	mustInit(t, binary, owner, host, testMnemonic)
	oldStorage := strings.TrimSpace(mustGit(t, host, "rev-parse", "refs/heads/cloak-storage"))
	command := exec.Command(binary, "set-head", "backup", "main")
	command.Dir, command.Env = owner, cloakGitEnvironment(binary)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("set-head: %v\n%s", err, output)
	}
	mustGit(t, host, "update-ref", "refs/heads/cloak-storage", oldStorage)
	checkpointPath := filepath.Join(owner, ".git", "cloak", "state")
	before, err := os.ReadFile(checkpointPath)
	if err != nil {
		t.Fatal(err)
	}
	command = exec.Command("git", "ls-remote", "backup")
	command.Dir, command.Env = owner, cloakGitEnvironment(binary)
	if output, err := command.CombinedOutput(); err == nil || !strings.Contains(string(output), "suspected rollback") {
		t.Fatalf("empty workspace accepted rollback: %v\n%s", err, output)
	}
	after, err := os.ReadFile(checkpointPath)
	if err != nil || string(before) != string(after) {
		t.Fatal("rollback changed checkpoint")
	}
}

func TestPushRevisionSourcesAndCommandNamedRemotes(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	owner, host := filepath.Join(root, "owner"), filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", owner)
	writeAndCommit(t, owner, "base.txt", "base\n", "base")
	writeAndCommit(t, owner, "next.txt", "next\n", "next")
	mustInit(t, binary, owner, host, testMnemonic)
	for _, source := range []string{"HEAD", "HEAD~1", strings.TrimSpace(mustGit(t, owner, "rev-parse", "HEAD"))} {
		t.Run(source, func(t *testing.T) {
			destination := "refs/heads/revision"
			mustCloakGit(t, binary, owner, "push", "--force", "backup", source+":"+destination)
			want := strings.TrimSpace(mustGit(t, owner, "rev-parse", source))
			got := mustCloakGit(t, binary, owner, "ls-remote", "backup", destination)
			if !strings.HasPrefix(got, want+"\t") {
				t.Fatalf("remote ref = %q, want %s", got, want)
			}
		})
	}
	for _, name := range []string{"version", "init", "clone", "doctor", "cache", "status", "set-head", "compact", "rekey", "migrate"} {
		t.Run("remote-"+name, func(t *testing.T) {
			mustGit(t, owner, "remote", "add", name, "cloak::"+host)
			mustCloakGit(t, binary, owner, "ls-remote", name)
		})
	}
	t.Run("detached HEAD push", func(t *testing.T) {
		mustGit(t, owner, "checkout", "--detach", "HEAD~1")
		want := strings.TrimSpace(mustGit(t, owner, "rev-parse", "HEAD"))
		mustCloakGit(t, binary, owner, "push", "backup", "HEAD:refs/heads/detached")
		got := mustCloakGit(t, binary, owner, "ls-remote", "backup", "refs/heads/detached")
		if !strings.HasPrefix(got, want+"\t") {
			t.Fatalf("detached push target = %q, want %s", got, want)
		}
		if head := mustGit(t, owner, "rev-parse", "--abbrev-ref", "HEAD"); head != "HEAD\n" {
			t.Fatalf("push attached local HEAD to %q", head)
		}
	})
	t.Run("command remote push URL", func(t *testing.T) {
		mustGit(t, owner, "remote", "set-url", "--push", "status", "cloak::"+host)
		mustGit(t, owner, "remote", "set-url", "status", "cloak::"+filepath.Join(root, "unused.git"))
		mustCloakGit(t, binary, owner, "push", "status", "HEAD:refs/heads/push-url")
	})
}
