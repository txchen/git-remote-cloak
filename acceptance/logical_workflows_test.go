package acceptance_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestIncrementalPushFetchPullAndUserMergeRoundTrip(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	owner := filepath.Join(root, "owner")
	secondHost := filepath.Join(root, "second-host")
	freshRecovery := filepath.Join(root, "fresh-recovery")
	repositoryHost := filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", repositoryHost)
	mustGit(t, root, "init", "-b", "main", owner)
	writeAndCommit(t, owner, "shared.txt", "base\n", "base commit")
	mustInit(t, binary, owner, repositoryHost, testMnemonic)
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	mustCloakGit(t, binary, root, "clone", "cloak::"+repositoryHost, secondHost)

	writeAndCommit(t, owner, "owner.txt", "owner change\n", "owner change")
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	mustCloakGit(t, binary, secondHost, "fetch", "origin")
	if got, want := mustGit(t, secondHost, "rev-parse", "origin/main"), mustGit(t, owner, "rev-parse", "main"); got != want {
		t.Fatalf("fetched main = %q, want %q", got, want)
	}

	writeAndCommit(t, secondHost, "second.txt", "second host change\n", "second host change")
	writeAndCommit(t, owner, "later.txt", "later owner change\n", "later owner change")
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	mustCloakGit(t, binary, secondHost, "pull", "--no-rebase", "origin", "main")
	if parents := mustGit(t, secondHost, "rev-list", "--parents", "-n", "1", "HEAD"); len(strings.Fields(parents)) != 3 {
		t.Fatalf("pull did not create the user-requested merge commit: %q", parents)
	}
	mustCloakGit(t, binary, secondHost, "push", "origin", "main")

	mustCloakGit(t, binary, root, "clone", "cloak::"+repositoryHost, freshRecovery)
	if got, want := mustGit(t, freshRecovery, "show-ref", "--heads", "--tags"), mustGit(t, secondHost, "show-ref", "--heads", "--tags"); got != want {
		t.Fatalf("fresh recovery refs:\n%s\nwant:\n%s", got, want)
	}
	if got, want := mustGit(t, freshRecovery, "rev-list", "--objects", "--all"), mustGit(t, secondHost, "rev-list", "--objects", "--all"); got != want {
		t.Fatalf("fresh recovery objects:\n%s\nwant:\n%s", got, want)
	}
	if output, err := exec.Command("git", "-C", freshRecovery, "fsck", "--full").CombinedOutput(); err != nil {
		t.Fatalf("fresh recovery fails fsck: %v\n%s", err, output)
	}
}

