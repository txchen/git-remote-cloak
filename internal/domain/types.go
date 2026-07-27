// Package domain defines the value types shared by Cloaking Layer modules.
package domain

// RecoverySecret is the Repository Owner's 256-bit root credential.
type RecoverySecret [32]byte

// RepositoryID is the public random identity of one Ciphertext Repository.
type RepositoryID [16]byte

// LogicalHEAD is the encrypted symbolic ref selecting the default Logical Ref.
type LogicalHEAD string
