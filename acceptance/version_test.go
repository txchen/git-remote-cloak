package acceptance_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type formatReport struct {
	Formats []struct {
		Major              uint64   `json:"major"`
		Minor              uint64   `json:"minor"`
		Read               bool     `json:"read"`
		Write              bool     `json:"write"`
		CryptographicSuite string   `json:"cryptographic_suite"`
		RequiredFeatures   []string `json:"required_features"`
	} `json:"formats"`
}

func TestVersionFormatsReportsExactV1Capability(t *testing.T) {
	binary := buildBinary(t)
	command := exec.Command(binary, "version", "--formats")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("version --formats failed: %v\n%s", err, output)
	}

	want := "v1.0 read=yes write=yes cryptographic-suite=aes-256-gcm-siv required-features=none\n"
	if string(output) != want {
		t.Fatalf("version --formats output = %q, want %q", output, want)
	}
}

func TestVersionFormatsJSONReportsExactV1Capability(t *testing.T) {
	binary := buildBinary(t)
	command := exec.Command(binary, "version", "--formats", "--json")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("version --formats --json failed: %v\n%s", err, output)
	}

	var report formatReport
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("decode version report: %v\n%s", err, output)
	}
	if len(report.Formats) != 1 {
		t.Fatalf("format count = %d, want 1", len(report.Formats))
	}
	format := report.Formats[0]
	if format.Major != 1 || format.Minor != 0 || !format.Read || !format.Write ||
		format.CryptographicSuite != "aes-256-gcm-siv" || len(format.RequiredFeatures) != 0 {
		t.Fatalf("unexpected v1 format report: %+v", format)
	}
}

func buildBinary(t *testing.T) string {
	t.Helper()
	repositoryRoot := filepath.Clean(filepath.Join(".."))
	binary := filepath.Join(t.TempDir(), "git-remote-cloak")
	command := exec.Command("go", "build", "-o", binary, "./cmd/git-remote-cloak")
	command.Dir = repositoryRoot
	command.Env = append(os.Environ(), "CGO_ENABLED=0")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build git-remote-cloak: %v\n%s", err, output)
	}
	return binary
}

func withoutEnvironment(environment []string, names ...string) []string {
	blocked := make(map[string]struct{}, len(names))
	for _, name := range names {
		blocked[name] = struct{}{}
	}
	clean := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if _, found := blocked[name]; !found {
			clean = append(clean, entry)
		}
	}
	return clean
}
