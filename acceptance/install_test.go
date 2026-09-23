package acceptance_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallerSelectsAndVerifiesReleaseBeforeReplacingBinary(t *testing.T) {
	for _, test := range []struct {
		name, system, machine, platform, architecture string
		pinned, corrupt, missing, unsupported         bool
	}{
		{name: "linux amd64 latest", system: "Linux", machine: "x86_64", platform: "linux", architecture: "amd64"},
		{name: "linux arm64 pinned", system: "Linux", machine: "aarch64", platform: "linux", architecture: "arm64", pinned: true},
		{name: "mac intel", system: "Darwin", machine: "x86_64", platform: "darwin", architecture: "amd64"},
		{name: "mac apple silicon", system: "Darwin", machine: "arm64", platform: "darwin", architecture: "arm64"},
		{name: "checksum mismatch", system: "Linux", machine: "x86_64", platform: "linux", architecture: "amd64", corrupt: true},
		{name: "failed download", system: "Linux", machine: "x86_64", platform: "linux", architecture: "amd64", missing: true},
		{name: "unsupported architecture", system: "Linux", machine: "riscv64", unsupported: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			fakeBin, destination, temporary := filepath.Join(root, "commands"), filepath.Join(root, "install dir"), filepath.Join(root, "temporary")
			for _, directory := range []string{fakeBin, destination, temporary} {
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			writeInstallerFixture(t, filepath.Join(fakeBin, "uname"), "#!/usr/bin/env bash\ncase \"$1\" in -s) echo \"$INSTALL_TEST_SYSTEM\" ;; -m) echo \"$INSTALL_TEST_MACHINE\" ;; esac\n", 0o700)
			writeInstallerFixture(t, filepath.Join(fakeBin, "curl"), `#!/usr/bin/env bash
set -eu
url='' output=''
while [[ $# -gt 0 ]]; do
  case "$1" in
    https://*) url="$1"; shift ;;
    -o) output="$2"; shift 2 ;;
    --proto|--tlsv1.2|--retry)
      if [[ "$1" == --tlsv1.2 ]]; then shift; else shift 2; fi ;;
    *) shift ;;
  esac
done
printf '%s\n' "$url" >>"$INSTALL_TEST_ROOT/requests"
case "$url" in
  */checksums.txt) cp "$INSTALL_TEST_ROOT/checksums.txt" "$output" ;;
  */git-remote-cloak_*) cp "$INSTALL_TEST_ROOT/archive.tar.gz" "$output" ;;
  *) exit 22 ;;
esac
`, 0o700)
			binary := []byte("#!/usr/bin/env bash\nprintf executed >\"$INSTALL_TEST_ROOT/executed\"\necho 'git-remote-cloak v1.2.3'\n")
			var archive bytes.Buffer
			compressed := gzip.NewWriter(&archive)
			writer := tar.NewWriter(compressed)
			if err := writer.WriteHeader(&tar.Header{Name: "git-remote-cloak", Mode: 0o755, Size: int64(len(binary))}); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Write(binary); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := compressed.Close(); err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(archive.Bytes())
			if test.corrupt {
				digest[0] ^= 0xff
			}
			archiveName := "git-remote-cloak_v1.2.3_" + test.platform + "_" + test.architecture + ".tar.gz"
			writeInstallerFixture(t, filepath.Join(root, "checksums.txt"), fmt.Sprintf("%x  %s\n", digest, archiveName), 0o600)
			if !test.missing {
				writeInstallerFixture(t, filepath.Join(root, "archive.tar.gz"), archive.String(), 0o600)
			}
			installed := filepath.Join(destination, "git-remote-cloak")
			writeInstallerFixture(t, installed, "previous binary", 0o755)
			script, err := os.ReadFile(filepath.Join("..", "install.sh"))
			if err != nil {
				t.Fatal(err)
			}
			command := exec.Command("bash")
			command.Stdin = bytes.NewReader(script) // Exercise curl | bash semantics.
			command.Env = append(withoutEnvironment(os.Environ(), "CLOAK_VERSION", "CLOAK_INSTALL_DIR"),
				"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"CLOAK_INSTALL_DIR="+destination, "TMPDIR="+temporary,
				"INSTALL_TEST_ROOT="+root, "INSTALL_TEST_SYSTEM="+test.system, "INSTALL_TEST_MACHINE="+test.machine)
			if test.pinned {
				command.Env = append(command.Env, "CLOAK_VERSION=v1.2.3")
			}
			output, runErr := command.CombinedOutput()
			got, err := os.ReadFile(installed)
			if err != nil {
				t.Fatal(err)
			}
			if test.corrupt || test.missing || test.unsupported {
				if runErr == nil {
					t.Fatalf("invalid install succeeded:\n%s", output)
				}
				if string(got) != "previous binary" {
					t.Fatal("failed install changed existing binary")
				}
				if _, err := os.Stat(filepath.Join(root, "executed")); !os.IsNotExist(err) {
					t.Fatal("unverified binary was executed")
				}
			} else {
				if runErr != nil {
					t.Fatalf("install failed: %v\n%s", runErr, output)
				}
				if !bytes.Equal(got, binary) {
					t.Fatal("wrong binary installed")
				}
				requests, err := os.ReadFile(filepath.Join(root, "requests"))
				if err != nil {
					t.Fatal(err)
				}
				manifestPath := "/latest/download/checksums.txt"
				if test.pinned {
					manifestPath = "/download/v1.2.3/checksums.txt"
				}
				if !strings.Contains(string(requests), manifestPath) || !strings.Contains(string(requests), "/download/v1.2.3/"+archiveName) {
					t.Fatalf("incorrect download URLs: %s", requests)
				}
			}
			entries, err := os.ReadDir(temporary)
			if err != nil || len(entries) != 0 {
				t.Fatalf("temporary downloads were not cleaned up: %v, %v", entries, err)
			}
			entries, err = os.ReadDir(destination)
			if err != nil || len(entries) != 1 {
				t.Fatalf("staged binary was not cleaned up: %v, %v", entries, err)
			}
		})
	}
}

func writeInstallerFixture(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
}
