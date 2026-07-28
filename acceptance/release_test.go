package acceptance_test

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleaseBuilderProducesPinnedCGoFreeArtifactsAndChecksums(t *testing.T) {
	repositoryRoot := filepath.Clean("..")
	destination := t.TempDir()
	command := exec.Command("bash", "scripts/build-release.sh", "v1.2.3", destination)
	command.Dir = repositoryRoot
	command.Env = append(os.Environ(), "SOURCE_DATE_EPOCH=1785155696")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build release: %v\n%s", err, output)
	}

	wantArchives := []string{
		"git-remote-cloak_v1.2.3_darwin_amd64.tar.gz",
		"git-remote-cloak_v1.2.3_darwin_arm64.tar.gz",
		"git-remote-cloak_v1.2.3_linux_amd64.tar.gz",
		"git-remote-cloak_v1.2.3_linux_arm64.tar.gz",
	}
	checksums := readChecksums(t, filepath.Join(destination, "checksums.txt"))
	if len(checksums) != len(wantArchives) {
		t.Fatalf("checksum count = %d, want %d", len(checksums), len(wantArchives))
	}
	for _, name := range wantArchives {
		archive := filepath.Join(destination, name)
		contents, err := os.ReadFile(archive)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		digest := sha256.Sum256(contents)
		if got := checksums[name]; got != hex.EncodeToString(digest[:]) {
			t.Fatalf("checksum for %s = %q, want %x", name, got, digest)
		}
	}

	hostArchive := filepath.Join(destination, "git-remote-cloak_v1.2.3_"+runtime.GOOS+"_"+runtime.GOARCH+".tar.gz")
	binary := extractReleaseBinary(t, hostArchive)
	output, err := exec.Command(binary, "version", "--json").CombinedOutput()
	if err != nil {
		t.Fatalf("run release binary: %v\n%s", err, output)
	}
	var report versionReport
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("decode release version: %v\n%s", err, output)
	}
	if report.Version != "v1.2.3" || report.CGo != "disabled" || report.Platform != runtime.GOOS+"/"+runtime.GOARCH {
		t.Fatalf("release identity = %+v", report)
	}
	if report.Commit == "" || report.Commit == "unknown" || report.Built == "" || report.Built == "unknown" {
		t.Fatalf("release omits source identity: %+v", report)
	}
	smoke := exec.Command("bash", "scripts/smoke-release.sh", binary)
	smoke.Dir = repositoryRoot
	if output, err := smoke.CombinedOutput(); err != nil {
		t.Fatalf("smoke release binary: %v\n%s", err, output)
	}
}

func readChecksums(t *testing.T, path string) map[string]string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	checksums := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			t.Fatalf("invalid checksum line %q", scanner.Text())
		}
		checksums[fields[1]] = fields[0]
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return checksums
}

func extractReleaseBinary(t *testing.T, archive string) string {
	t.Helper()
	file, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	header, err := tarReader.Next()
	if err != nil {
		t.Fatal(err)
	}
	if header.Name != "git-remote-cloak" || header.Mode&0o111 == 0 {
		t.Fatalf("release archive entry = %q mode %o", header.Name, header.Mode)
	}
	destination := filepath.Join(t.TempDir(), "git-remote-cloak")
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(output, tarReader); err != nil {
		output.Close()
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := tarReader.Next(); err != io.EOF {
		t.Fatalf("release archive has unexpected extra entry: %v", err)
	}
	return destination
}