func TestMultiRefPushAndDeletionAreAtomic(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	owner := filepath.Join(root, "owner")
	recovered := filepath.Join(root, "recovered")
	repositoryHost := filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", repositoryHost)
	mustGit(t, root, "init", "-b", "main", owner)
	writeAndCommit(t, owner, "base.txt", "base\n", "base commit")
	mustInit(t, binary, owner, repositoryHost, testMnemonic)
	mustCloakGit(t, binary, owner, "push", "backup", "main")

	mustGit(t, owner, "switch", "-c", "topic")
	writeAndCommit(t, owner, "topic.txt", "topic\n", "topic commit")
	mustGit(t, owner, "tag", "-a", "annotated", "-m", "protected annotated tag")
	mustGit(t, owner, "tag", "lightweight")
	mustGit(t, owner, "switch", "main")
	writeAndCommit(t, owner, "main.txt", "main\n", "main commit")
	before := storageHistoryLength(t, repositoryHost)
	mustCloakGit(t, binary, owner, "push", "backup", "main", "topic", "refs/tags/annotated", "refs/tags/lightweight")
	if got := storageHistoryLength(t, repositoryHost); got != before+1 {
		t.Fatalf("multi-ref push created %d Storage History commits, want one", got-before)
	}
	assertProtectedPlaintextAbsent(t, repositoryHost, "refs/heads/main", "refs/heads/topic", "refs/tags/annotated", "refs/tags/lightweight", "protected annotated tag", "topic.txt", "topic commit")
	mustCloakGit(t, binary, root, "clone", "cloak::"+repositoryHost, recovered)
	assertLogicalRefsEqual(t, recovered, owner)
	if got := mustGit(t, recovered, "cat-file", "-t", "refs/tags/annotated"); got != "tag\n" {
		t.Fatalf("annotated tag recovered as %q", got)
	}
	if got := mustGit(t, recovered, "cat-file", "-t", "refs/tags/lightweight"); got != "commit\n" {
		t.Fatalf("lightweight tag recovered as %q", got)
	}

	before = storageHistoryLength(t, repositoryHost)
	mustCloakGit(t, binary, owner, "push", "backup", ":topic", ":refs/tags/lightweight")
	if got := storageHistoryLength(t, repositoryHost); got != before+1 {
		t.Fatalf("multi-ref deletion created %d Storage History commits, want one", got-before)
	}
	assertProtectedPlaintextAbsent(t, repositoryHost, "refs/heads/topic", "refs/tags/lightweight")
	mustCloakGit(t, binary, recovered, "fetch", "--prune", "origin", "+refs/heads/*:refs/remotes/origin/*", "+refs/tags/*:refs/tags/*")
	if got := mustGit(t, recovered, "for-each-ref", "--format=%(refname)", "refs/remotes/origin/topic"); got != "" {
		t.Fatalf("deleted branch remains advertised: %q", got)
	}
	if got := mustGit(t, recovered, "tag", "--list", "lightweight"); got != "" {
		t.Fatalf("deleted tag remains advertised: %q", got)
	}

	remoteMain := strings.TrimSpace(mustGit(t, owner, "rev-parse", "main"))
	writeAndCommit(t, owner, "next.txt", "next\n", "next main commit")
	mustGit(t, owner, "tag", "-f", "annotated", remoteMain+"^")
	beforeStorageID := mustGit(t, repositoryHost, "rev-parse", "refs/heads/cloak-storage")
	helper := exec.Command(binary, "backup", repositoryHost)
	helper.Dir = owner
	helper.Env = cloakGitEnvironment(binary)
	helper.Stdin = strings.NewReader("push refs/heads/main:refs/heads/main\npush refs/tags/annotated:refs/tags/annotated\n\n")
	output, err := helper.CombinedOutput()
	if err != nil {
		t.Fatalf("remote-helper transaction failed outside the protocol: %v\n%s", err, output)
	}
	if strings.Count(string(output), "error ") != 2 {
		t.Fatalf("failed transaction did not reject every requested update:\n%s", output)
	}
	if got := mustGit(t, repositoryHost, "rev-parse", "refs/heads/cloak-storage"); got != beforeStorageID {
		t.Fatalf("failed multi-ref push changed Storage Ref from %q to %q", beforeStorageID, got)
	}

	mustCloakGit(t, binary, owner, "push", "backup", ":main", ":refs/tags/annotated")
	assertProtectedPlaintextAbsent(t, repositoryHost, "refs/heads/main", "refs/tags/annotated")
	emptyRecovery := filepath.Join(root, "empty-recovery")
	mustCloakGit(t, binary, root, "clone", "cloak::"+repositoryHost, emptyRecovery)
	if got := mustGit(t, emptyRecovery, "for-each-ref", "--format=%(refname)"); got != "" {
		t.Fatalf("repository after deleting its last Logical Refs is not empty: %q", got)
	}
	if got := mustGit(t, emptyRecovery, "symbolic-ref", "HEAD"); got != "refs/heads/main\n" {
		t.Fatalf("unborn repository Logical HEAD = %q", got)
	}
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	assertProtectedPlaintextAbsent(t, repositoryHost, "refs/heads/main", "next.txt", "next main commit")
	rebornRecovery := filepath.Join(root, "reborn-recovery")
	mustCloakGit(t, binary, root, "clone", "cloak::"+repositoryHost, rebornRecovery)
	if got, want := mustGit(t, rebornRecovery, "rev-parse", "main"), mustGit(t, owner, "rev-parse", "main"); got != want {
		t.Fatalf("republished branch = %q, want %q", got, want)
	}
}

