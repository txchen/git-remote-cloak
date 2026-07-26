# Cloaked Git Repository

This context describes a privacy-preserving repository workflow in which authorized users work with ordinary files while an untrusted repository host stores only a protected representation.

## Language

**Plaintext Workspace**:
The user-facing local directory containing the original file contents and paths available to an authorized user.
_Avoid_: Plaintext repository, clear repository

**Ciphertext Repository**:
The Git repository stored on an untrusted host, whose committed representation does not expose plaintext file contents, original paths, or commit messages.
_Avoid_: Remote working tree, backup folder

**Cloaking Layer**:
The program-managed boundary that maps a Plaintext Workspace to and from a Ciphertext Repository.
_Avoid_: Transparent encryption, Git filter

**Repository Host**:
The remote Git service that stores the Ciphertext Repository; its access controls may limit exposure, but its operators and storage are not trusted with protected plaintext.
_Avoid_: Trusted cloud, backup authority

**Protected Plaintext**:
Original file contents, file and directory paths, commit messages, and Logical Ref names that must never appear in the Ciphertext Repository.
_Avoid_: Secret files, sensitive subset

**Recovered Repository**:
A normal authorized Git repository reconstructed from a Ciphertext Repository, preserving all pushed reachable branches, tags, commits, paths, contents, messages, authors, timestamps, and Git object identities.
_Avoid_: Decrypted folder, exported snapshot

**Repository Owner**:
The single person authorized to cloak and recover a repository across their own machines.
_Avoid_: Member, collaborator, tenant

**Recovery Secret**:
A per-repository 256-bit random root credential held separately by the Repository Owner and used to derive purpose-specific keys for cloaking and recovering a Ciphertext Repository.
_Avoid_: Password, data key, GitHub credential

**Recovery Mnemonic**:
The versioned 24-word English representation of a Recovery Secret that an owner stores or supplies to an Authorized Host.
_Avoid_: Passphrase, wallet seed, recovery password

**Authorized Host**:
An owner-controlled machine or service environment permitted to persist a Recovery Secret and work with a Plaintext Workspace without interactive unlocking after restart.
_Avoid_: Repository Host, GitHub runner, untrusted server

**Pack Payload**:
A self-contained native Git pack produced for one published repository update, recoverable without using an object from another Pack Payload as a delta base.
_Avoid_: Backup snapshot, encrypted file, storage commit

**Encrypted Pack Chunk**:
A bounded authenticated ciphertext portion of a Pack Payload that does not expose the Protected Plaintext or the individual Git objects it carries.
_Avoid_: Encrypted source file, ciphertext pathname, Pack Payload

**Storage History**:
The internal Git history used to publish states of a Ciphertext Repository; it is distinct from the protected Git history and may be rewritten during compaction.
_Avoid_: Repository history, backup history, original history

**Bootstrap Header**:
Public authenticated metadata that identifies a Ciphertext Repository and its format without containing Protected Plaintext.
_Avoid_: Manifest, repository config, plaintext metadata

**Repository ID**:
A random public identifier that distinguishes one Ciphertext Repository and binds its cryptographic context without identifying its plaintext contents.
_Avoid_: Repository name, Git remote ID, Recovery Secret

**Logical Ref**:
An original branch, tag, or other pushed ref of the protected Git repository, whose name and target are recovered for authorized Git operations.
_Avoid_: Storage ref, wrapper branch, remote branch

**Storage Ref**:
The single public Git ref through which states of a Ciphertext Repository are published atomically, distinct from every protected Logical Ref.
_Avoid_: Logical ref, original branch, protected branch

**Opaque Segment Identifier**:
A public content address derived from an Encrypted Pack Chunk that reveals no original path, Git object identity, or other Protected Plaintext.
_Avoid_: Encrypted filename, cloaked path, object ID

**Encrypted Pack Index**:
Authenticated encrypted metadata that identifies the Git objects carried by one Pack Payload without exposing their identities to the Repository Host.
_Avoid_: Manifest, Git index, object list

**Encrypted Manifest**:
The authoritative authenticated encrypted metadata that relates Logical Refs and their original object identities to the live Pack Payloads of a Ciphertext Snapshot.
_Avoid_: Bootstrap Header, Git manifest, public index

**Ciphertext Snapshot**:
The complete set of Bootstrap Header, encrypted metadata, and immutable ciphertext objects referenced by one published Storage History state.
_Avoid_: Backup snapshot, plaintext snapshot, storage commit

**Logical Repository**:
The ordinary plaintext Git refs, reachable objects, and Plaintext Workspace presented to an Authorized Host.
_Avoid_: Ciphertext Repository, storage repository

**Logical HEAD**:
The encrypted symbolic ref that selects the default Logical Ref checked out by a clone.
_Avoid_: Storage Ref, hosted default branch

**Rekey**:
Replacement of a Ciphertext Repository from a complete local Logical Repository using a new Recovery Secret and Repository ID, without requiring the old Recovery Secret.
_Avoid_: Password change, envelope rotation

**Compaction**:
Replacement of fragmented Pack Payloads with a validated optimized Ciphertext Snapshot while preserving the Recovery Secret and Logical Repository.
_Avoid_: Rekey, history rewrite

**Rollback Checkpoint**:
Trusted local or external state recording the highest authenticated storage generation observed for a Ciphertext Repository.
_Avoid_: Backup snapshot, cache
