package acceptance_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type migrationPlanReport struct {
	CurrentFormat             string `json:"current_format"`
	TargetFormat              string `json:"target_format"`
	LogicalRefCount           int    `json:"logical_ref_count"`
	EstimatedFullUploadBytes  uint64 `json:"estimated_full_upload_bytes"`
	CompatibilityCheck        string `json:"compatibility_check"`
	CapacityCheck             string `json:"capacity_check"`
	WriterCompatibilityEffect string `json:"writer_compatibility_effect"`
}

func TestFormatMigrationRebuildsValidatedParentlessCiphertextSnapshot(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	owner := filepath.Join(root, "owner")
	host := filepath.Join(root, "host.git")
	recovered := filepath.Join(root, "recovered")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, root, "init", "-b", "main", owner)
	writeAndCommit(t, owner, "private.md", "# migration preserves this\n", "base")
	mustGit(t, owner, "tag", "v1")
	mustInit(t, binary, owner, host, testMnemonic)
	mustCloakGit(t, binary, owner, "push", "backup", "main", "--tags")

	wantRepositoryID := strings.TrimSpace(mustGit(t, owner, "config", "--get", "remote.backup.cloakRepositoryID"))
	wantRefs := mustGit(t, owner, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads", "refs/tags")
	wantObjects := mustGit(t, owner, "rev-list", "--objects", "--all")
	wantGeneration := checkpointGeneration(t, binary, owner)
	wantStorageRef := strings.TrimSpace(mustGit(t, host, "rev-parse", "refs/heads/cloak-storage"))

	dryRun := exec.Command(binary, "migrate", "backup", "--to", "test-v2.0", "--dry-run", "--json")
	dryRun.Dir = owner
	dryRun.Env = append(cloakGitEnvironment(binary), "CLOAK_TEST_FORMATS=1")
	output, err := dryRun.Output()
	if err != nil {
		t.Fatalf("Migration dry-run failed: %v\n%s", err, output)
	}
	var plan migrationPlanReport
	if err := json.Unmarshal(output, &plan); err != nil {
		t.Fatalf("decode Migration plan: %v\n%s", err, output)
	}
	if plan.CurrentFormat != "v1.0" || plan.TargetFormat != "test-v2.0" || plan.LogicalRefCount != 2 ||
		plan.EstimatedFullUploadBytes == 0 || plan.CompatibilityCheck != "passed" || plan.CapacityCheck != "passed" ||
		plan.WriterCompatibilityEffect == "" {
		t.Fatalf("incomplete Migration dry-run plan: %+v", plan)
	}
	assertStorageRef(t, host, wantStorageRef)

	migrate := exec.Command(binary, "migrate", "backup", "--to", "test-v2.0", "--yes")
	migrate.Dir = owner
	migrate.Env = append(cloakGitEnvironment(binary), "CLOAK_TEST_FORMATS=1")
	output, err = migrate.CombinedOutput()
	if err != nil {
		t.Fatalf("Migration failed: %v\n%s", err, output)
	}
	for _, detail := range []string{"v1.0", "test-v2.0", "2 Logical Refs", "Estimated full upload", "writer compatibility", "Packing", "Encryption", "Validation", "Upload", "Publication"} {
		if !strings.Contains(string(output), detail) {
			t.Fatalf("Migration output omitted %q:\n%s", detail, output)
		}
	}
	if got := storageHistoryLength(t, host); got != 1 {
		t.Fatalf("migrated Storage History length = %d, want one parentless root", got)
	}
	if got := strings.TrimSpace(mustGit(t, owner, "config", "--get", "remote.backup.cloakRepositoryID")); got != wantRepositoryID {
		t.Fatalf("Migration changed Repository ID from %s to %s", wantRepositoryID, got)
	}
	if got := checkpointGeneration(t, binary, owner); got != wantGeneration+1 {
		t.Fatalf("Migration checkpoint generation = %d, want %d", got, wantGeneration+1)
	}
	if bootstrap := mustGit(t, host, "show", "refs/heads/cloak-storage:bootstrap"); len(bootstrap) < 10 || bootstrap[9] != 2 {
		t.Fatalf("Migration did not publish test format Bootstrap Preamble: %x", []byte(bootstrap))
	}

	clone := exec.Command(binary, "clone", host, recovered)
	clone.Env = append(cloakGitEnvironment(binary), "CLOAK_TEST_FORMATS=1")
	if cloneOutput, err := clone.CombinedOutput(); err != nil {
		t.Fatalf("migrated repository cannot be recovered: %v\n%s", err, cloneOutput)
	}
	if got := mustGit(t, recovered, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads", "refs/tags"); got != wantRefs {
		t.Fatalf("migrated refs differ:\nwant:\n%s\ngot:\n%s", wantRefs, got)
	}
	if got := mustGit(t, recovered, "rev-list", "--objects", "--all"); got != wantObjects {
		t.Fatal("migrated reachable original Git object IDs differ")
	}
	if fsckOutput, err := exec.Command("git", "-C", recovered, "fsck", "--full").CombinedOutput(); err != nil {
		t.Fatalf("migrated recovery fails git fsck --full: %v\n%s", err, fsckOutput)
	}

	unsupported := exec.Command(binary, "clone", host, filepath.Join(root, "unsupported"))
	unsupported.Env = cloakGitEnvironment(binary)
	if unsupportedOutput, err := unsupported.CombinedOutput(); err == nil || !strings.Contains(string(unsupportedOutput), "unsupported repository format") {
		t.Fatalf("production reader did not fail closed for test format: %v\n%s", err, unsupportedOutput)
	}

	writeAndCommit(t, owner, "later.md", "# ordinary push stays migrated\n", "later")
	push := exec.Command("git", "push", "backup", "main")
	push.Dir = owner
	push.Env = append(cloakGitEnvironment(binary), "CLOAK_TEST_FORMATS=1")
	if pushOutput, err := push.CombinedOutput(); err != nil {
		t.Fatalf("ordinary push after Migration failed: %v\n%s", err, pushOutput)
	}
	if bootstrap := mustGit(t, host, "show", "refs/heads/cloak-storage:bootstrap"); len(bootstrap) < 10 || bootstrap[9] != 2 {
		t.Fatalf("ordinary push automatically changed repository format: %x", []byte(bootstrap))
	}
	compact := exec.Command(binary, "compact", "backup")
	compact.Dir = owner
	compact.Env = append(cloakGitEnvironment(binary), "CLOAK_TEST_FORMATS=1")
	if compactOutput, err := compact.CombinedOutput(); err != nil {
		t.Fatalf("Compaction after Migration failed: %v\n%s", err, compactOutput)
	}
	if bootstrap := mustGit(t, host, "show", "refs/heads/cloak-storage:bootstrap"); len(bootstrap) < 10 || bootstrap[9] != 2 {
		t.Fatalf("Compaction automatically changed repository format: %x", []byte(bootstrap))
	}

	downgrade := exec.Command(binary, "migrate", "backup", "--to", "v1.0", "--dry-run", "--json")
	downgrade.Dir = owner
	downgrade.Env = append(cloakGitEnvironment(binary), "CLOAK_TEST_FORMATS=1")
	if downgradeOutput, err := downgrade.CombinedOutput(); err == nil || !strings.Contains(string(downgradeOutput), "downgrade") {
		t.Fatalf("in-place downgrade was not rejected: %v\n%s", err, downgradeOutput)
	}
}

