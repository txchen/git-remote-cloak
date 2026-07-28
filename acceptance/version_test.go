package acceptance_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type capabilityReport struct {
	Major              uint64   `json:"major"`
	Minor              uint64   `json:"minor"`
	Read               bool     `json:"read"`
	Write              bool     `json:"write"`
	CryptographicSuite string   `json:"cryptographic_suite"`
	RequiredFeatures   []string `json:"required_features"`
}

type formatReport struct {
	Formats []capabilityReport `json:"formats"`
}

type versionReport struct {
	Version   string             `json:"version"`
	Commit    string             `json:"commit"`
	Built     string             `json:"built"`
	GoVersion string             `json:"go_version"`
	Platform  string             `json:"platform"`
	CGo       string             `json:"cgo"`
	Formats   []capabilityReport `json:"formats"`
}

func TestVersionReportsReleaseBuildAndExactFormatCapabilities(t *testing.T) {
	binary := buildBinaryWithLinkerFlags(t,
		"-X main.buildVersion=v1.2.3",
		"-X main.buildCommit=0123456789abcdef",
		"-X main.buildDate=2026-07-27T12:34:56Z",
		"-X main.buildCGo=disabled",
	)
	command := exec.Command(binary, "version", "--json")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("version --json failed: %v\n%s", err, output)
	}

	var report versionReport
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("decode version report: %v\n%s", err, output)
	}
	if report.Version != "v1.2.3" || report.Commit != "0123456789abcdef" ||
		report.Built != "2026-07-27T12:34:56Z" || report.CGo != "disabled" {
		t.Fatalf("unexpected release identity: %+v", report)
	}
	if report.GoVersion == "" || report.Platform == "" {
		t.Fatalf("version omitted toolchain or target platform: %+v", report)
	}
	if len(report.Formats) != 1 || report.Formats[0].Major != 1 || report.Formats[0].Minor != 0 ||
		!report.Formats[0].Read || !report.Formats[0].Write ||
		report.Formats[0].CryptographicSuite != "aes-256-gcm-siv" || len(report.Formats[0].RequiredFeatures) != 0 {
		t.Fatalf("unexpected format capabilities: %+v", report.Formats)
	}
}

func TestVersionReportsHumanReadableBuildIdentity(t *testing.T) {
	binary := buildBinaryWithLinkerFlags(t,
		"-X main.buildVersion=v1.2.3",
		"-X main.buildCommit=0123456789abcdef",
		"-X main.buildDate=2026-07-27T12:34:56Z",
		"-X main.buildCGo=disabled",
	)
	output, err := exec.Command(binary, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("version failed: %v\n%s", err, output)
	}
	for _, want := range []string{
		"git-remote-cloak v1.2.3\n",
		"commit: 0123456789abcdef\n",
		"built: 2026-07-27T12:34:56Z\n",
		"cgo: disabled\n",
		"v1.0 read=yes write=yes cryptographic-suite=aes-256-gcm-siv required-features=none\n",
	} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("version output does not contain %q:\n%s", want, output)
		}
	}
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
	return buildBinaryWithLinkerFlags(t)
}

func buildBinaryWithLinkerFlags(t *testing.T, linkerFlags ...string) string {
	t.Helper()
	repositoryRoot := filepath.Clean(filepath.Join(".."))
	binary := filepath.Join(t.TempDir(), "git-remote-cloak")
	arguments := []string{"build"}
	if len(linkerFlags) > 0 {
		arguments = append(arguments, "-ldflags", strings.Join(linkerFlags, " "))
	}
	arguments = append(arguments, "-o", binary, "./cmd/git-remote-cloak")
	command := exec.Command("go", arguments...)
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
