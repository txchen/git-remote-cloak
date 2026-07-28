// Package gitdb isolates native Git pack creation and Logical Repository restoration.
package gitdb

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/txchen/git-remote-cloak/internal/domain"
	cloakformat "github.com/txchen/git-remote-cloak/internal/format"
)

// State is the Logical Repository state authenticated by an Encrypted Manifest.
type State struct {
	LogicalHEAD  domain.LogicalHEAD
	ObjectFormat string
	LogicalRefs  map[string]string
}

// ReadState reads the branches and tags from a bare Logical Repository.
func ReadState(gitDirectory string) (State, error) {
	head, err := run(gitDirectory, nil, "symbolic-ref", "HEAD")
	if err != nil {
		return State{}, fmt.Errorf("read Logical HEAD: %w", err)
	}
	objectFormat, refs, err := readObjectFormatAndRefs(gitDirectory)
	if err != nil {
		return State{}, err
	}
	for name := range refs {
		if !domain.LogicalRefName(name).IsStorable() {
			return State{}, errors.New("Logical Repository contains an unsupported ref")
		}
	}
	return State{LogicalHEAD: domain.LogicalHEAD(strings.TrimSpace(string(head))), ObjectFormat: objectFormat, LogicalRefs: refs}, nil
}

// ReadSelectedState selects every local head and tag plus refs matched by the
// explicit refspecs. Remote-tracking and local operational refs are never
// selectable as Logical Refs.
func ReadSelectedState(gitDirectory string, refspecs []string) (State, error) {
	head, err := run(gitDirectory, nil, "symbolic-ref", "HEAD")
	if err != nil {
		return State{}, errors.New("Rekey requires a symbolic local HEAD")
	}
	objectFormat, available, err := readObjectFormatAndRefs(gitDirectory)
	if err != nil {
		return State{}, err
	}
	selected := make(map[string]string)
	for name, objectID := range available {
		if domain.LogicalRefName(name).IsSupported() {
			selected[name] = objectID
		}
	}
	for _, refspec := range refspecs {
		matched, err := applySelectionRefspec(selected, available, refspec)
		if err != nil {
			return State{}, err
		}
		if !matched {
			return State{}, fmt.Errorf("explicit Rekey refspec %q matches no local ref", refspec)
		}
	}
	logicalHEAD := strings.TrimSpace(string(head))
	if _, exists := selected[logicalHEAD]; !exists {
		return State{}, errors.New("Logical HEAD must select a local branch included by Rekey")
	}
	return State{LogicalHEAD: domain.LogicalHEAD(logicalHEAD), ObjectFormat: objectFormat, LogicalRefs: selected}, nil
}

func readObjectFormatAndRefs(gitDirectory string) (string, map[string]string, error) {
	objectFormat, err := run(gitDirectory, nil, "rev-parse", "--show-object-format")
	if err != nil {
		return "", nil, fmt.Errorf("read Git object format: %w", err)
	}
	output, err := run(gitDirectory, nil, "for-each-ref", "--format=%(refname) %(objectname)")
	if err != nil {
		return "", nil, fmt.Errorf("enumerate local refs: %w", err)
	}
	refs := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return "", nil, errors.New("Git returned malformed local ref metadata")
		}
		refs[fields[0]] = fields[1]
	}
	return strings.TrimSpace(string(objectFormat)), refs, nil
}

