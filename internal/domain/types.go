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
