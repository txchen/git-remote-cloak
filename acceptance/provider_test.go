package acceptance_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProviderCertificationHappyPath(t *testing.T) {
	repositoryURL := providerFixture(t, "CLOAK_PROVIDER_URL")
	binary := certificationBinary(t)
	root := t.TempDir()
	owner := filepath.Join(root, "owner")
	recovered := filepath.Join(root, "recovered")
	restarted := filepath.Join(root, "restarted")
	explicit := filepath.Join(root, "explicit")
	mustGit(t, root, "init", "-b", "main", owner)
	writeAndCommit(t, owner, "provider-secret.txt", "provider certification plaintext\n", "provider certification commit")

	t.Cleanup(func() {
		command := exec.Command("git", "push", repositoryURL, ":refs/heads/cloak-storage")
		command.Env = providerGitEnvironment(binary, "")
		_ = command.Run()
	})

	runProviderCommand(t, binary, owner, testMnemonic, "init", "backup", repositoryURL)
	runGitWithCloak(t, binary, owner, testMnemonic, "push", "backup", "main")
	runProviderCommand(t, binary, root, testMnemonic, "clone", repositoryURL, recovered)
	if got := mustGit(t, recovered, "show", "HEAD:provider-secret.txt"); got != "provider certification plaintext\n" {
		t.Fatalf("Recovered Repository content = %q", got)
	}

	writeAndCommit(t, owner, "after-restart.txt", "restart-safe provider content\n", "restart-safe provider commit")
	runGitWithCloak(t, binary, owner, testMnemonic, "push", "backup", "main")
	runGitWithCloak(t, binary, recovered, testMnemonic, "fetch", "origin")
	mustGit(t, recovered, "reset", "--hard", "origin/main")
	writeAndCommit(t, recovered, "environment-push.txt", "environment source resumed push\n", "environment source push")
	runGitWithCloak(t, binary, recovered, testMnemonic, "push", "origin", "main")
	secretFile := filepath.Join(root, "recovery-secret")
	if err := os.WriteFile(secretFile, []byte(testMnemonic+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runProviderCommandWithSecretFile(t, binary, root, secretFile, "clone", repositoryURL, restarted)
	writeAndCommit(t, recovered, "fetch-after-restart.txt", "fetched after restart\n", "fetch after restart")
	runGitWithCloak(t, binary, recovered, testMnemonic, "push", "origin", "main")
	runGitWithSecretFile(t, binary, restarted, secretFile, "fetch", "origin")
	mustGit(t, restarted, "reset", "--hard", "origin/main")
	writeAndCommit(t, restarted, "file-push.txt", "file source resumed push\n", "file source push")
	runGitWithSecretFile(t, binary, restarted, secretFile, "push", "origin", "main")
	runProviderCommand(t, binary, root, "", "clone", repositoryURL, explicit, "--secret-file", secretFile)

	mustGit(t, restarted, "tag", "provider-certification")
	runGitWithSecretFile(t, binary, restarted, secretFile, "push", "origin", "provider-certification")
	certifyProviderCompareAndSwap(t, binary, root, repositoryURL)
	runProviderCommand(t, binary, restarted, testMnemonic, "compact", "origin")
	assertProviderStorageRootHasNoParents(t, binary, root, repositoryURL, testMnemonic, "Compaction")
	runProviderCommand(t, binary, restarted, otherMnemonic, "rekey", "origin", "--yes")
	runProviderCommand(t, binary, root, otherMnemonic, "clone", repositoryURL, filepath.Join(root, "rekeyed"))
	assertProviderStorageRootHasNoParents(t, binary, root, repositoryURL, otherMnemonic, "Rekey")
	formatCheck := exec.Command(binary, "migrate", "origin")
	formatCheck.Dir = restarted
	formatCheck.Env = providerGitEnvironment(binary, otherMnemonic)
	if output, err := formatCheck.CombinedOutput(); err == nil || !strings.Contains(string(output), "already uses target format v1.0") {
		t.Fatalf("live provider format check did not confirm v1.0: %v\n%s", err, output)
	}

	assertProtectedPlaintextAbsentFromProviderRefs(t, repositoryURL, "provider-secret.txt", "provider certification plaintext")
}

func assertProviderStorageRootHasNoParents(t *testing.T, binary, root, repositoryURL, mnemonic, operation string) {
	t.Helper()
	storageRef := strings.Fields(runGitOutput(t, binary, root, mnemonic, "ls-remote", repositoryURL, "refs/heads/cloak-storage"))
	if len(storageRef) != 2 {
		t.Fatalf("Storage Ref query = %q", storageRef)
	}
	storageInspection := filepath.Join(t.TempDir(), "storage-inspection.git")
	mustGit(t, root, "init", "--bare", storageInspection)
	command := exec.Command("git", "-C", storageInspection, "fetch", repositoryURL, "refs/heads/cloak-storage")
	command.Env = providerGitEnvironment(binary, mnemonic)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fetch Storage Ref for force-re-root check: %v\n%s", err, output)
	}
	if got := strings.Fields(mustGit(t, storageInspection, "show", "-s", "--format=%P", "FETCH_HEAD")); len(got) != 0 {
		t.Fatalf("%s Storage History root has parents: %v", operation, got)
	}
}

func TestProviderCertificationExpectedRejections(t *testing.T) {
	binary := certificationBinary(t)
	for _, test := range []struct {
		name        string
		environment string
	}{
		{name: "Repository Host authentication", environment: "CLOAK_PROVIDER_AUTH_REJECT_URL"},
		{name: "object or quota", environment: "CLOAK_PROVIDER_OBJECT_REJECT_URL"},
		{name: "Storage Ref branch protection", environment: "CLOAK_PROVIDER_PROTECTED_URL"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repositoryURL := providerFixture(t, test.environment)
			workspace := filepath.Join(t.TempDir(), "workspace")
			mustGit(t, t.TempDir(), "init", "-b", "main", workspace)
			command := exec.Command(binary, "init", "backup", repositoryURL)
			command.Dir = workspace
			command.Env = providerGitEnvironment(binary, testMnemonic)
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("%s rejection fixture accepted publication", test.name)
			}
			if strings.Contains(string(output), testMnemonic) {
				t.Fatalf("%s rejection logged the Recovery Secret", test.name)
			}
		})
	}
}

