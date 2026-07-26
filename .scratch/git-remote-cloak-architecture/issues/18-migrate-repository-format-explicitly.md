# 18 — Migrate repository format explicitly

**What to build:** Let a Repository Owner explicitly rebuild a Ciphertext Repository into a selected supported format without changing its Recovery Secret, Repository ID, or Logical Repository, while ensuring that normal operations and binary upgrades never perform an automatic migration prohibited by the [v1 specification](../spec.md).

**Blocked by:** 16 — Compact a fragmented Ciphertext Repository.

**Status:** ready-for-agent

- [ ] The Format Registry declares exact read and write support for each format and required feature; read support never implies write support.
- [ ] Unknown bootstrap framing, unsupported major versions, unknown required features, and header/preamble disagreement fail closed before encrypted payload processing.
- [ ] A reader that cannot write the current format returns a migration-required error for writes rather than silently selecting another writer.
- [ ] Human Migration displays current and target formats, Logical Ref count, estimated full upload, and writer compatibility effect before confirmation.
- [ ] Unattended Migration requires both an explicit target and explicit consent, while dry-run structured output performs compatibility, capacity, and plan checks without mutation.
- [ ] Migration treats authenticated remote Logical Refs and Logical HEAD as authoritative, keeps the Recovery Secret and Repository ID, derives target-format keys, and increments generation exactly once.
- [ ] Every pack, index, and manifest is rebuilt and re-encrypted; the target snapshot is compacted, parentless, and carries authenticated migration-source format, generation, and Storage Ref metadata.
- [ ] Candidate validation compares all Logical Refs, reachable original Git object IDs, Logical HEAD, and `git fsck --full` before immutable upload and compare-and-swap publication.
- [ ] Any concurrent remote update aborts Migration and requires a new plan; ordinary clone, fetch, push, Compaction, installation, and binary upgrade never migrate automatically.
- [ ] V1 offers no in-place downgrade, dual-format publication, cross-format Storage History, or second Storage Ref.
- [ ] A test-only second format proves successful Migration and rollback continuity without being advertised as a supported production format.
