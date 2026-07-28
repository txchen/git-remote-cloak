package acceptance_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// BenchmarkMarkdownHeavyCompaction is deterministic workload evidence for the
// v1 capacity target; it is not a universal storage or transfer guarantee.
func BenchmarkMarkdownHeavyCompaction(b *testing.B) {
	for iteration := 0; iteration < b.N; iteration++ {
		binary := benchmarkBuildBinary(b)
		root := b.TempDir()
		owner := filepath.Join(root, "owner")
		host := filepath.Join(root, "host.git")
		benchmarkGit(b, root, "init", "--bare", host)
		benchmarkGit(b, root, "init", "-b", "main", owner)
		for document := 0; document < 200; document++ {
			benchmarkWriteMarkdown(b, owner, document, 0)
		}
		benchmarkGit(b, owner, "add", ".")
		benchmarkGit(b, owner, "commit", "-m", "initial markdown corpus")
		benchmarkRun(b, owner, cloakGitEnvironment(binary), binary, "init", "backup", host)
		benchmarkGit(b, owner, "config", "remote.backup.cloakAutoCompact", "false")

		knownObjects := make(map[string]struct{})
		var cumulativeTransfer int64
		for update := 0; update < 8; update++ {
			benchmarkWriteMarkdown(b, owner, update, update+1)
			benchmarkGit(b, owner, "add", ".")
			benchmarkGit(b, owner, "commit", "-m", fmt.Sprintf("markdown update %02d", update+1))
			benchmarkRun(b, owner, cloakGitEnvironment(binary), "git", "push", "backup", "main")
			for path, size := range benchmarkCiphertextObjects(b, host) {
				if _, exists := knownObjects[path]; !exists {
					knownObjects[path] = struct{}{}
					cumulativeTransfer += size
				}
			}
		}

		fragmentedObjects := benchmarkCiphertextObjects(b, host)
		fragmentedLive := sumBenchmarkSizes(fragmentedObjects)
		fragmentedHistory := benchmarkReachableStorageBytes(b, host)
		ordinaryPack := int64(len(benchmarkGitBytes(b, owner, "pack-objects", "--stdout", "--all")))
		benchmarkRun(b, owner, cloakGitEnvironment(binary), binary, "compact", "backup")
		compactedObjects := benchmarkCiphertextObjects(b, host)
		compactedLive := sumBenchmarkSizes(compactedObjects)
		compactedHistory := benchmarkReachableStorageBytes(b, host)
		if len(compactedObjects) != 3 {
			b.Fatalf("Compaction left %d ciphertext objects, want one Encrypted Manifest, Encrypted Pack Index, and Encrypted Pack Chunk", len(compactedObjects))
		}
		ratio := float64(compactedLive) / float64(ordinaryPack)
		if ratio > 1.15 {
			b.Fatalf("compacted live storage ratio %.3fx materially regressed from accepted 1.04x target", ratio)
		}
		b.ReportMetric(float64(fragmentedLive), "fragmented-live-bytes")
		b.ReportMetric(float64(fragmentedHistory), "fragmented-history-bytes")
		b.ReportMetric(float64(compactedLive), "compacted-live-bytes")
		b.ReportMetric(float64(compactedHistory), "compacted-history-bytes")
		b.ReportMetric(float64(cumulativeTransfer), "cumulative-transfer-bytes")
		b.ReportMetric(float64((len(fragmentedObjects)-1)/2), "fragmented-pack-payloads")
		b.ReportMetric(1, "compacted-pack-payloads")
		b.ReportMetric(ratio, "compacted/ordinary-pack")
	}
}

func benchmarkWriteMarkdown(b *testing.B, repository string, document, revision int) {
	b.Helper()
	if err := os.WriteFile(filepath.Join(repository, fmt.Sprintf("doc-%03d.md", document)), []byte(deterministicMarkdown(document, revision)), 0o600); err != nil {
		b.Fatal(err)
	}
}

func deterministicMarkdown(document, revision int) string {
	var contents strings.Builder
	fmt.Fprintf(&contents, "# Document %03d\n\n", document)
	for paragraph := 0; paragraph < 20; paragraph++ {
		fmt.Fprintf(&contents, "## Section %02d\n\nDeterministic prose for document %03d, revision %02d, paragraph %02d. Links use [the local reference](doc-%03d.md).\n\n", paragraph, document, revision, paragraph, (document+paragraph+1)%200)
	}
	return contents.String()
}

func benchmarkBuildBinary(b *testing.B) string {
	b.Helper()
	binary := filepath.Join(b.TempDir(), "git-remote-cloak")
	benchmarkRun(b, filepath.Clean(".."), append(os.Environ(), "CGO_ENABLED=0"), "go", "build", "-o", binary, "./cmd/git-remote-cloak")
	return binary
}

func benchmarkRun(b *testing.B, directory string, environment []string, name string, arguments ...string) []byte {
	b.Helper()
	command := exec.Command(name, arguments...)
	command.Dir = directory
	command.Env = environment
	output, err := command.CombinedOutput()
	if err != nil {
		b.Fatalf("%s %v failed: %v\n%s", name, arguments, err, output)
	}
	return output
}

func benchmarkGit(b *testing.B, directory string, arguments ...string) string {
	b.Helper()
	return string(benchmarkGitBytes(b, directory, arguments...))
}

func benchmarkGitBytes(b *testing.B, directory string, arguments ...string) []byte {
	b.Helper()
	environment := append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Cloak Benchmark", "GIT_AUTHOR_EMAIL=benchmark@example.invalid", "GIT_COMMITTER_NAME=Cloak Benchmark", "GIT_COMMITTER_EMAIL=benchmark@example.invalid")
	return benchmarkRun(b, directory, environment, "git", arguments...)
}

func benchmarkCiphertextObjects(b *testing.B, host string) map[string]int64 {
	b.Helper()
	output := benchmarkGit(b, host, "ls-tree", "-r", "-l", "refs/heads/cloak-storage", "objects")
	objects := make(map[string]int64)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		size, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil {
			b.Fatal(err)
		}
		objects[fields[4]] = size
	}
	return objects
}

func benchmarkReachableStorageBytes(b *testing.B, host string) int64 {
	b.Helper()
	objects := benchmarkGit(b, host, "rev-list", "--objects", "refs/heads/cloak-storage")
	seen := make(map[string]struct{})
	var total int64
	for _, line := range strings.Split(strings.TrimSpace(objects), "\n") {
		objectID := strings.Fields(line)[0]
		if _, exists := seen[objectID]; exists {
			continue
		}
		seen[objectID] = struct{}{}
		sizeText := bytes.TrimSpace(benchmarkGitBytes(b, host, "cat-file", "-s", objectID))
		size, err := strconv.ParseInt(string(sizeText), 10, 64)
		if err != nil {
			b.Fatal(err)
		}
		total += size
	}
	return total
}

func sumBenchmarkSizes(objects map[string]int64) int64 {
	var total int64
	for _, size := range objects {
		total += size
	}
	return total
}
