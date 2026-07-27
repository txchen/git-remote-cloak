package acceptance_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	cloakformat "github.com/txchen/git-remote-cloak/internal/format"
	"github.com/txchen/git-remote-cloak/internal/gitdb"
	"github.com/txchen/git-remote-cloak/internal/secret"
	"github.com/txchen/git-remote-cloak/internal/storage"
)

func TestDepthOneCloneAndFetchPreserveOrdinaryShallowHistory(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	recovered := filepath.Join(root, "recovered")
	host := filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", workspace)
	writeAndCommit(t, workspace, "first.txt", "first protected contents\n", "first protected commit")
	writeAndCommit(t, workspace, "second.txt", "second protected contents\n", "second protected commit")
	writeAndCommit(t, workspace, "third.txt", "third protected contents\n", "third protected commit")
	mustInit(t, binary, workspace, host, testMnemonic)
	mustCloakGit(t, binary, workspace, "push", "backup", "main")

	clone := exec.Command("git", "clone", "--depth=1", "cloak::"+host, recovered)
	clone.Dir = root
	clone.Env = cloakGitEnvironment(binary)
	if output, err := clone.CombinedOutput(); err != nil {
		t.Fatalf("depth-one clone failed: %v\n%s", err, output)
	}
	if got := mustGit(t, recovered, "rev-list", "--count", "HEAD"); got != "1\n" {
		t.Fatalf("depth-one clone visible commit count = %q, want 1", got)
	}
	if got := strings.TrimSpace(mustGit(t, recovered, "rev-parse", "--is-shallow-repository")); got != "true" {
		t.Fatalf("depth-one clone shallow state = %q, want true", got)
	}
	if got := strings.TrimSpace(mustGit(t, recovered, "rev-parse", "HEAD")); got != strings.TrimSpace(mustGit(t, workspace, "rev-parse", "HEAD")) {
		t.Fatalf("depth-one clone recovered HEAD = %q, want current Plaintext Workspace HEAD", got)
	}
	for _, path := range []string{"first.txt", "second.txt", "third.txt"} {
		if _, err := os.Stat(filepath.Join(recovered, path)); err != nil {
			t.Fatalf("depth-one clone omitted current checkout path %s: %v", path, err)
		}
	}

	writeAndCommit(t, workspace, "fourth.txt", "fourth protected contents\n", "fourth protected commit")
	mustCloakGit(t, binary, workspace, "push", "backup", "main")
	mustCloakGit(t, binary, recovered, "fetch", "origin", "main")
	if got := strings.TrimSpace(mustGit(t, recovered, "rev-parse", "--is-shallow-repository")); got != "true" {
		t.Fatalf("fetch discarded shallow state: %q", got)
	}
	if got := mustGit(t, recovered, "rev-list", "--count", "FETCH_HEAD"); got != "2\n" {
		t.Fatalf("shallow fetch visible commit count = %q, want new commit plus prior boundary", got)
	}
	if output, err := exec.Command("git", "-C", recovered, "fsck", "--full").CombinedOutput(); err != nil {
		t.Fatalf("shallow repository fails fsck: %v\n%s", err, output)
	}
	mustCloakGit(t, binary, recovered, "fetch", "--deepen=1", "origin", "main")
	if got := mustGit(t, recovered, "rev-list", "--count", "FETCH_HEAD"); got != "3\n" {
		t.Fatalf("deepened fetch visible commit count = %q, want 3", got)
	}
	mustCloakGit(t, binary, recovered, "fetch", "--unshallow", "origin", "main")
	if got := strings.TrimSpace(mustGit(t, recovered, "rev-parse", "--is-shallow-repository")); got != "false" {
		t.Fatalf("unshallow fetch retained shallow state: %q", got)
	}
	if got := mustGit(t, recovered, "rev-list", "--count", "FETCH_HEAD"); got != "4\n" {
		t.Fatalf("unshallowed fetch visible commit count = %q, want 4", got)
	}
	assertProtectedPlaintextAbsent(t, host,
		"first.txt", "first protected contents", "first protected commit",
		"second.txt", "second protected contents", "second protected commit",
		"third.txt", "third protected contents", "third protected commit",
		"fourth.txt", "fourth protected contents", "fourth protected commit",
	)
}