func applySelectionRefspec(selected, available map[string]string, refspec string) (bool, error) {
	if refspec == "" || strings.HasPrefix(refspec, "+") || strings.HasPrefix(refspec, ":") {
		return false, fmt.Errorf("invalid Rekey refspec %q", refspec)
	}
	source, destination, hasDestination := strings.Cut(refspec, ":")
	if strings.Contains(destination, ":") || !strings.HasPrefix(source, "refs/") || strings.Count(source, "*") > 1 {
		return false, fmt.Errorf("invalid Rekey refspec %q", refspec)
	}
	if !hasDestination {
		destination = source
	}
	if strings.Count(destination, "*") != strings.Count(source, "*") {
		return false, fmt.Errorf("invalid Rekey refspec %q", refspec)
	}
	sourcePrefix, sourceSuffix, wildcard := strings.Cut(source, "*")
	destinationPrefix, destinationSuffix, _ := strings.Cut(destination, "*")
	matched := false
	for name, objectID := range available {
		middle := ""
		if wildcard {
			if !strings.HasPrefix(name, sourcePrefix) || !strings.HasSuffix(name, sourceSuffix) || len(name) < len(sourcePrefix)+len(sourceSuffix) {
				continue
			}
			middle = name[len(sourcePrefix) : len(name)-len(sourceSuffix)]
		} else if name != source {
			continue
		}
		if !domain.LogicalRefName(name).IsStorable() {
			return false, fmt.Errorf("Rekey cannot select remote-tracking or operational ref %s", name)
		}
		target := destinationPrefix + middle + destinationSuffix
		if !domain.LogicalRefName(target).IsStorable() {
			return false, fmt.Errorf("Rekey refspec %q has an invalid destination", refspec)
		}
		if existing, exists := selected[target]; exists && existing != objectID {
			return false, fmt.Errorf("Rekey refspec %q collides at %s", refspec, target)
		}
		selected[target] = objectID
		matched = true
	}
	return matched, nil
}

// RemoteTrackingWithoutLocal returns remote-tracking branches whose short
// branch name is absent from local heads.
func RemoteTrackingWithoutLocal(gitDirectory string) ([]string, error) {
	output, err := run(gitDirectory, nil, "for-each-ref", "--format=%(refname)", "refs/remotes")
	if err != nil {
		return nil, fmt.Errorf("enumerate remote-tracking branches: %w", err)
	}
	var warnings []string
	for _, remoteRef := range strings.Fields(string(output)) {
		parts := strings.SplitN(strings.TrimPrefix(remoteRef, "refs/remotes/"), "/", 2)
		if len(parts) != 2 || parts[1] == "HEAD" {
			continue
		}
		_, found, err := runOptional(gitDirectory, "show-ref", "--verify", "--quiet", "refs/heads/"+parts[1])
		if err != nil {
			return nil, err
		} else if !found {
			warnings = append(warnings, remoteRef)
		}
	}
	sort.Strings(warnings)
	return warnings, nil
}

// CreatePack creates one self-contained native pack and its exact reachable object index.
func CreatePack(gitDirectory string) (cloakformat.PackPayload, error) {
	objectIDs, err := ReachableObjectIDs(gitDirectory)
	if err != nil {
		return cloakformat.PackPayload{}, err
	}
	return CreatePackForObjects(gitDirectory, objectIDs)
}

// CreatePackForObjects creates a self-contained native pack for exactly the selected objects.
func CreatePackForObjects(gitDirectory string, objectIDs []string) (cloakformat.PackPayload, error) {
	if len(objectIDs) == 0 {
		return cloakformat.PackPayload{}, errors.New("cannot create an empty Pack Payload")
	}
	input := []byte(strings.Join(objectIDs, "\n") + "\n")
	pack, err := run(gitDirectory, input, "pack-objects", "--stdout", "--no-reuse-delta", "--no-reuse-object")
	if err != nil {
		return cloakformat.PackPayload{}, fmt.Errorf("create self-contained native Git Pack Payload: %w", err)
	}
	return cloakformat.PackPayload{Pack: pack, ObjectIDs: objectIDs}, nil
}

// ReachableObjectIDs returns the exact sorted object IDs reachable from supported Logical Refs.
func ReachableObjectIDs(gitDirectory string) ([]string, error) {
	return reachableObjectIDs(gitDirectory)
}

