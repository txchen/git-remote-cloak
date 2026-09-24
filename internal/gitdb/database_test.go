package gitdb

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	cloakformat "github.com/txchen/git-remote-cloak/internal/format"
)

func TestCreatePackForObjectsDoesNotRequireAnOlderPackForDeltaBases(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "repository")
	mustRunGit(t, root, nil, "init", "-b", "main", repository)
	mustRunGit(t, repository, nil, "config", "user.name", "Cloak Test")
	mustRunGit(t, repository, nil, "config", "user.email", "cloak@example.invalid")
	writeGitFile(t, repository, "document.md", strings.Repeat("shared base line\n", 200))
	mustRunGit(t, repository, nil, "add", ".")
	mustRunGit(t, repository, nil, "commit", "-m", "base")
	baseObjects, err := ReachableObjectIDs(filepath.Join(repository, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	writeGitFile(t, repository, "document.md", strings.Repeat("shared base line\n", 199)+"new line\n")
	mustRunGit(t, repository, nil, "commit", "-am", "incremental")
	allObjects, err := ReachableObjectIDs(filepath.Join(repository, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	base := make(map[string]struct{}, len(baseObjects))
	for _, objectID := range baseObjects {
		base[objectID] = struct{}{}
	}
	newObjects := make([]string, 0)
	for _, objectID := range allObjects {
		if _, exists := base[objectID]; !exists {
			newObjects = append(newObjects, objectID)
		}
	}
	payload, err := CreatePackForObjects(filepath.Join(repository, ".git"), newObjects)
	if err != nil {
		t.Fatal(err)
	}
	isolated := filepath.Join(root, "isolated.git")
	mustRunGit(t, root, nil, "init", "--bare", isolated)
	mustRunGit(t, root, payload.Pack, "--git-dir="+isolated, "index-pack", "--stdin")
	for _, objectID := range newObjects {
		mustRunGit(t, root, nil, "--git-dir="+isolated, "cat-file", "-e", objectID+"^{object}")
	}
}

func TestImportAndLFSValidationBatchObjectReads(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "source")
	mustRunGit(t, root, nil, "init", "-b", "main", repository)
	mustRunGit(t, repository, nil, "config", "user.name", "Cloak Test")
	mustRunGit(t, repository, nil, "config", "user.email", "cloak@example.invalid")
	for index := range 100 {
		writeGitFile(t, repository, fmt.Sprintf("file-%03d.txt", index), fmt.Sprintf("content %d\n", index))
	}
	mustRunGit(t, repository, nil, "add", ".")
	mustRunGit(t, repository, nil, "commit", "-m", "one hundred files")
	sourceGit := filepath.Join(repository, ".git")
	payload, err := CreatePack(sourceGit)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "destination.git")
	mustRunGit(t, root, nil, "init", "--bare", destination)
	trace := filepath.Join(root, "git-trace.json")
	t.Setenv("GIT_TRACE2_EVENT", trace)
	if err := Import(destination, []cloakformat.PackPayload{payload}); err != nil {
		t.Fatal(err)
	}
	// A second import must detect that every object is already present.
	if err := Import(destination, []cloakformat.PackPayload{payload}); err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(mustRunGit(t, repository, nil, "rev-parse", "HEAD"))
	mustRunGit(t, root, nil, "--git-dir="+destination, "update-ref", "refs/heads/main", head)
	if err := RejectLFSPointers(destination, payload.ObjectIDs); err != nil {
		t.Fatal(err)
	}
	log, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	starts := 0
	for _, line := range bytes.Split(log, []byte{'\n'}) {
		if bytes.Contains(line, []byte(`"event":"start"`)) && bytes.Contains(line, []byte(`"cat-file"`)) {
			starts++
		}
	}
	if got := starts; got != 5 {
		t.Fatalf("Git cat-file process count = %d, want 5 for 100 files", got)
	}
}

func writeGitFile(t *testing.T, repository, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repository, name), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustRunGit(t *testing.T, directory string, input []byte, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", arguments, err, output)
	}
	return string(output)
}
