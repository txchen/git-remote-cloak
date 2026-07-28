package acceptance_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCiphertextCacheSurvivesRestartCorruptionAndClear(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	owner := filepath.Join(root, "owner")
	recovered := filepath.Join(root, "recovered")
	host := filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", owner)
	writeAndCommit(t, owner, "protected.txt", "protected cache contents\n", "protected cache commit")
	mustInit(t, binary, owner, host, testMnemonic)
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	mustCloakGit(t, binary, root, "clone", "cloak::"+host, recovered)
	mustCloakGit(t, binary, recovered, "fetch", "origin")

	cache := filepath.Join(recovered, ".git", "cloak", "cache")
	object := firstRegularFile(t, filepath.Join(cache, "objects"))
	assertFilesExclude(t, cache, testMnemonic, "protected cache contents", "protected cache commit", "protected.txt", "refs/heads/main")
	beforeReuse, err := os.Stat(object)
	if err != nil {
		t.Fatal(err)
	}
	mustCloakGit(t, binary, recovered, "fetch", "origin")
	afterReuse, err := os.Stat(object)
	if err != nil {
		t.Fatal(err)
	}
	if !afterReuse.ModTime().Equal(beforeReuse.ModTime()) {
		t.Fatal("valid immutable cache entry was rebuilt instead of reused")
	}

	if err := os.WriteFile(object, []byte("damaged cache entry"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustCloakGit(t, binary, recovered, "fetch", "origin")
	if got, err := os.ReadFile(object); err != nil || bytes.Equal(got, []byte("damaged cache entry")) {
		t.Fatalf("corrupt cache entry was not reconstructed: contents=%q err=%v", got, err)
	}

	clear := exec.Command(binary, "cache", "clear")
	clear.Dir = recovered
	clear.Env = withoutEnvironment(os.Environ(), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE")
	if output, err := clear.CombinedOutput(); err != nil {
		t.Fatalf("cache clear failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatalf("cache clear left reconstructable data behind: %v", err)
	}
	mustCloakGit(t, binary, recovered, "fetch", "origin")
	_ = firstRegularFile(t, filepath.Join(cache, "objects"))
}

func TestFetchFromEmptyCiphertextRepositoryPreservesExistingUnbornRepository(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	initializer := filepath.Join(root, "initializer")
	existing := filepath.Join(root, "existing")
	host := filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", initializer)
	mustInit(t, binary, initializer, host, testMnemonic)
	mustGit(t, root, "init", "-b", "main", existing)
	mustGit(t, existing, "remote", "add", "origin", "cloak::"+host)
	mustGit(t, existing, "config", "cloak.test-marker", "preserved")
	if err := os.Chmod(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(existing, ".git", "hooks", "preserved-hook")
	if err := os.WriteFile(hook, []byte("local hook contents\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	mustCloakGit(t, binary, existing, "fetch", "origin")
	if got := mustGit(t, existing, "config", "--get", "cloak.test-marker"); got != "preserved\n" {
		t.Fatalf("empty fetch changed local config marker to %q", got)
	}
	if got, err := os.ReadFile(hook); err != nil || string(got) != "local hook contents\n" {
		t.Fatalf("empty fetch replaced the existing repository hook: contents=%q err=%v", got, err)
	}
	info, err := os.Stat(existing)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("empty fetch changed existing repository permissions to %v", info.Mode().Perm())
	}

	writeAndCommit(t, initializer, "published.txt", "published later\n", "published later")
	mustCloakGit(t, binary, initializer, "push", "backup", "main")
	mustCloakGit(t, binary, existing, "fetch", "origin")
	if got := mustGit(t, existing, "config", "--get", "cloak.test-marker"); got != "preserved\n" {
		t.Fatalf("non-empty fetch changed local config marker to %q", got)
	}
	if got, err := os.ReadFile(hook); err != nil || string(got) != "local hook contents\n" {
		t.Fatalf("non-empty fetch replaced the existing repository hook: contents=%q err=%v", got, err)
	}
	if got, want := mustGit(t, existing, "rev-parse", "origin/main"), mustGit(t, initializer, "rev-parse", "main"); got != want {
		t.Fatalf("non-empty fetch target = %q, want %q", got, want)
	}
}

func TestPushJournalRecoversBeforeAndAfterStorageRefPublication(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	owner := filepath.Join(root, "owner")
	host := filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", owner)
	writeAndCommit(t, owner, "base.txt", "base protected contents\n", "base protected commit")
	mustInit(t, binary, owner, host, testMnemonic)
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	previousCommit := mustGit(t, owner, "rev-parse", "HEAD")

	writeAndCommit(t, owner, "interrupted.txt", "interrupted protected contents\n", "interrupted protected commit")
	before := mustGit(t, host, "rev-parse", "refs/heads/cloak-storage")
	failedCloakPush(t, binary, owner, "lost-process-before-storage-ref")
	if got := mustGit(t, host, "rev-parse", "refs/heads/cloak-storage"); got != before {
		t.Fatalf("interruption before publication changed Storage Ref from %q to %q", before, got)
	}
	transactions := filepath.Join(owner, ".git", "cloak", "transactions")
	_ = firstRegularFile(t, transactions)
	assertFilesExclude(t, transactions, testMnemonic, "interrupted protected contents", "interrupted protected commit", "interrupted.txt", "refs/heads/main")
	previousRecovery := filepath.Join(root, "previous-after-process-loss")
	mustCloakGit(t, binary, root, "clone", "cloak::"+host, previousRecovery)
	if got := mustGit(t, previousRecovery, "rev-parse", "HEAD"); got != previousCommit {
		t.Fatalf("snapshot after process loss recovered %q, want %q", got, previousCommit)
	}
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	assertDirectoryHasNoFiles(t, transactions)

	writeAndCommit(t, owner, "lost-response.txt", "lost response protected contents\n", "lost response protected commit")
	before = mustGit(t, host, "rev-parse", "refs/heads/cloak-storage")
	failedCloakPush(t, binary, owner, "after-storage-ref")
	after := mustGit(t, host, "rev-parse", "refs/heads/cloak-storage")
	if after == before {
		t.Fatal("lost-response fault did not publish the prepared Storage commit")
	}
	_ = firstRegularFile(t, transactions)
	assertFilesExclude(t, transactions, testMnemonic, "lost response protected contents", "lost response protected commit", "lost-response.txt", "refs/heads/main")
	compatibleWriter := filepath.Join(root, "compatible-writer")
	mustCloakGit(t, binary, root, "clone", "cloak::"+host, compatibleWriter)
	mustGit(t, compatibleWriter, "config", "remote.origin.cloakAutoCompact", "false")
	mustGit(t, compatibleWriter, "switch", "-c", "topic")
	writeAndCommit(t, compatibleWriter, "topic.txt", "compatible writer\n", "compatible writer commit")
	mustCloakGit(t, binary, compatibleWriter, "push", "origin", "topic")
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	assertDirectoryHasNoFiles(t, transactions)

	recovered := filepath.Join(root, "recovered")
	mustCloakGit(t, binary, root, "clone", "cloak::"+host, recovered)
	if got, want := mustGit(t, recovered, "rev-parse", "HEAD"), mustGit(t, owner, "rev-parse", "HEAD"); got != want {
		t.Fatalf("retry recovered commit %q, want %q", got, want)
	}
}

func TestPublicationFaultsRemainRetryableAndPreviousSnapshotRecovers(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	owner := filepath.Join(root, "owner")
	host := filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", owner)
	writeAndCommit(t, owner, "base.txt", "base\n", "base")
	mustInit(t, binary, owner, host, testMnemonic)
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	previousCommit := mustGit(t, owner, "rev-parse", "HEAD")

	writeAndCommit(t, owner, "upload.txt", "upload fault protected\n", "upload fault protected commit")
	before := mustGit(t, host, "rev-parse", "refs/heads/cloak-storage")
	output := failedCloakPush(t, binary, owner, "immutable-upload-failure")
	if !strings.Contains(output, "3 attempts") {
		t.Fatalf("bounded upload failure did not report its retry limit:\n%s", output)
	}
	if got := mustGit(t, host, "rev-parse", "refs/heads/cloak-storage"); got != before {
		t.Fatal("failed immutable upload changed the authoritative Storage Ref")
	}
	previousRecovery := filepath.Join(root, "previous-recovery")
	mustCloakGit(t, binary, root, "clone", "cloak::"+host, previousRecovery)
	if got := mustGit(t, previousRecovery, "rev-parse", "HEAD"); got != previousCommit {
		t.Fatalf("previous snapshot recovered %q, want %q", got, previousCommit)
	}
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	previousCommit = mustGit(t, owner, "rev-parse", "HEAD")

	writeAndCommit(t, owner, "journal.txt", "journal fault protected\n", "journal fault protected commit")
	before = mustGit(t, host, "rev-parse", "refs/heads/cloak-storage")
	_ = failedCloakPush(t, binary, owner, "local-journal-write")
	if got := mustGit(t, host, "rev-parse", "refs/heads/cloak-storage"); got != before {
		t.Fatal("local journal write failure changed the authoritative Storage Ref")
	}
	journalFailureRecovery := filepath.Join(root, "previous-after-journal-failure")
	mustCloakGit(t, binary, root, "clone", "cloak::"+host, journalFailureRecovery)
	if got := mustGit(t, journalFailureRecovery, "rev-parse", "HEAD"); got != previousCommit {
		t.Fatalf("snapshot after journal failure recovered %q, want %q", got, previousCommit)
	}
	mustCloakGit(t, binary, owner, "push", "backup", "main")

	transactions := filepath.Join(owner, ".git", "cloak", "transactions")
	if err := os.MkdirAll(transactions, 0o700); err != nil {
		t.Fatal(err)
	}
	invalid := filepath.Join(transactions, strings.Repeat("a", 64)+".json")
	validLookingCorruption := `{"version":1,"intent_id":"` + strings.Repeat("a", 64) + `","starting_storage_commit_id":"` + strings.Repeat("0", 40) + `","prepared_storage_commit_id":"` + strings.Repeat("1", 40) + `","authentication_tag":"` + strings.Repeat("2", 64) + `"}` + "\n"
	if err := os.WriteFile(invalid, []byte(validLookingCorruption), 0o600); err != nil {
		t.Fatal(err)
	}
	mustCloakGit(t, binary, owner, "fetch", "backup")
	if _, err := os.Stat(invalid); !os.IsNotExist(err) {
		t.Fatalf("invalid journal was not isolated: %v", err)
	}
}

func failedCloakPush(t *testing.T, binary, directory, fault string) string {
	t.Helper()
	command := exec.Command("git", "push", "backup", "main")
	command.Dir = directory
	command.Env = append(cloakGitEnvironment(binary), "CLOAK_TEST_FAULT="+fault, "TMPDIR="+t.TempDir())
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("push with %s fault succeeded:\n%s", fault, output)
	}
	return string(output)
}

func assertDirectoryHasNoFiles(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return filepath.SkipDir
			}
			return err
		}
		if entry.Type().IsRegular() {
			t.Fatalf("unexpected persistent transaction journal %s", path)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func firstRegularFile(t *testing.T, root string) string {
	t.Helper()
	var found string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if found == "" && entry.Type().IsRegular() {
			found = path
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == "" {
		t.Fatalf("no regular cache entry under %s", root)
	}
	return found
}

func assertFilesExclude(t *testing.T, root string, protected ...string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return err
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, value := range protected {
			if strings.Contains(string(contents), value) {
				t.Fatalf("persistent local state %s contains protected value %q", path, value)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
