package gitdb

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// RejectLFSPointers rejects supported refs that depend on Git LFS content. It
// reports only affected local paths, never pointer contents or object IDs.
func RejectLFSPointers(gitDirectory string, candidateObjectIDs []string) error {
	candidates := make(map[string]struct{}, len(candidateObjectIDs))
	for _, objectID := range candidateObjectIDs {
		candidates[objectID] = struct{}{}
	}
	if len(candidates) == 0 {
		return nil
	}
	commits, err := run(gitDirectory, nil, "rev-list", "--all")
	if err != nil {
		return fmt.Errorf("enumerate commits for Git LFS validation: %w", err)
	}
	affected := make(map[string]struct{})
	checked := make(map[string]bool)
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
			if _, wanted := candidates[objectID]; !wanted {
				continue
			}
			isPointer, exists := checked[objectID]
			if !exists {
				isPointer, err = isLFSPointer(gitDirectory, objectID)
				if err != nil {
					return err
				}
				checked[objectID] = isPointer
			}
			if isPointer {
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

func isLFSPointer(gitDirectory, objectID string) (bool, error) {
	sizeOutput, err := run(gitDirectory, nil, "cat-file", "-s", objectID)
	if err != nil {
		return false, fmt.Errorf("inspect possible Git LFS pointer: %w", err)
	}
	size, err := strconv.ParseInt(strings.TrimSpace(string(sizeOutput)), 10, 64)
	if err != nil {
		return false, fmt.Errorf("parse Git blob size: %w", err)
	}
	if size > 1024 {
		return false, nil
	}
	contents, err := run(gitDirectory, nil, "cat-file", "blob", objectID)
	if err != nil {
		return false, fmt.Errorf("read possible Git LFS pointer: %w", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(contents), "\n"), "\n")
	if len(lines) < 3 || lines[0] != "version https://git-lfs.github.com/spec/v1" {
		return false, nil
	}
	index := 1
	extensions := make(map[string]struct{})
	for index < len(lines) && strings.HasPrefix(lines[index], "ext-") {
		key, valid := validLFSExtension(lines[index])
		if !valid {
			return false, nil
		}
		if _, duplicate := extensions[key]; duplicate {
			return false, nil
		}
		extensions[key] = struct{}{}
		index++
	}
	if index+2 != len(lines) || !strings.HasPrefix(lines[index], "oid sha256:") {
		return false, nil
	}
	digest := strings.TrimPrefix(lines[index], "oid sha256:")
	if len(digest) != 64 || !isLowerHex(digest) {
		return false, nil
	}
	value, found := strings.CutPrefix(lines[index+1], "size ")
	if !found || value == "" || len(value) > 1 && value[0] == '0' {
		return false, nil
	}
	_, err = strconv.ParseUint(value, 10, 64)
	return err == nil, nil
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