// ReachableObjectIDsForRefs returns exactly the objects reachable from refs.
func ReachableObjectIDsForRefs(gitDirectory string, refs map[string]string) ([]string, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	input := make([]string, 0, len(refs))
	for _, objectID := range refs {
		input = append(input, objectID)
	}
	sort.Strings(input)
	output, err := run(gitDirectory, []byte(strings.Join(input, "\n")+"\n"), "rev-list", "--objects", "--stdin")
	if err != nil {
		return nil, fmt.Errorf("enumerate selected reachable Git objects: %w", err)
	}
	seen := make(map[string]struct{})
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			seen[fields[0]] = struct{}{}
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// Import adds authenticated Pack Payloads to an existing local Git object database.
func Import(gitDirectory string, packs []cloakformat.PackPayload) error {
	for _, payload := range packs {
		allPresent := true
		for _, objectID := range payload.ObjectIDs {
			if _, err := run(gitDirectory, nil, "cat-file", "-e", objectID+"^{object}"); err != nil {
				allPresent = false
				break
			}
		}
		if allPresent {
			continue
		}
		if _, err := run(gitDirectory, payload.Pack, "index-pack", "--stdin"); err != nil {
			return fmt.Errorf("import native Git Pack Payload: %w", err)
		}
		for _, objectID := range payload.ObjectIDs {
			if _, err := run(gitDirectory, nil, "cat-file", "-e", objectID+"^{object}"); err != nil {
				return fmt.Errorf("Encrypted Pack Index references missing Git object %s", objectID)
			}
		}
	}
	return nil
}

// Restore imports authenticated packs, restores refs and HEAD, and fully validates the result.
func Restore(destination string, bare bool, state State, packs []cloakformat.PackPayload) error {
	return restore(destination, bare, !bare, state, packs)
}

// RestoreForClone prepares a non-bare object database and refs while leaving checkout to Git clone.
func RestoreForClone(destination string, state State, packs []cloakformat.PackPayload) error {
	gitDirectory, err := initializeAndImport(destination, false, state, packs)
	if err != nil {
		return err
	}
	if _, err := run(gitDirectory, nil, "fsck", "--full"); err != nil {
		return fmt.Errorf("validate Recovered Repository objects: %w", err)
	}
	return nil
}

// ValidateLogicalRepository reconstructs the decoded logical state in isolation
// and enforces supported-content rules before objects enter a caller's repository.
func ValidateLogicalRepository(state State, packs []cloakformat.PackPayload) error {
	temporaryRoot, err := os.MkdirTemp("", "git-remote-cloak-validation-")
	if err != nil {
		return fmt.Errorf("create Logical Repository validation directory: %w", err)
	}
	defer os.RemoveAll(temporaryRoot)
	return Restore(filepath.Join(temporaryRoot, "repository.git"), true, state, packs)
}

func restore(destination string, bare, checkout bool, state State, packs []cloakformat.PackPayload) error {
	gitDirectory, err := initializeAndImport(destination, bare, state, packs)
	if err != nil {
		return err
	}
	refNames := make([]string, 0, len(state.LogicalRefs))
	for name := range state.LogicalRefs {
		refNames = append(refNames, name)
	}
	sort.Strings(refNames)
	for _, name := range refNames {
		if _, err := run(gitDirectory, nil, "update-ref", name, state.LogicalRefs[name]); err != nil {
			return fmt.Errorf("restore Logical Ref %s: %w", name, err)
		}
	}
	for name, want := range state.LogicalRefs {
		got, err := run(gitDirectory, nil, "rev-parse", "--verify", name)
		if err != nil || strings.TrimSpace(string(got)) != want {
			return fmt.Errorf("restored Logical Ref %s does not match Encrypted Manifest", name)
		}
	}
	wantObjects := indexedObjectIDs(packs)
	gotObjects, err := reachableObjectIDs(gitDirectory)
	if err != nil {
		return err
	}
	indexed := make(map[string]struct{}, len(wantObjects))
	for _, objectID := range wantObjects {
		indexed[objectID] = struct{}{}
	}
	for _, objectID := range gotObjects {
		if _, exists := indexed[objectID]; !exists {
			return errors.New("Recovered Repository reachable object set disagrees with Encrypted Pack Index")
		}
	}
	if err := RejectLFSPointers(gitDirectory, gotObjects); err != nil {
		return err
	}
	if _, err := run(gitDirectory, nil, "fsck", "--full"); err != nil {
		return fmt.Errorf("validate Recovered Repository: %w", err)
	}
	if checkout {
		if target, exists := state.LogicalRefs[string(state.LogicalHEAD)]; exists {
			if _, err := runWorkTree(destination, "reset", "--hard", target); err != nil {
				return fmt.Errorf("check out Recovered Repository: %w", err)
			}
		}
	}
	return nil
}

func initializeAndImport(destination string, bare bool, state State, packs []cloakformat.PackPayload) (string, error) {
	arguments := []string{"init", "--object-format=" + state.ObjectFormat}
	if bare {
		arguments = append(arguments, "--bare")
	}
	arguments = append(arguments, "-b", strings.TrimPrefix(string(state.LogicalHEAD), "refs/heads/"), destination)
	initialize := exec.Command("git", arguments...)
	initialize.Env = cleanEnvironment()
	if output, err := initialize.CombinedOutput(); err != nil {
		return "", fmt.Errorf("initialize Recovered Repository: %s", strings.TrimSpace(string(output)))
	}
	gitDirectory := destinationGitDirectory(destination, bare)
	if err := Import(gitDirectory, packs); err != nil {
		return "", err
	}
	if _, err := run(gitDirectory, nil, "symbolic-ref", "HEAD", string(state.LogicalHEAD)); err != nil {
		return "", fmt.Errorf("restore Logical HEAD: %w", err)
	}
	return gitDirectory, nil
}

func runWorkTree(workTree string, arguments ...string) ([]byte, error) {
	fullArguments := append([]string{"--git-dir=" + destinationGitDirectory(workTree, false), "--work-tree=" + workTree}, arguments...)
	command := exec.Command("git", fullArguments...)
	command.Env = cleanEnvironment()
	output, err := command.Output()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("git %s: %s", strings.Join(arguments, " "), strings.TrimSpace(string(exitError.Stderr)))
		}
		return nil, err
	}
	return output, nil
}

