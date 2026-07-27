# 10 — Initialize and recover an empty Ciphertext Repository

**What to build:** Deliver the first production Go vertical slice from the [v1 specification](../spec.md): a Repository Owner can initialize an empty standard Git Repository Host as a format-conformant Ciphertext Repository and recover it as an ordinary empty Logical Repository with either supported clone workflow. This slice establishes the production Recovery Secret, cryptographic format, command frontends, Repository Engine, Format Registry, Local State, and local bare-repository Storage Transport only as deeply as this working behavior requires.

**Blocked by:** None — can start immediately.

**Status:** resolved

- [x] A single `git-remote-cloak` Go binary builds and tests without using the Python prototype codec, JSON framing, Tink internal APIs, a cryptographic fork, or CGo.
- [x] Interactive initialization generates a 256-bit operating-system-random Recovery Secret, displays its `cloak-v1:` 24-word Recovery Mnemonic once, and requires confirmation that it was saved before publication.
- [x] Environment, environment-named file, and explicit secret-file acquisition obey the ambiguity, validation, permission-warning, non-interactive, no-literal-secret-argument, and redaction contracts.
- [x] Initializing an empty local bare Repository Host publishes a valid empty Ciphertext Snapshot and exposes only `refs/heads/cloak-storage`; it does not push or modify any local Logical Ref, Git object, index entry, or worktree file.
- [x] Initialization is idempotent for the same Cloak identity and fails without mutation for foreign refs, another Cloak identity, a mismatched remote configuration, or a detached `HEAD` without an explicit default branch.
- [x] Both `git-remote-cloak clone` and `git clone cloak::<repository-url>` recover an ordinary empty repository with the correct `origin` and Logical HEAD behavior, using a mode-`0700` temporary directory and atomic destination publication.
- [x] The v1 Bootstrap Preamble, Bootstrap Header, empty Encrypted Manifest, associated-data encodings, limits, feature identifiers, and cryptographic-suite identifiers are governed by normative CDDL and canonical golden byte vectors.
- [x] HKDF-SHA-256 purpose separation, Tink Go AES-256-GCM-SIV RAW framing, RFC 8452 or Wycheproof vectors, random nonces, authenticated final records, tamper detection, truncation detection, and cross-context substitution failures are covered by tests.
- [x] `git-remote-cloak version --formats` reports exact v1 read and write capability in human-readable and machine-readable forms and fails closed for unknown framing, unsupported major versions, or unknown required features.
- [x] Inspection of every reachable outer Git object finds no Recovery Secret, derived key, Logical Ref name, original path, content, or commit message.
