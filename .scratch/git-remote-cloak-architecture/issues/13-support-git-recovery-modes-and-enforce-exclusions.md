# 13 — Support Git recovery modes and enforce exclusions

**What to build:** Extend clone, fetch, and push so the supported Git recovery modes and explicit v1 exclusions in the [v1 specification](../spec.md) are enforced as observable user behavior instead of becoming silent full clones, incomplete backups, or plaintext escape paths.

**Blocked by:** 11 — Publish and recover one protected branch through ordinary Git.

**Status:** ready-for-agent

- [ ] A depth-one clone records an ordinary Git shallow boundary, exposes only the requested visible commit history, and produces a complete current checkout with exact recovered objects for that boundary.
- [ ] Shallow fetch behavior remains consistent with ordinary Git semantics even when a cache miss requires every live Pack Payload.
- [ ] `.gitmodules` and gitlink mode `160000` round-trip without Cloak automatically cloning or sharing Recovery Secrets with submodule repositories.
- [ ] SHA-1 and SHA-256 Git object-format repositories each complete the supported push and clone path with exact expected object IDs and `git fsck --full`.
- [ ] Ordinary binary blobs stored directly in Git remain supported regardless of content type.
- [ ] A push that makes a Git LFS pointer blob newly reachable rejects the whole logical transaction before Storage Ref publication and names affected paths only in the local error.
- [ ] Clone and fetch reject an existing Logical Repository that depends on Git LFS content without exposing a partial usable checkout or offering a bypass flag.
- [ ] Partial clone filters and promisor-object state fail explicitly rather than silently producing a full clone or publishing an incomplete backup.
- [ ] Tests distinguish supported shallow behavior from unsupported partial-clone behavior and scan the outer repository for Protected Plaintext after each mode.
