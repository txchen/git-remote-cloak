// Package domain defines the value types shared by Cloaking Layer modules.
package domain

import "strings"

// RecoverySecret is the Repository Owner's 256-bit root credential.
type RecoverySecret [32]byte

// RepositoryID is the public random identity of one Ciphertext Repository.
type RepositoryID [16]byte

// LogicalHEAD is the encrypted symbolic ref selecting the default Logical Ref.
type LogicalHEAD string

// LogicalRefName is the name of a branch or tag Logical Ref.
type LogicalRefName string

// IsSupported reports whether the ref is part of v1's branch-and-tag surface.
func (name LogicalRefName) IsSupported() bool {
	value := string(name)
	return strings.HasPrefix(value, "refs/heads/") || strings.HasPrefix(value, "refs/tags/")
}

// IsStorable reports whether a ref may be preserved as an explicitly selected
// Logical Ref. Ordinary push remains limited to IsSupported's heads and tags.
func (name LogicalRefName) IsStorable() bool {
	value := string(name)
	if !strings.HasPrefix(value, "refs/") || strings.HasSuffix(value, "/") || strings.HasSuffix(value, ".") ||
		strings.Contains(value, "//") || strings.Contains(value, "..") || strings.Contains(value, "@{") ||
		strings.ContainsAny(value, " ~^:?*[\\") {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return value != "refs/stash" && !strings.HasPrefix(value, "refs/remotes/") &&
		!strings.HasPrefix(value, "refs/bisect/") && !strings.HasPrefix(value, "refs/rewritten/") &&
		!strings.HasPrefix(value, "refs/replace/") && !strings.HasPrefix(value, "refs/worktree/") &&
		!strings.HasPrefix(value, "refs/original/") && !strings.HasPrefix(value, "refs/prefetch/") &&
		!strings.HasPrefix(value, "refs/bundle/")
}
