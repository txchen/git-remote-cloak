package gitdb

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SetShallowDepth records the ordinary Git shallow boundary for the requested
// commit tips. The complete authenticated object database may still be present;
// Git uses this boundary to expose only the requested history.
func SetShallowDepth(gitDirectory string, requestedObjectIDs []string, depth int, relative bool) error {
	if depth < 1 {
		return errors.New("shallow depth must be positive")
	}
	tips := make([]string, 0, len(requestedObjectIDs))
	if relative {
		contents, err := os.ReadFile(filepath.Join(gitDirectory, "shallow"))
		if err != nil {
			return fmt.Errorf("read existing shallow boundary: %w", err)
		}
		seenTips := make(map[string]struct{})
		for _, boundary := range strings.Fields(string(contents)) {
			parents, err := runIgnoringShallow(gitDirectory, "show", "-s", "--format=%P", boundary)
			if err != nil {
				return fmt.Errorf("deepen shallow boundary: %w", err)
			}
			for _, parent := range strings.Fields(string(parents)) {
				if _, exists := seenTips[parent]; !exists {
					seenTips[parent] = struct{}{}
					tips = append(tips, parent)
				}
			}
		}
	} else {
		seenTips := make(map[string]struct{}, len(requestedObjectIDs))
		for _, objectID := range requestedObjectIDs {
			commit, err := runIgnoringShallow(gitDirectory, "rev-parse", "--verify", objectID+"^{commit}")
			if err != nil {
				return fmt.Errorf("resolve shallow fetch tip %s: %w", objectID, err)
			}
			tip := strings.TrimSpace(string(commit))
			if _, exists := seenTips[tip]; !exists {
				seenTips[tip] = struct{}{}
				tips = append(tips, tip)
			}
		}
	}
	if len(tips) == 0 {
		return removeShallowBoundary(gitDirectory)
	}
	boundaries, err := shallowBoundaries(gitDirectory, tips, depth)
	if err != nil {
		return err
	}
	if len(boundaries) == 0 {
		return removeShallowBoundary(gitDirectory)
	}
	sort.Strings(boundaries)
	shallowPath := filepath.Join(gitDirectory, "shallow")
	temporary, err := os.CreateTemp(gitDirectory, "shallow-")
	if err != nil {
		return fmt.Errorf("create shallow boundary: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure shallow boundary: %w", err)
	}
	if _, err := temporary.WriteString(strings.Join(boundaries, "\n") + "\n"); err != nil {
		temporary.Close()
		return fmt.Errorf("write shallow boundary: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close shallow boundary: %w", err)
	}
	if err := os.Rename(temporaryPath, shallowPath); err != nil {
		return fmt.Errorf("publish shallow boundary: %w", err)
	}
	return nil
}

func shallowBoundaries(gitDirectory string, tips []string, depth int) ([]string, error) {
	included := make(map[string][]string)
	frontier := make(map[string]struct{}, len(tips))
	for _, tip := range tips {
		frontier[tip] = struct{}{}
	}
	for generation := 0; generation < depth && len(frontier) > 0; generation++ {
		next := make(map[string]struct{})
		for commit := range frontier {
			parentsOutput, err := runIgnoringShallow(gitDirectory, "show", "-s", "--format=%P", commit)
			if err != nil {
				return nil, fmt.Errorf("calculate shallow boundary: %w", err)
			}
			parents := strings.Fields(string(parentsOutput))
			included[commit] = parents
			if generation+1 < depth {
				for _, parent := range parents {
					if _, exists := included[parent]; !exists {
						next[parent] = struct{}{}
					}
				}
			}
		}
		frontier = next
	}
	boundarySet := make(map[string]struct{})
	for commit, parents := range included {
		for _, parent := range parents {
			if _, exists := included[parent]; !exists {
				boundarySet[commit] = struct{}{}
				break
			}
		}
	}
	boundaries := make([]string, 0, len(boundarySet))
	for boundary := range boundarySet {
		boundaries = append(boundaries, boundary)
	}
	return boundaries, nil
}

func removeShallowBoundary(gitDirectory string) error {
	if err := os.Remove(filepath.Join(gitDirectory, "shallow")); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove shallow boundary: %w", err)
	}
	return nil
}

func runIgnoringShallow(gitDirectory string, arguments ...string) ([]byte, error) {
	alternate, err := os.CreateTemp(gitDirectory, "cloak-empty-shallow-")
	if err != nil {
		return nil, err
	}
	alternatePath := alternate.Name()
	if err := alternate.Close(); err != nil {
		os.Remove(alternatePath)
		return nil, err
	}
	defer os.Remove(alternatePath)
	output, _, err := runWithOptions(gitDirectory, nil, gitRunOptions{
		additionalEnvironment: []string{"GIT_SHALLOW_FILE=" + alternatePath},
	}, arguments...)
	return output, err
}