func TestForceAndForceWithLeaseUseCurrentLogicalRef(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	owner := filepath.Join(root, "owner")
	other := filepath.Join(root, "other")
	repositoryHost := filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", repositoryHost)
	mustGit(t, root, "init", "-b", "main", owner)
	writeAndCommit(t, owner, "base.txt", "base\n", "base commit")
	mustInit(t, binary, owner, repositoryHost, testMnemonic)
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	mustCloakGit(t, binary, root, "clone", "cloak::"+repositoryHost, other)

	writeAndCommit(t, owner, "owner.txt", "owner\n", "owner update")
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	mustGit(t, other, "reset", "--hard", "HEAD~0")
	writeAndCommit(t, other, "other.txt", "other\n", "divergent update")
	before := mustGit(t, repositoryHost, "rev-parse", "refs/heads/cloak-storage")
	push := exec.Command("git", "push", "origin", "main")
	push.Dir = other
	push.Env = cloakGitEnvironment(binary)
	if output, err := push.CombinedOutput(); err == nil {
		t.Fatalf("normal non-fast-forward push succeeded:\n%s", output)
	}
	if got := mustGit(t, repositoryHost, "rev-parse", "refs/heads/cloak-storage"); got != before {
		t.Fatalf("rejected non-fast-forward changed Storage Ref")
	}

	forceTarget := strings.TrimSpace(mustGit(t, other, "rev-parse", "main"))
	mustCloakGit(t, binary, other, "push", "--force", "origin", "main")
	if got := strings.TrimSpace(mustCloakGit(t, binary, owner, "ls-remote", "backup", "refs/heads/main")); !strings.HasPrefix(got, forceTarget+"\t") {
		t.Fatalf("forced Logical Ref target = %q, want %q", got, forceTarget)
	}

	writeAndCommit(t, other, "lease.txt", "lease\n", "lease update")
	leaseTarget := strings.TrimSpace(mustGit(t, other, "rev-parse", "main"))
	mustCloakGit(t, binary, other, "push", "--force-with-lease=main:"+forceTarget, "origin", "main")
	staleLease := forceTarget
	writeAndCommit(t, other, "stale.txt", "stale\n", "stale lease update")
	before = mustGit(t, repositoryHost, "rev-parse", "refs/heads/cloak-storage")
	push = exec.Command("git", "push", "--force-with-lease=main:"+staleLease, "origin", "main")
	push.Dir = other
	push.Env = cloakGitEnvironment(binary)
	if output, err := push.CombinedOutput(); err == nil {
		t.Fatalf("stale force-with-lease succeeded:\n%s", output)
	}
	if got := mustGit(t, repositoryHost, "rev-parse", "refs/heads/cloak-storage"); got != before {
		t.Fatalf("stale force-with-lease changed Storage Ref")
	}
	if got := strings.TrimSpace(mustCloakGit(t, binary, owner, "ls-remote", "backup", "refs/heads/main")); !strings.HasPrefix(got, leaseTarget+"\t") {
		t.Fatalf("stale lease changed Logical Ref to %q, want %q", got, leaseTarget)
	}
}