func TestShallowDepthCountsMergeGraphGenerations(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	recovered := filepath.Join(root, "recovered")
	host := filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", workspace)
	writeAndCommit(t, workspace, "base.txt", "base\n", "base commit")
	mustGit(t, workspace, "switch", "-c", "left")
	writeAndCommit(t, workspace, "left.txt", "left\n", "left commit")
	left := strings.TrimSpace(mustGit(t, workspace, "rev-parse", "HEAD"))
	mustGit(t, workspace, "switch", "main")
	writeAndCommit(t, workspace, "right.txt", "right\n", "right commit")
	right := strings.TrimSpace(mustGit(t, workspace, "rev-parse", "HEAD"))
	mustGit(t, workspace, "merge", "--no-ff", "left", "-m", "merge protected histories")
	mustInit(t, binary, workspace, host, testMnemonic)
	mustCloakGit(t, binary, workspace, "push", "backup", "main")

	clone := exec.Command("git", "clone", "--depth=2", "cloak::"+host, recovered)
	clone.Dir = root
	clone.Env = cloakGitEnvironment(binary)
	if output, err := clone.CombinedOutput(); err != nil {
		t.Fatalf("depth-two merge clone failed: %v\n%s", err, output)
	}
	if got := mustGit(t, recovered, "rev-list", "--count", "HEAD"); got != "3\n" {
		t.Fatalf("depth-two merge clone visible commit count = %q, want merge and both parents", got)
	}
	shallow, err := os.ReadFile(filepath.Join(recovered, ".git", "shallow"))
	if err != nil {
		t.Fatal(err)
	}
	wantBoundaries := map[string]bool{left: true, right: true}
	for _, boundary := range strings.Fields(string(shallow)) {
		delete(wantBoundaries, boundary)
	}
	if len(wantBoundaries) != 0 || len(strings.Fields(string(shallow))) != 2 {
		t.Fatalf("merge shallow boundaries = %q, want both merge parents", shallow)
	}
	assertProtectedPlaintextAbsent(t, host, "base.txt", "left.txt", "right.txt", "merge protected histories")
}

