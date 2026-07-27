# 11 — Publish and recover one protected branch through ordinary Git

**What to build:** Extend the production Go vertical slice so a Repository Owner can use ordinary Git to push one protected branch containing real commits and files, then clone it through Cloak with exact original Git objects while the Repository Host retains only the fixed Storage Ref and opaque ciphertext representation defined by the [v1 specification](../spec.md).

**Blocked by:** 10 — Initialize and recover an empty Ciphertext Repository.

**Status:** resolved

- [x] The remote-helper `list`, `fetch`, and `push` paths support an ordinary first push of one branch and an ordinary fresh clone without an auxiliary binary or provider API.
- [x] A push creates a self-contained native Git Pack Payload before encryption, an authenticated Encrypted Pack Index, bounded authenticated Encrypted Pack Chunks, and one authoritative Encrypted Manifest.
- [x] Encrypted Pack Chunks obey the repository's fixed maximum plaintext chunk size, use ciphertext SHA-256 lowercase unpadded Base32 Opaque Segment Identifiers, and authenticate record kind, Repository ID, payload identity, chunk index, final marker, and plaintext length.
- [x] Immutable ciphertext is uploaded before a single compare-and-swap Storage Ref publication, and any failure before that update leaves the previous Ciphertext Snapshot authoritative.
- [x] Fresh recovery imports the native pack, restores Logical HEAD and the branch target, compares expected object IDs, runs `git fsck --full`, and produces the exact reachable Git object set from the source Logical Repository.
- [x] The Repository Host exposes only `refs/heads/cloak-storage`, uses a constant non-sensitive Storage History commit message, and contains no original path, file content, branch name, or commit message in any reachable outer Git object.
- [x] Ordinary compressed binary blobs stored directly in Git round-trip exactly in the same scenario.
- [x] Authentication, missing-object, malformed-index, chunk-substitution, and pack-corruption failures abort recovery without exposing a partial usable plaintext checkout.
- [x] End-to-end tests invoke a real supported Git executable and a local bare Repository Host rather than replacing Git behavior with mocks.