func reachableObjectIDs(gitDirectory string) ([]string, error) {
	output, err := run(gitDirectory, nil, "rev-list", "--objects", "--all")
	if err != nil {
		return nil, fmt.Errorf("enumerate reachable Git objects: %w", err)
	}
	seen := make(map[string]struct{})
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			seen[fields[0]] = struct{}{}
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func indexedObjectIDs(packs []cloakformat.PackPayload) []string {
	seen := make(map[string]struct{})
	for _, payload := range packs {
		for _, id := range payload.ObjectIDs {
			seen[id] = struct{}{}
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func destinationGitDirectory(destination string, bare bool) string {
	if bare {
		return destination
	}
	return destination + string(os.PathSeparator) + ".git"
}

type gitRunOptions struct {
	additionalEnvironment []string
	exitOneMeansMissing   bool
}

func run(gitDirectory string, stdin []byte, arguments ...string) ([]byte, error) {
	output, _, err := runWithOptions(gitDirectory, stdin, gitRunOptions{}, arguments...)
	return output, err
}

func runOptional(gitDirectory string, arguments ...string) ([]byte, bool, error) {
	return runWithOptions(gitDirectory, nil, gitRunOptions{exitOneMeansMissing: true}, arguments...)
}

func runWithOptions(gitDirectory string, stdin []byte, options gitRunOptions, arguments ...string) ([]byte, bool, error) {
	fullArguments := append([]string{"--git-dir=" + gitDirectory}, arguments...)
	command := exec.Command("git", fullArguments...)
	command.Env = append(cleanEnvironment(), options.additionalEnvironment...)
	if stdin != nil {
		command.Stdin = bytes.NewReader(stdin)
	}
	output, err := command.Output()
	if err == nil {
		return output, true, nil
	}
	if exitError, ok := err.(*exec.ExitError); ok && options.exitOneMeansMissing && exitError.ExitCode() == 1 {
		return nil, false, nil
	}
	if exitError, ok := err.(*exec.ExitError); ok {
		return nil, false, fmt.Errorf("git %s: %s", strings.Join(arguments, " "), strings.TrimSpace(string(exitError.Stderr)))
	}
	return nil, false, err
}

func cleanEnvironment() []string {
	environment := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if name != "GIT_DIR" && name != "GIT_WORK_TREE" && name != "GIT_INDEX_FILE" && name != "GIT_OBJECT_DIRECTORY" && name != "GIT_ALTERNATE_OBJECT_DIRECTORIES" {
			environment = append(environment, entry)
		}
	}
	return append(environment, "GIT_CONFIG_NOSYSTEM=1")
}
