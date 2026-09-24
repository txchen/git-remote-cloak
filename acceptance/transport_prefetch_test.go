package acceptance_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoOpPushBatchesFilteredStorageBlobFetches(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	host := filepath.Join(root, "host.git")
	workspace := filepath.Join(root, "workspace")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, host, "config", "uploadpack.allowFilter", "true")
	mustGit(t, root, "init", "-b", "main", workspace)
	writeAndCommit(t, workspace, "private.txt", "protected\n", "first")
	mustInit(t, binary, workspace, "file://"+host, testMnemonic)
	mustCloakGit(t, binary, workspace, "push", "backup", "main")

	tracePath := filepath.Join(root, "trace.json")
	command := exec.Command("git", "push", "backup", "main")
	command.Dir = workspace
	command.Env = append(cloakGitEnvironment(binary), "GIT_TRACE2_EVENT="+tracePath)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "Everything up-to-date") {
		t.Fatalf("no-op push failed: %v\n%s", err, output)
	}
	trace, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	uploadPacks := 0
	for _, line := range bytes.Split(trace, []byte{'\n'}) {
		var event struct {
			Event string   `json:"event"`
			Argv  []string `json:"argv"`
		}
		if len(line) == 0 || json.Unmarshal(line, &event) != nil || event.Event != "start" || len(event.Argv) == 0 {
			continue
		}
		if filepath.Base(event.Argv[0]) == "git-upload-pack" {
			uploadPacks++
		}
	}
	if uploadPacks < 1 || uploadPacks > 2 {
		t.Fatalf("no-op push opened %d upload-pack sessions, want at most 2", uploadPacks)
	}
}