func TestFormatMigrationRequiresPinnedUnattendedTargetAndAbortsConcurrentUpdate(t *testing.T) {
	binary, _, owner, host, storageRef := rekeyFixture(t)

	for _, arguments := range [][]string{{"migrate", "backup", "--yes"}, {"migrate", "backup", "--dry-run", "--json"}} {
		command := exec.Command(binary, arguments...)
		command.Dir = owner
		command.Env = append(cloakGitEnvironment(binary), "CLOAK_TEST_FORMATS=1")
		if output, err := command.CombinedOutput(); err == nil || !strings.Contains(string(output), "--to") {
			t.Fatalf("unpinned unattended Migration %v was accepted: %v\n%s", arguments, err, output)
		}
	}

	cancelled := exec.Command(binary, "migrate", "backup")
	cancelled.Dir = owner
	cancelled.Env = append(cloakGitEnvironment(binary), "CLOAK_TEST_FORMATS=1")
	cancelled.Stdin = strings.NewReader("cancel\n")
	if output, err := cancelled.CombinedOutput(); err == nil || !strings.Contains(string(output), "v1.0 -> test-v2.0") ||
		!strings.Contains(string(output), "Logical Refs") || !strings.Contains(string(output), "Estimated full upload") ||
		!strings.Contains(string(output), "compatibility") || !strings.Contains(string(output), "cancelled") {
		t.Fatalf("human Migration cancellation did not show the complete plan: %v\n%s", err, output)
	}
	assertStorageRef(t, host, storageRef)

	interrupted := exec.Command(binary, "migrate", "backup", "--to", "test-v2.0", "--yes")
	interrupted.Dir = owner
	interrupted.Env = append(cloakGitEnvironment(binary), "CLOAK_TEST_FORMATS=1", "CLOAK_TEST_FAULT=before-storage-ref")
	if output, err := interrupted.CombinedOutput(); err == nil {
		t.Fatalf("interrupted Migration succeeded:\n%s", output)
	}
	assertStorageRef(t, host, storageRef)

	command := exec.Command(binary, "migrate", "backup", "--to", "test-v2.0", "--yes")
	command.Dir = owner
	command.Env = append(cloakGitEnvironment(binary), "CLOAK_TEST_FORMATS=1", "CLOAK_TEST_FAULT=stale-storage-ref")
	if output, err := command.CombinedOutput(); err == nil || !strings.Contains(string(output), "concurrently") {
		t.Fatalf("concurrent Migration did not abort: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(mustGit(t, host, "rev-parse", "refs/heads/cloak-storage")); got == storageRef {
		t.Fatal("concurrency fixture did not publish its competing update")
	}
}

func TestVersionNeverAdvertisesTestMigrationFormat(t *testing.T) {
	binary := buildBinary(t)
	command := exec.Command(binary, "version", "--formats")
	command.Env = append(os.Environ(), "CLOAK_TEST_FORMATS=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(output), "test-v2") || string(output) != "v1.0 read=yes write=yes cryptographic-suite=aes-256-gcm-siv required-features=none\n" {
		t.Fatalf("version advertised test-only format: %s", output)
	}
}

func TestFormatMigrationCrashJournalRecognizesPublishedSnapshot(t *testing.T) {
	binary, _, owner, host, oldStorageRef := rekeyFixture(t)
	wantGeneration := checkpointGeneration(t, binary, owner)
	crash := exec.Command(binary, "migrate", "backup", "--to", "test-v2.0", "--yes")
	crash.Dir = owner
	crash.Env = append(cloakGitEnvironment(binary), "CLOAK_TEST_FORMATS=1", "CLOAK_TEST_FAULT=lost-process-after-storage-ref")
	if output, err := crash.CombinedOutput(); err == nil {
		t.Fatalf("injected post-publication process loss succeeded:\n%s", output)
	}
	newStorageRef := strings.TrimSpace(mustGit(t, host, "rev-parse", "refs/heads/cloak-storage"))
	if newStorageRef == oldStorageRef {
		t.Fatal("post-publication process loss occurred before Storage Ref publication")
	}
	if got := checkpointGeneration(t, binary, owner); got != wantGeneration {
		t.Fatalf("process loss advanced checkpoint to %d before publication recovery, want %d", got, wantGeneration)
	}

	retry := exec.Command(binary, "migrate", "backup", "--to", "test-v2.0", "--yes")
	retry.Dir = owner
	retry.Env = append(cloakGitEnvironment(binary), "CLOAK_TEST_FORMATS=1")
	if output, err := retry.CombinedOutput(); err != nil {
		t.Fatalf("Migration retry did not recognize published crash journal: %v\n%s", err, output)
	}
	assertStorageRef(t, host, newStorageRef)
	if got := checkpointGeneration(t, binary, owner); got != wantGeneration+1 {
		t.Fatalf("recovered Migration checkpoint generation = %d, want %d", got, wantGeneration+1)
	}
}
