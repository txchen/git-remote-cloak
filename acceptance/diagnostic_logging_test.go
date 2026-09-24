package acceptance_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOptInDiagnosticsExplainPushWithoutLoggingProtectedInputs(t *testing.T) {
	binary := buildBinary(t)
	root := t.TempDir()
	host := filepath.Join(root, "host.git")
	workspace := filepath.Join(root, "workspace")
	mustGit(t, root, "init", "--bare", host)
	mustGit(t, host, "config", "uploadpack.allowFilter", "true")
	mustGit(t, root, "init", "-b", "main", workspace)
	writeAndCommit(t, workspace, "private-file-name.txt", "protected contents\n", "private commit message")
	mustInit(t, binary, workspace, "file://"+host, testMnemonic)
	if output := mustCloakGit(t, binary, workspace, "push", "backup", "main"); strings.Contains(output, "cloak debug +") {
		t.Fatalf("diagnostics were enabled by default:\n%s", output)
	}

	writeAndCommit(t, workspace, "private-file-name.txt", "new protected contents\n", "another private message")
	for _, noOp := range []bool{false, true} {
		command := exec.Command("git", "push", "backup", "main")
		command.Dir = workspace
		command.Env = append(cloakGitEnvironment(binary), "CLOAK_LOG=debug")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("diagnostic push failed: %v\n%s", err, output)
		}
		log := string(output)
		for _, marker := range []string{
			"cloak debug +", "remote helper invoked", "recovery secret loaded", "remote helper started", "remote inspection started",
			"storage clone started", "storage clone ended after",
			"storage blob prefetch started", "missing storage blobs=",
			"snapshot decode ended after", "remote inspection completed",
		} {
			if !strings.Contains(log, marker) {
				t.Fatalf("diagnostic push missing %q:\n%s", marker, log)
			}
		}
		if noOp {
			if !strings.Contains(log, "Everything up-to-date") {
				t.Fatalf("no-op push lacked completion message:\n%s", log)
			}
		} else {
			for _, marker := range []string{"push attempt started", "candidate validation started", "storage ref publication started"} {
				if !strings.Contains(log, marker) {
					t.Fatalf("publication log missing %q:\n%s", marker, log)
				}
			}
		}
		for _, protected := range []string{testMnemonic, "private-file-name.txt", "private commit message", "another private message", "protected contents"} {
			if strings.Contains(log, protected) {
				t.Fatalf("diagnostic push logged protected input %q", protected)
			}
		}
	}
}