func TestIncrementalPayloadReuseAndLogicalHEADChange(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	owner := filepath.Join(root, "owner")
	recovered := filepath.Join(root, "recovered")
	repositoryHost := filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", repositoryHost)
	mustGit(t, root, "init", "-b", "main", owner)
	writeAndCommit(t, owner, "base.txt", "base\n", "base commit")
	mustInit(t, binary, owner, repositoryHost, testMnemonic)
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	firstObjects := ciphertextObjectPaths(t, repositoryHost)
	assertProtectedPlaintextAbsent(t, repositoryHost, "refs/heads/main", "base.txt", "base commit")

	mustGit(t, owner, "switch", "-c", "alternate")
	writeAndCommit(t, owner, "alternate.txt", "alternate\n", "alternate commit")
	mustGit(t, owner, "switch", "main")
	writeAndCommit(t, owner, "incremental.txt", "incremental\n", "incremental commit")
	mustCloakGit(t, binary, owner, "push", "backup", "main", "alternate")
	secondObjects := ciphertextObjectPaths(t, repositoryHost)
	assertProtectedPlaintextAbsent(t, repositoryHost, "refs/heads/alternate", "alternate.txt", "alternate commit", "incremental.txt", "incremental commit")
	if reused := intersectionSize(firstObjects, secondObjects); reused < 2 {
		t.Fatalf("incremental push reused %d ciphertext objects, want the existing Pack Payload index and chunk", reused)
	}

	setHead := exec.Command(binary, "set-head", "backup", "alternate")
	setHead.Dir = owner
	setHead.Env = cloakGitEnvironment(binary)
	if output, err := setHead.CombinedOutput(); err != nil {
		t.Fatalf("set-head failed: %v\n%s", err, output)
	}
	thirdObjects := ciphertextObjectPaths(t, repositoryHost)
	assertProtectedPlaintextAbsent(t, repositoryHost, "refs/heads/alternate")
	if reused := intersectionSize(secondObjects, thirdObjects); reused != len(secondObjects)-1 {
		t.Fatalf("Logical HEAD update reused %d of %d prior ciphertext objects, want every Pack Payload object", reused, len(secondObjects))
	}

	mustCloakGit(t, binary, root, "clone", "cloak::"+repositoryHost, recovered)
	if got, want := mustGit(t, recovered, "symbolic-ref", "HEAD"), "refs/heads/alternate\n"; got != want {
		t.Fatalf("recovered Logical HEAD = %q, want %q", got, want)
	}
	assertLogicalRefsEqual(t, recovered, owner)
	if got := mustGit(t, repositoryHost, "for-each-ref", "--format=%(refname)"); got != "refs/heads/cloak-storage\n" {
		t.Fatalf("Repository Host refs after Logical HEAD change = %q", got)
	}
}

func TestSignedCommitAndAnnotatedTagRemainVerifiable(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen is required for signed-object acceptance")
	}
	binary := buildBinary(t)
	root := t.TempDir()
	owner := filepath.Join(root, "owner")
	recovered := filepath.Join(root, "recovered")
	repositoryHost := filepath.Join(root, "host.git")
	key := filepath.Join(root, "signing-key")
	keygen := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key)
	if output, err := keygen.CombinedOutput(); err != nil {
		t.Fatalf("generate signing key: %v\n%s", err, output)
	}
	publicKey, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	allowedSigners := filepath.Join(root, "allowed-signers")
	if err := os.WriteFile(allowedSigners, append([]byte("cloak@example.invalid "), publicKey...), 0o600); err != nil {
		t.Fatal(err)
	}

	mustGit(t, root, "init", "--bare", repositoryHost)
	mustGit(t, root, "init", "-b", "main", owner)
	mustGit(t, owner, "config", "gpg.format", "ssh")
	mustGit(t, owner, "config", "user.signingkey", key)
	mustGit(t, owner, "config", "commit.gpgsign", "true")
	mustGit(t, owner, "config", "gpg.ssh.allowedSignersFile", allowedSigners)
	writeAndCommit(t, owner, "signed.txt", "signed contents\n", "signed commit")
	mustGit(t, owner, "tag", "-s", "signed-tag", "-m", "signed annotated tag")
	wantCommit := mustGit(t, owner, "cat-file", "commit", "HEAD")
	wantTag := mustGit(t, owner, "cat-file", "tag", "refs/tags/signed-tag")
	mustInit(t, binary, owner, repositoryHost, testMnemonic)
	mustCloakGit(t, binary, owner, "push", "backup", "main", "refs/tags/signed-tag")
	mustCloakGit(t, binary, root, "clone", "cloak::"+repositoryHost, recovered)
	mustGit(t, recovered, "config", "gpg.ssh.allowedSignersFile", allowedSigners)
	mustGit(t, recovered, "verify-commit", "HEAD")
	mustGit(t, recovered, "verify-tag", "signed-tag")
	if got := mustGit(t, recovered, "cat-file", "commit", "HEAD"); got != wantCommit {
		t.Fatal("signed commit did not recover byte-exactly")
	}
	if got := mustGit(t, recovered, "cat-file", "tag", "refs/tags/signed-tag"); got != wantTag {
		t.Fatal("signed annotated tag did not recover byte-exactly")
	}
}

