// Package gitdb isolates native Git pack creation and Logical Repository restoration.
package gitdb

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	objectFormat, err := run(gitDirectory, nil, "rev-parse", "--show-object-format")
	if err != nil {
		return State{}, fmt.Errorf("read Git object format: %w", err)
	}
	refOutput, err := run(gitDirectory, nil, "for-each-ref", "--format=%(refname) %(objectname)")
	if err != nil {
		return State{}, fmt.Errorf("read Logical Refs: %w", err)
	}
	refs := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(refOutput)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || !domain.LogicalRefName(fields[0]).IsSupported() {
			return State{}, errors.New("Logical Repository contains an unsupported ref")
		}
		refs[fields[0]] = fields[1]
	}
	return State{LogicalHEAD: domain.LogicalHEAD(strings.TrimSpace(string(head))), ObjectFormat: strings.TrimSpace(string(objectFormat)), LogicalRefs: refs}, nil
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

func run(gitDirectory string, stdin []byte, arguments ...string) ([]byte, error) {
	fullArguments := append([]string{"--git-dir=" + gitDirectory}, arguments...)
	command := exec.Command("git", fullArguments...)
	command.Env = cleanEnvironment()
	if stdin != nil {
		command.Stdin = bytes.NewReader(stdin)
	}
	output, err := command.Output()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("git %s: %s", strings.Join(arguments, " "), strings.TrimSpace(string(exitError.Stderr)))
		}
		return nil, err
	}
	return output, nil
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
