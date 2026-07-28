package acceptance_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCompactRebuildsOneValidatedParentlessSnapshot(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	owner := filepath.Join(root, "owner")
	recovered := filepath.Join(root, "recovered")
	host := filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", owner)
	writeAndCommit(t, owner, "base.md", strings.Repeat("# private heading\n\nprivate prose\n", 200), "base")
	mustInit(t, binary, owner, host, testMnemonic)
	mustGit(t, owner, "config", "remote.backup.cloakAutoCompact", "false")
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	writeAndCommit(t, owner, "second.md", strings.Repeat("## another heading\n\nmore private prose\n", 200), "second")
	pushOutput := mustCloakGit(t, binary, owner, "push", "backup", "main")
	if !strings.Contains(pushOutput, "automatic Compaction is disabled") {
		t.Fatalf("push beyond disabled threshold omitted capacity warning:\n%s", pushOutput)
	}

	wantRepositoryID := strings.TrimSpace(mustGit(t, owner, "config", "--get", "remote.backup.cloakRepositoryID"))
	wantMain := strings.TrimSpace(mustGit(t, owner, "rev-parse", "refs/heads/main"))
	wantObjects := mustGit(t, owner, "rev-list", "--objects", "--all")
	wantGeneration := checkpointGeneration(t, binary, owner)
	if got := storageHistoryLength(t, host); got < 3 {
		t.Fatalf("fragmented Storage History length = %d, want at least 3", got)
	}
	if got := len(ciphertextObjectPaths(t, host)); got <= 3 {
		t.Fatalf("fragmented snapshot has %d ciphertext objects, want more than one Pack Payload", got)
	}

	command := exec.Command(binary, "compact", "backup")
	command.Dir = owner
	command.Env = cloakGitEnvironment(binary)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("compact failed: %v\n%s", err, output)
	}
	for _, phase := range []string{"Packing", "Encryption", "Upload", "Validation", "Publication"} {
		if !bytes.Contains(output, []byte(phase)) {
			t.Fatalf("compact output omits %s phase:\n%s", phase, output)
		}
	}
	if !bytes.Contains(output, []byte("retention")) {
		t.Fatalf("compact output omits Repository Host retention warning:\n%s", output)
	}
	for _, protected := range []string{"base.md", "second.md", "private prose", testMnemonic} {
		if bytes.Contains(output, []byte(protected)) {
			t.Fatalf("compact progress exposed Protected Plaintext %q", protected)
		}
	}
	if got := storageHistoryLength(t, host); got != 1 {
		t.Fatalf("compacted Storage History length = %d, want parentless root", got)
	}
	if got := len(ciphertextObjectPaths(t, host)); got != 3 {
		t.Fatalf("compacted snapshot has %d ciphertext objects, want Encrypted Manifest, Encrypted Pack Index, and one Encrypted Pack Chunk", got)
	}
	if got := strings.TrimSpace(mustGit(t, owner, "config", "--get", "remote.backup.cloakRepositoryID")); got != wantRepositoryID {
		t.Fatalf("Repository ID changed from %q to %q", wantRepositoryID, got)
	}
	if got := checkpointGeneration(t, binary, owner); got != wantGeneration+1 {
		t.Fatalf("generation after Compaction = %d, want %d", got, wantGeneration+1)
	}

	mustCloakGit(t, binary, root, "clone", "cloak::"+host, recovered)
	if got := strings.TrimSpace(mustGit(t, recovered, "rev-parse", "refs/remotes/origin/main")); got != wantMain {
		t.Fatalf("recovered main changed from %s to %s", wantMain, got)
	}
	if got := mustGit(t, recovered, "rev-list", "--objects", "--all"); got != wantObjects {
		t.Fatalf("reachable original object IDs changed after Compaction")
	}
	if output, err := exec.Command("git", "-C", recovered, "fsck", "--full").CombinedOutput(); err != nil {
		t.Fatalf("compacted recovery fails git fsck --full: %v\n%s", err, output)
	}
}

func TestPushAutomaticallyCompactsAtAddedCiphertextThreshold(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	owner := filepath.Join(root, "owner")
	host := filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", owner)
	writeAndCommit(t, owner, "first.md", strings.Repeat("# deterministic markdown\n\ninitial paragraph\n", 300), "first")
	mustInit(t, binary, owner, host, testMnemonic)
	mustGit(t, owner, "config", "remote.backup.cloakAutoCompact", "true")
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	writeAndCommit(t, owner, "second.md", strings.Repeat("# deterministic markdown\n\nsecond paragraph\n", 300), "second")

	output := mustCloakGit(t, binary, owner, "push", "backup", "main")
	for _, phase := range []string{"Packing", "Encryption", "Upload", "Validation", "Publication"} {
		if !strings.Contains(output, phase) {
			t.Fatalf("automatic Compaction output omits %s:\n%s", phase, output)
		}
	}
	if got := storageHistoryLength(t, host); got != 1 {
		t.Fatalf("automatic Compaction left Storage History length %d, want 1", got)
	}
	if got := len(ciphertextObjectPaths(t, host)); got != 3 {
		t.Fatalf("automatic Compaction left %d ciphertext objects, want one Pack Payload", got)
	}
}

