// Package localstate owns safe local persistence and temporary plaintext data.
package localstate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// PublishDirectory builds a repository in a restrictive sibling temporary
// directory and atomically renames it into an absent destination.
func PublishDirectory(destination string, build func(temporary string) error) error {
	if _, err := os.Stat(destination); err == nil {
		return errors.New("clone destination already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	temporary, err := os.MkdirTemp(parent, ".cloak-clone-")
	if err != nil {
		return fmt.Errorf("create clone temporary directory: %w", err)
	}
	if err := os.Chmod(temporary, 0o700); err != nil {
		_ = os.RemoveAll(temporary)
		return err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(temporary)
		}
	}()
	if err := build(temporary); err != nil {
		return err
	}
	if err := os.Rename(temporary, destination); err != nil {
		return fmt.Errorf("publish Recovered Repository: %w", err)
	}
	published = true
	return nil
}