func providerFixture(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value != "" {
		return value
	}
	if os.Getenv("CLOAK_REQUIRE_PROVIDER_CERTIFICATION") == "1" {
		t.Fatalf("required provider fixture %s is not configured", name)
	}
	t.Skip(name + " is not configured")
	return ""
}

func certifyProviderCompareAndSwap(t *testing.T, binary, root, repositoryURL string) {
	t.Helper()
	writers := []string{filepath.Join(root, "writer-a"), filepath.Join(root, "writer-b")}
	branches := []string{"provider-cas-a", "provider-cas-b"}
	for index, writer := range writers {
		runProviderCommand(t, binary, root, testMnemonic, "clone", repositoryURL, writer)
		mustGit(t, writer, "switch", "-c", branches[index])
		writeAndCommit(t, writer, "concurrent.txt", []string{"writer a\n", "writer b\n"}[index], "concurrent provider writer")
	}
	commands := make([]*exec.Cmd, len(writers))
	outputs := make([]bytes.Buffer, len(writers))
	barrier := t.TempDir()
	for index, writer := range writers {
		commands[index] = exec.Command("git", "-C", writer, "push", "origin", branches[index])
		commands[index].Env = append(providerGitEnvironment(binary, testMnemonic),
			"CLOAK_TEST_STORAGE_REF_BARRIER="+barrier,
			"CLOAK_TEST_STORAGE_REF_PARTICIPANT="+branches[index],
		)
		commands[index].Stdout = &outputs[index]
		commands[index].Stderr = &outputs[index]
		if err := commands[index].Start(); err != nil {
			t.Fatalf("start concurrent provider push: %v", err)
		}
	}
	waitForStorageRefBarrier(t, barrier, branches...)
	if err := os.WriteFile(filepath.Join(barrier, "release"), []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for index, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatalf("compatible provider push %s did not retry after the forced CAS race: %v\n%s", branches[index], err, outputs[index].String())
		}
		if strings.Contains(outputs[index].String(), testMnemonic) {
			t.Fatal("concurrent provider push logged the Recovery Secret")
		}
	}
	verification := filepath.Join(root, "cas-verification")
	runProviderCommand(t, binary, root, testMnemonic, "clone", repositoryURL, verification)
	for index, branch := range branches {
		if got, want := mustGit(t, verification, "rev-parse", "origin/"+branch), mustGit(t, writers[index], "rev-parse", branch); got != want {
			t.Fatalf("compatible provider ref %s = %q, want %q", branch, got, want)
		}
	}
}