func TestInterruptedCompactionLeavesPreviousStorageRefAuthoritative(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	owner := filepath.Join(root, "owner")
	host := filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", owner)
	writeAndCommit(t, owner, "first.md", "# first\n", "first")
	mustInit(t, binary, owner, host, testMnemonic)
	mustGit(t, owner, "config", "remote.backup.cloakAutoCompact", "false")
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	writeAndCommit(t, owner, "second.md", "# second\n", "second")
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	wantStorageRef := strings.TrimSpace(mustGit(t, host, "rev-parse", "refs/heads/cloak-storage"))

	command := exec.Command(binary, "compact", "backup")
	command.Dir = owner
	command.Env = append(cloakGitEnvironment(binary), "CLOAK_TEST_FAULT=before-storage-ref")
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("interrupted Compaction succeeded:\n%s", output)
	}
	if got := strings.TrimSpace(mustGit(t, host, "rev-parse", "refs/heads/cloak-storage")); got != wantStorageRef {
		t.Fatalf("interrupted Compaction changed Storage Ref from %s to %s", wantStorageRef, got)
	}

	retry := exec.Command(binary, "compact", "backup")
	retry.Dir = owner
	retry.Env = cloakGitEnvironment(binary)
	if output, err := retry.CombinedOutput(); err != nil {
		t.Fatalf("Compaction retry failed: %v\n%s", err, output)
	}
	if got := storageHistoryLength(t, host); got != 1 {
		t.Fatalf("retried Compaction left Storage History length %d, want 1", got)
	}

	writeAndCommit(t, owner, "third.md", "# third\n", "third")
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	generationBeforeLostResponse := checkpointGeneration(t, binary, owner)
	lostResponse := exec.Command(binary, "compact", "backup")
	lostResponse.Dir = owner
	lostResponse.Env = append(cloakGitEnvironment(binary), "CLOAK_TEST_FAULT=after-storage-ref")
	if output, err := lostResponse.CombinedOutput(); err == nil {
		t.Fatalf("lost-response Compaction reported success:\n%s", output)
	}
	recognizedRetry := exec.Command(binary, "compact", "backup")
	recognizedRetry.Dir = owner
	recognizedRetry.Env = cloakGitEnvironment(binary)
	if output, err := recognizedRetry.CombinedOutput(); err != nil {
		t.Fatalf("lost-response Compaction retry was not recognized: %v\n%s", err, output)
	}
	if got := checkpointGeneration(t, binary, owner); got != generationBeforeLostResponse+1 {
		t.Fatalf("recognized Compaction retry advanced generation to %d, want %d", got, generationBeforeLostResponse+1)
	}
}

func TestCompactSupportsAnEmptyLogicalRepository(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	owner := filepath.Join(root, "owner")
	recovered := filepath.Join(root, "recovered")
	host := filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", owner)
	mustInit(t, binary, owner, host, testMnemonic)
	command := exec.Command(binary, "compact", "backup")
	command.Dir = owner
	command.Env = cloakGitEnvironment(binary)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("empty Compaction failed: %v\n%s", err, output)
	}
	if got := storageHistoryLength(t, host); got != 1 {
		t.Fatalf("empty Compaction left Storage History length %d, want 1", got)
	}
	mustCloakGit(t, binary, root, "clone", "cloak::"+host, recovered)
	if got := mustGit(t, recovered, "for-each-ref", "--format=%(refname)"); got != "" {
		t.Fatalf("empty compacted repository recovered refs %q", got)
	}
}

func TestCompactedStorageTargetIsAnOrdinaryRegressionGate(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	owner := filepath.Join(root, "owner")
	host := filepath.Join(root, "host.git")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", owner)
	for document := 0; document < 200; document++ {
		if err := os.WriteFile(filepath.Join(owner, fmt.Sprintf("doc-%03d.md", document)), []byte(deterministicMarkdown(document, 0)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mustGit(t, owner, "add", ".")
	mustGit(t, owner, "commit", "-m", "deterministic markdown corpus")
	mustInit(t, binary, owner, host, testMnemonic)
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	if err := os.WriteFile(filepath.Join(owner, "doc-000.md"), []byte(deterministicMarkdown(0, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, owner, "add", ".")
	mustGit(t, owner, "commit", "-m", "one markdown update")
	mustCloakGit(t, binary, owner, "push", "backup", "main")
	ordinaryPack := exec.Command("git", "pack-objects", "--stdout", "--all")
	ordinaryPack.Dir = owner
	ordinaryPackBytes, err := ordinaryPack.Output()
	if err != nil {
		t.Fatal(err)
	}
	compact := exec.Command(binary, "compact", "backup")
	compact.Dir = owner
	compact.Env = cloakGitEnvironment(binary)
	if output, err := compact.CombinedOutput(); err != nil {
		t.Fatalf("compact regression fixture: %v\n%s", err, output)
	}
	var compactedBytes int64
	for _, line := range strings.Split(strings.TrimSpace(mustGit(t, host, "ls-tree", "-r", "-l", "refs/heads/cloak-storage", "objects")), "\n") {
		fields := strings.Fields(line)
		size, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		compactedBytes += size
	}
	if ratio := float64(compactedBytes) / float64(len(ordinaryPackBytes)); ratio > 1.15 {
		t.Fatalf("compacted live storage ratio %.3fx materially regressed from accepted 1.04x workload target", ratio)
	}
}

func checkpointGeneration(t *testing.T, binary, repository string) uint64 {
	t.Helper()
	status := exec.Command(binary, "status", "--json")
	status.Dir = repository
	status.Env = cloakGitEnvironment(binary)
	output, err := status.Output()
	if err != nil {
		t.Fatal(err)
	}
	var parsed checkpointStatus
	if err := json.Unmarshal(output, &parsed); err != nil {
		t.Fatal(err)
	}
	return parsed.HighestAuthenticatedGeneration
}