func writeAndCommit(t *testing.T, repository, name, contents, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repository, name), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repository, "add", name)
	mustGit(t, repository, "commit", "-m", message)
}

func mustCloakGit(t *testing.T, binary, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	command.Env = cloakGitEnvironment(binary)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", arguments, err, output)
	}
	return string(output)
}

func storageHistoryLength(t *testing.T, repositoryHost string) int {
	t.Helper()
	count, err := strconv.Atoi(strings.TrimSpace(mustGit(t, repositoryHost, "rev-list", "--count", "refs/heads/cloak-storage")))
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func assertLogicalRefsEqual(t *testing.T, gotRepository, wantRepository string) {
	t.Helper()
	wantRefs := mustGit(t, wantRepository, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads", "refs/tags")
	for _, line := range strings.Split(strings.TrimSpace(wantRefs), "\n") {
		fields := strings.Fields(line)
		name, want := fields[0], fields[1]
		gotName := name
		got := strings.TrimSpace(mustGit(t, gotRepository, "for-each-ref", "--format=%(objectname)", gotName))
		if got == "" && strings.HasPrefix(name, "refs/heads/") {
			gotName = "refs/remotes/origin/" + strings.TrimPrefix(name, "refs/heads/")
			got = strings.TrimSpace(mustGit(t, gotRepository, "for-each-ref", "--format=%(objectname)", gotName))
		}
		if got != want {
			t.Fatalf("Logical Ref %s through %s = %q, want %q", name, gotName, got, want)
		}
	}
}

func ciphertextObjectPaths(t *testing.T, repositoryHost string) map[string]struct{} {
	t.Helper()
	output := mustGit(t, repositoryHost, "ls-tree", "-r", "--name-only", "refs/heads/cloak-storage", "objects")
	paths := make(map[string]struct{})
	for _, path := range strings.Fields(output) {
		paths[path] = struct{}{}
	}
	return paths
}

func intersectionSize(left, right map[string]struct{}) int {
	count := 0
	for value := range left {
		if _, exists := right[value]; exists {
			count++
		}
	}
	return count
}

func assertProtectedPlaintextAbsent(t *testing.T, repositoryHost string, protected ...string) {
	t.Helper()
	if got := mustGit(t, repositoryHost, "for-each-ref", "--format=%(refname)"); got != "refs/heads/cloak-storage\n" {
		t.Fatalf("Repository Host refs = %q", got)
	}
	reachable := mustGit(t, repositoryHost, "rev-list", "--objects", "--all")
	var inspected bytes.Buffer
	inspected.WriteString(reachable)
	for _, line := range strings.Split(strings.TrimSpace(reachable), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			inspected.WriteString(mustGit(t, repositoryHost, "cat-file", "-p", fields[0]))
		}
	}
	for _, value := range protected {
		if bytes.Contains(inspected.Bytes(), []byte(value)) {
			t.Fatalf("reachable Ciphertext Repository objects expose Protected Plaintext %q", value)
		}
	}
}