func certificationBinary(t *testing.T) string {
	t.Helper()
	if binary := os.Getenv("CLOAK_BINARY"); binary != "" {
		return binary
	}
	return buildBinary(t)
}

func providerGitEnvironment(binary, mnemonic string) []string {
	environment := append(withoutEnvironment(os.Environ(), "CLOAK_RECOVERY_SECRET", "CLOAK_RECOVERY_SECRET_FILE"),
		"PATH="+filepath.Dir(binary)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GIT_CONFIG_NOSYSTEM=1",
	)
	if mnemonic != "" {
		environment = append(environment, "CLOAK_RECOVERY_SECRET="+mnemonic)
	}
	return environment
}

func runProviderCommand(t *testing.T, binary, directory, mnemonic string, arguments ...string) {
	t.Helper()
	command := exec.Command(binary, arguments...)
	command.Dir = directory
	command.Env = providerGitEnvironment(binary, mnemonic)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git-remote-cloak %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}

func runProviderCommandWithSecretFile(t *testing.T, binary, directory, secretFile string, arguments ...string) {
	t.Helper()
	command := exec.Command(binary, arguments...)
	command.Dir = directory
	command.Env = append(providerGitEnvironment(binary, ""), "CLOAK_RECOVERY_SECRET_FILE="+secretFile)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git-remote-cloak %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}

func runGitWithCloak(t *testing.T, binary, directory, mnemonic string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	command.Env = providerGitEnvironment(binary, mnemonic)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}

func runGitWithSecretFile(t *testing.T, binary, directory, secretFile string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	command.Env = append(providerGitEnvironment(binary, ""), "CLOAK_RECOVERY_SECRET_FILE="+secretFile)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}

func runGitOutput(t *testing.T, binary, directory, mnemonic string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	command.Env = providerGitEnvironment(binary, mnemonic)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return string(output)
}

func assertProtectedPlaintextAbsentFromProviderRefs(t *testing.T, repositoryURL string, protected ...string) {
	t.Helper()
	inspection := filepath.Join(t.TempDir(), "inspection.git")
	mustGit(t, t.TempDir(), "init", "--bare", inspection)
	command := exec.Command("git", "-C", inspection, "fetch", repositoryURL, "+refs/*:refs/*")
	command.Env = os.Environ()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fetch provider refs for privacy inspection: %v\n%s", err, output)
	}
	objectList := mustGit(t, inspection, "rev-list", "--objects", "--all")
	objectIDs := make([]string, 0)
	for _, line := range strings.Split(strings.TrimSpace(objectList), "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			objectIDs = append(objectIDs, fields[0])
		}
	}
	for _, value := range protected {
		for _, objectID := range objectIDs {
			output, err := exec.Command("git", "-C", inspection, "cat-file", "-p", objectID).CombinedOutput()
			if err != nil {
				t.Fatalf("inspect provider object %s: %v", objectID, err)
			}
			if strings.Contains(string(output), value) {
				t.Fatalf("provider object %s exposed Protected Plaintext %q", objectID, value)
			}
		}
	}
}
