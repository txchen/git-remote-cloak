package gitdb

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// RejectLFSPointers rejects supported refs that depend on Git LFS content. It
// reports only affected local paths, never pointer contents or object IDs.
func RejectLFSPointers(gitDirectory string, candidateObjectIDs []string) error {
	if len(candidateObjectIDs) == 0 {
		return nil
	}
	pointers, err := lfsPointerObjects(gitDirectory, candidateObjectIDs)
	if err != nil || len(pointers) == 0 {
		return err
	}
	commits, err := run(gitDirectory, nil, "rev-list", "--all")
	if err != nil {
		return fmt.Errorf("enumerate commits for Git LFS validation: %w", err)
	}
	affected := make(map[string]struct{})
	for _, commit := range strings.Fields(string(commits)) {
		tree, err := run(gitDirectory, nil, "ls-tree", "-r", "-z", "--full-tree", commit)
		if err != nil {
			return fmt.Errorf("inspect commit for Git LFS content: %w", err)
		}
		for _, entry := range bytes.Split(tree, []byte{0}) {
			metadata, path, found := bytes.Cut(entry, []byte{'\t'})
			if !found {
				continue
			}
			fields := strings.Fields(string(metadata))
			if len(fields) != 3 || fields[1] != "blob" {
				continue
			}
			objectID := fields[2]
			if pointers[objectID] {
				affected[string(path)] = struct{}{}
			}
		}
	}
	if len(affected) == 0 {
		return nil
	}
	paths := make([]string, 0, len(affected))
	for path := range affected {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return fmt.Errorf("Git LFS is unsupported; affected paths: %s", strings.Join(paths, ", "))
}

func lfsPointerObjects(gitDirectory string, candidateObjectIDs []string) (map[string]bool, error) {
	pointers := make(map[string]bool)
	// Bound each response when a repository has many small files.
	for start := 0; start < len(candidateObjectIDs); start += 512 {
		end := min(start+512, len(candidateObjectIDs))
		ids := candidateObjectIDs[start:end]
		output, err := run(gitDirectory, []byte(strings.Join(ids, "\n")+"\n"), "cat-file", "--batch-check=%(objectname) %(objecttype) %(objectsize)")
		if err != nil {
			return nil, fmt.Errorf("inspect possible Git LFS pointers: %w", err)
		}
		lines := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
		if len(lines) != len(ids) {
			return nil, errors.New("Git returned malformed batch object metadata")
		}
		smallBlobs := make([]string, 0)
		for index, line := range lines {
			fields := strings.Fields(line)
			if len(fields) != 3 || fields[0] != ids[index] {
				return nil, errors.New("Git returned malformed batch object metadata")
			}
			size, err := strconv.ParseInt(fields[2], 10, 64)
			if err != nil || size < 0 {
				return nil, errors.New("Git returned malformed batch object metadata")
			}
			if fields[1] == "blob" && size <= 1024 {
				smallBlobs = append(smallBlobs, ids[index])
			}
		}
		if len(smallBlobs) == 0 {
			continue
		}
		contents, err := run(gitDirectory, []byte(strings.Join(smallBlobs, "\n")+"\n"), "cat-file", "--batch")
		if err != nil {
			return nil, fmt.Errorf("read possible Git LFS pointers: %w", err)
		}
		reader := bufio.NewReader(bytes.NewReader(contents))
		for _, id := range smallBlobs {
			header, err := reader.ReadString('\n')
			if err != nil {
				return nil, errors.New("Git returned malformed batch blob contents")
			}
			fields := strings.Fields(header)
			if len(fields) != 3 || fields[0] != id || fields[1] != "blob" {
				return nil, errors.New("Git returned malformed batch blob contents")
			}
			size, err := strconv.ParseInt(fields[2], 10, 64)
			if err != nil || size < 0 || size > 1024 {
				return nil, errors.New("Git returned malformed batch blob contents")
			}
			blob := make([]byte, size)
			if _, err := io.ReadFull(reader, blob); err != nil {
				return nil, errors.New("Git returned malformed batch blob contents")
			}
			separator, err := reader.ReadByte()
			if err != nil || separator != '\n' {
				return nil, errors.New("Git returned malformed batch blob contents")
			}
			if isLFSPointerContents(blob) {
				pointers[id] = true
			}
		}
		if _, err := reader.ReadByte(); err != io.EOF {
			return nil, errors.New("Git returned malformed batch blob contents")
		}
	}
	return pointers, nil
}

// RejectPromisorState fails closed when a local Git object database is marked
// as partial or promisor-backed, even if its currently requested objects happen
// to be present.
func RejectPromisorState(gitDirectory string) error {
	for _, arguments := range [][]string{
		{"config", "--get", "extensions.partialclone"},
		{"config", "--get-regexp", `^remote\..*\.partialclonefilter$`},
	} {
		output, found, err := runOptional(gitDirectory, arguments...)
		if err != nil {
			return fmt.Errorf("inspect partial clone state: %w", err)
		}
		if found && strings.TrimSpace(string(output)) != "" {
			return errors.New("partial clone filters and promisor-object repositories are unsupported")
		}
	}
	promisorConfig, found, err := runOptional(gitDirectory, "config", "--bool", "--get-regexp", `^remote\..*\.promisor$`)
	if err != nil {
		return fmt.Errorf("inspect promisor configuration: %w", err)
	}
	if found {
		for _, line := range strings.Split(strings.TrimSpace(string(promisorConfig)), "\n") {
			if strings.HasSuffix(line, " true") {
				return errors.New("partial clone filters and promisor-object repositories are unsupported")
			}
		}
	}
	promisorFiles, err := filepath.Glob(filepath.Join(gitDirectory, "objects", "pack", "*.promisor"))
	if err != nil {
		return fmt.Errorf("inspect promisor object state: %w", err)
	}
	if len(promisorFiles) > 0 {
		return errors.New("partial clone filters and promisor-object repositories are unsupported")
	}
	return nil
}

func isLFSPointerContents(contents []byte) bool {
	lines := strings.Split(strings.TrimSuffix(string(contents), "\n"), "\n")
	if len(lines) < 3 || lines[0] != "version https://git-lfs.github.com/spec/v1" {
		return false
	}
	index := 1
	extensions := make(map[string]struct{})
	for index < len(lines) && strings.HasPrefix(lines[index], "ext-") {
		key, valid := validLFSExtension(lines[index])
		if !valid {
			return false
		}
		if _, duplicate := extensions[key]; duplicate {
			return false
		}
		extensions[key] = struct{}{}
		index++
	}
	if index+2 != len(lines) || !strings.HasPrefix(lines[index], "oid sha256:") {
		return false
	}
	digest := strings.TrimPrefix(lines[index], "oid sha256:")
	if len(digest) != 64 || !isLowerHex(digest) {
		return false
	}
	value, found := strings.CutPrefix(lines[index+1], "size ")
	if !found || value == "" || len(value) > 1 && value[0] == '0' {
		return false
	}
	_, err := strconv.ParseUint(value, 10, 64)
	return err == nil
}

func validLFSExtension(line string) (string, bool) {
	key, value, found := strings.Cut(line, " ")
	if !found || value == "" {
		return "", false
	}
	priorityAndName := strings.TrimPrefix(key, "ext-")
	priority, name, found := strings.Cut(priorityAndName, "-")
	if !found || priority == "" || name == "" {
		return "", false
	}
	if _, err := strconv.ParseUint(priority, 10, 64); err != nil {
		return "", false
	}
	for _, character := range name {
		if unicode.IsSpace(character) {
			return "", false
		}
	}
	return key, true
}

func isLowerHex(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}