func TestSubmoduleMetadataAndGitlinkRoundTripWithoutImplicitClone(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	recovered := filepath.Join(root, "recovered")
	host := filepath.Join(root, "host.git")
	submoduleSource := filepath.Join(root, "private-submodule-source")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", workspace)
	mustGit(t, root, "init", "-b", "main", submoduleSource)
	writeAndCommit(t, submoduleSource, "private-child.txt", "private child contents\n", "private child commit")
	submoduleCommit := strings.TrimSpace(mustGit(t, submoduleSource, "rev-parse", "HEAD"))
	gitmodules := "[submodule \"private-child\"]\n\tpath = modules/private-child\n\turl = cloak::private-child-host.git\n"
	if err := os.WriteFile(filepath.Join(workspace, ".gitmodules"), []byte(gitmodules), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, workspace, "add", ".gitmodules")
	mustGit(t, workspace, "update-index", "--add", "--cacheinfo", "160000,"+submoduleCommit+",modules/private-child")
	mustGit(t, workspace, "commit", "-m", "record private submodule metadata")
	mustInit(t, binary, workspace, host, testMnemonic)
	mustCloakGit(t, binary, workspace, "push", "backup", "main")
	mustCloakGit(t, binary, root, "clone", "cloak::"+host, recovered)

	if got, err := os.ReadFile(filepath.Join(recovered, ".gitmodules")); err != nil || string(got) != gitmodules {
		t.Fatalf("recovered .gitmodules = %q, err=%v", got, err)
	}
	stage := mustGit(t, recovered, "ls-files", "--stage", "modules/private-child")
	if !strings.HasPrefix(stage, "160000 "+submoduleCommit+" 0\t") {
		t.Fatalf("recovered gitlink = %q", stage)
	}
	if _, err := os.Stat(filepath.Join(recovered, "modules", "private-child", ".git")); !os.IsNotExist(err) {
		t.Fatalf("Cloak implicitly cloned the submodule: %v", err)
	}
	config, err := os.ReadFile(filepath.Join(recovered, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(config), "[submodule ") {
		t.Fatalf("Cloak configured submodule secret orchestration: %s", config)
	}
	assertProtectedPlaintextAbsent(t, host,
		".gitmodules", "modules/private-child", "cloak::private-child-host.git",
		"record private submodule metadata",
	)
}

func TestPushRejectsNewlyReachableLFSPointersBeforePublication(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	host := filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", workspace)
	writeAndCommit(t, workspace, "ordinary.bin", "\x00ordinary binary content\xff\n", "ordinary binary commit")
	writeAndCommit(t, workspace, "lfs-lookalike.txt",
		"version https://git-lfs.github.com/spec/v1\n"+
			"oid sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n"+
			"size 123456\n"+
			"ordinary trailing content makes this a normal Git blob\n",
		"ordinary LFS-lookalike commit")
	mustInit(t, binary, workspace, host, testMnemonic)
	mustCloakGit(t, binary, workspace, "push", "backup", "main")
	before := strings.TrimSpace(mustGit(t, host, "rev-parse", "refs/heads/cloak-storage"))

	lfsPointer := "version https://git-lfs.github.com/spec/v1\n" +
		"oid sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n" +
		"size 123456\n"
	for _, path := range []string{"assets/private-video.bin", "assets/private-image.bin"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(workspace, path)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspace, path), []byte(lfsPointer), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	extensionPath := "assets/private-extension.bin"
	extensionPointer := "version https://git-lfs.github.com/spec/v1\n" +
		"ext-0-vendor.example/retention archive\n" +
		"oid sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n" +
		"size 123456\n"
	if err := os.WriteFile(filepath.Join(workspace, extensionPath), []byte(extensionPointer), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, workspace, "add", "assets")
	mustGit(t, workspace, "commit", "-m", "add private LFS content")
	push := exec.Command("git", "push", "backup", "main")
	push.Dir = workspace
	push.Env = cloakGitEnvironment(binary)
	output, err := push.CombinedOutput()
	if err == nil {
		t.Fatalf("Git LFS pointer push succeeded:\n%s", output)
	}
	message := string(output)
	for _, path := range []string{"assets/private-video.bin", "assets/private-image.bin", extensionPath} {
		if !strings.Contains(message, path) {
			t.Fatalf("LFS rejection does not name affected path %q:\n%s", path, output)
		}
	}
	if strings.Contains(message, "0123456789abcdef") || strings.Contains(message, "123456") {
		t.Fatalf("LFS rejection exposes pointer content instead of paths only:\n%s", output)
	}
	if got := strings.TrimSpace(mustGit(t, host, "rev-parse", "refs/heads/cloak-storage")); got != before {
		t.Fatalf("rejected LFS push changed Storage Ref from %s to %s", before, got)
	}
	assertProtectedPlaintextAbsent(t, host,
		"assets/private-video.bin", "assets/private-image.bin", extensionPath, "add private LFS content",
		"lfs-lookalike.txt", "ordinary LFS-lookalike commit", "ordinary trailing content",
		"0123456789abcdef", "123456",
	)
}

func TestCloneAndFetchRejectExistingLogicalRepositoryWithLFSContent(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	consumer := filepath.Join(root, "consumer")
	humanDestination := filepath.Join(root, "human-recovery")
	helperDestination := filepath.Join(root, "helper-recovery")
	host := filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", workspace)
	writeAndCommit(t, workspace, "ordinary.txt", "ordinary contents\n", "ordinary commit")
	mustInit(t, binary, workspace, host, testMnemonic)
	mustCloakGit(t, binary, workspace, "push", "backup", "main")
	mustCloakGit(t, binary, root, "clone", "cloak::"+host, consumer)
	oldTarget := strings.TrimSpace(mustGit(t, consumer, "rev-parse", "origin/main"))

	lfsPath := "assets/existing-lfs.bin"
	if err := os.MkdirAll(filepath.Join(workspace, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, lfsPath), []byte(
		"version https://git-lfs.github.com/spec/v1\n"+
			"oid sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789\n"+
			"size 987654\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, workspace, "add", lfsPath)
	mustGit(t, workspace, "commit", "-m", "existing LFS commit")
	publishUnsupportedLFSSnapshot(t, workspace, host)

	for name, command := range map[string]*exec.Cmd{
		"human":         exec.Command(binary, "clone", host, humanDestination),
		"remote-helper": exec.Command("git", "clone", "cloak::"+host, helperDestination),
	} {
		command.Dir = root
		command.Env = cloakGitEnvironment(binary)
		output, err := command.CombinedOutput()
		if err == nil {
			t.Fatalf("%s clone of LFS-backed repository succeeded:\n%s", name, output)
		}
		if !strings.Contains(string(output), "Git LFS") {
			t.Fatalf("%s clone did not explain the Git LFS exclusion:\n%s", name, output)
		}
		destination := humanDestination
		if name == "remote-helper" {
			destination = helperDestination
		}
		if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
			t.Fatalf("%s failed clone exposed a partial destination: %v", name, statErr)
		}
	}

	fetch := exec.Command("git", "fetch", "origin")
	fetch.Dir = consumer
	fetch.Env = cloakGitEnvironment(binary)
	output, err := fetch.CombinedOutput()
	if err == nil {
		t.Fatalf("fetch of LFS-backed repository succeeded:\n%s", output)
	}
	if !strings.Contains(string(output), "Git LFS") {
		t.Fatalf("fetch did not explain the Git LFS exclusion:\n%s", output)
	}
	if got := strings.TrimSpace(mustGit(t, consumer, "rev-parse", "origin/main")); got != oldTarget {
		t.Fatalf("rejected fetch changed origin/main from %s to %s", oldTarget, got)
	}
	if _, err := os.Stat(filepath.Join(consumer, lfsPath)); !os.IsNotExist(err) {
		t.Fatalf("rejected fetch exposed an LFS-backed path: %v", err)
	}
	assertProtectedPlaintextAbsent(t, host, lfsPath, "existing LFS commit", "abcdef0123456789", "987654")
}

func publishUnsupportedLFSSnapshot(t *testing.T, workspace, host string) {
	t.Helper()
	recoverySecret, err := secret.Parse(testMnemonic)
	if err != nil {
		t.Fatal(err)
	}
	transport, err := storage.OpenLocalBare(host)
	if err != nil {
		t.Fatal(err)
	}
	current, err := transport.Read()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := cloakformat.NewRegistry().DecodeSnapshot(recoverySecret, current.Bootstrap, current.CiphertextObjects)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := gitdb.CreatePack(filepath.Join(workspace, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	target := strings.TrimSpace(mustGit(t, workspace, "rev-parse", "main"))
	encoded, err := cloakformat.NewRegistry().EncodeSnapshot(recoverySecret, cloakformat.SnapshotInput{
		Repository: cloakformat.SnapshotState{
			RepositoryID:       decoded.Repository.RepositoryID,
			Generation:         decoded.Repository.Generation + 1,
			LogicalHEAD:        decoded.Repository.LogicalHEAD,
			ObjectFormat:       decoded.Repository.ObjectFormat,
			LogicalRefs:        map[string]string{"refs/heads/main": target},
			PreviousStorageRef: current.StorageCommitID,
		},
		Packs: []cloakformat.PackPayload{payload},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.PublishSnapshot(current.StorageCommitID, encoded.Bootstrap, encoded.CiphertextObjects); err != nil {
		t.Fatal(err)
	}
}

func TestPartialCloneFilterFailsExplicitlyWithoutUsableDestination(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	destination := filepath.Join(root, "partial-recovery")
	host := filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", workspace)
	writeAndCommit(t, workspace, "protected.txt", "protected partial-clone contents\n", "protected partial-clone commit")
	mustInit(t, binary, workspace, host, testMnemonic)
	mustCloakGit(t, binary, workspace, "push", "backup", "main")

	clone := exec.Command("git", "clone", "--filter=blob:none", "cloak::"+host, destination)
	clone.Dir = root
	clone.Env = cloakGitEnvironment(binary)
	output, err := clone.CombinedOutput()
	if err == nil {
		t.Fatalf("partial clone silently became a full clone:\n%s", output)
	}
	message := strings.ToLower(string(output))
	if !strings.Contains(message, "partial clone") && !strings.Contains(message, "promisor") {
		t.Fatalf("partial clone rejection was not explicit:\n%s", output)
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("rejected partial clone exposed a usable destination: %v", statErr)
	}
	assertProtectedPlaintextAbsent(t, host, "protected.txt", "protected partial-clone contents", "protected partial-clone commit")
}

func TestPromisorStateRejectsFetchAndPushWithoutLogicalMutation(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	consumer := filepath.Join(root, "consumer")
	host := filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", workspace)
	writeAndCommit(t, workspace, "first.txt", "first contents\n", "first commit")
	mustInit(t, binary, workspace, host, testMnemonic)
	mustCloakGit(t, binary, workspace, "push", "backup", "main")
	mustCloakGit(t, binary, root, "clone", "cloak::"+host, consumer)
	consumerTarget := strings.TrimSpace(mustGit(t, consumer, "rev-parse", "origin/main"))

	writeAndCommit(t, workspace, "second.txt", "second contents\n", "second commit")
	mustCloakGit(t, binary, workspace, "push", "backup", "main")
	configurePromisorRepository(t, consumer, "origin")
	fetch := exec.Command("git", "fetch", "origin")
	fetch.Dir = consumer
	fetch.Env = cloakGitEnvironment(binary)
	fetchOutput, err := fetch.CombinedOutput()
	if err == nil {
		t.Fatalf("promisor-backed fetch succeeded:\n%s", fetchOutput)
	}
	if message := strings.ToLower(string(fetchOutput)); !strings.Contains(message, "partial clone") && !strings.Contains(message, "promisor") {
		t.Fatalf("promisor-backed fetch rejection was not explicit:\n%s", fetchOutput)
	}
	if got := strings.TrimSpace(mustGit(t, consumer, "rev-parse", "origin/main")); got != consumerTarget {
		t.Fatalf("rejected promisor fetch changed origin/main from %s to %s", consumerTarget, got)
	}

	configurePromisorRepository(t, workspace, "backup")
	writeAndCommit(t, workspace, "third.txt", "third contents\n", "third commit")
	beforePush := strings.TrimSpace(mustGit(t, host, "rev-parse", "refs/heads/cloak-storage"))
	push := exec.Command("git", "push", "backup", "main")
	push.Dir = workspace
	push.Env = cloakGitEnvironment(binary)
	pushOutput, err := push.CombinedOutput()
	if err == nil {
		t.Fatalf("promisor-backed push succeeded:\n%s", pushOutput)
	}
	if message := strings.ToLower(string(pushOutput)); !strings.Contains(message, "partial clone") && !strings.Contains(message, "promisor") {
		t.Fatalf("promisor-backed push rejection was not explicit:\n%s", pushOutput)
	}
	if got := strings.TrimSpace(mustGit(t, host, "rev-parse", "refs/heads/cloak-storage")); got != beforePush {
		t.Fatalf("rejected promisor push changed Storage Ref from %s to %s", beforePush, got)
	}
	assertProtectedPlaintextAbsent(t, host, "first.txt", "second.txt", "third.txt", "first commit", "second commit", "third commit")
}

func configurePromisorRepository(t *testing.T, repository, remote string) {
	t.Helper()
	mustGit(t, repository, "config", "core.repositoryFormatVersion", "1")
	mustGit(t, repository, "config", "extensions.partialClone", remote)
	mustGit(t, repository, "config", "remote."+remote+".promisor", "true")
	mustGit(t, repository, "config", "remote."+remote+".partialCloneFilter", "blob:none")
}
