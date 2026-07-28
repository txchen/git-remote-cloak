# 18 — Migrate repository format explicitly

**What to build:** Let a Repository Owner explicitly rebuild a Ciphertext Repository into a selected supported format without changing its Recovery Secret, Repository ID, or Logical Repository, while ensuring that normal operations and binary upgrades never perform an automatic migration prohibited by the [v1 specification](../spec.md).

**Blocked by:** 16 — Compact a fragmented Ciphertext Repository.

**Status:** resolved

- [x] The Format Registry declares exact read and write support for each format and required feature; read support never implies write support.
- [x] Unknown bootstrap framing, unsupported major versions, unknown required features, and header/preamble disagreement fail closed before encrypted payload processing.
- [x] A reader that cannot write the current format returns a migration-required error for writes rather than silently selecting another writer.
- [x] Human Migration displays current and target formats, Logical Ref count, estimated full upload, and writer compatibility effect before confirmation.
- [x] Unattended Migration requires both an explicit target and explicit consent, while dry-run structured output performs compatibility, capacity, and plan checks without mutation.
- [x] Migration treats authenticated remote Logical Refs and Logical HEAD as authoritative, keeps the Recovery Secret and Repository ID, derives target-format keys, and increments generation exactly once.
- [x] Every pack, index, and manifest is rebuilt and re-encrypted; the target snapshot is compacted, parentless, and carries authenticated migration-source format, generation, and Storage Ref metadata.
- [x] Candidate validation compares all Logical Refs, reachable original Git object IDs, Logical HEAD, and `git fsck --full` before immutable upload and compare-and-swap publication.
- [x] Any concurrent remote update aborts Migration and requires a new plan; ordinary clone, fetch, push, Compaction, installation, and binary upgrade never migrate automatically.
- [x] V1 offers no in-place downgrade, dual-format publication, cross-format Storage History, or second Storage Ref.
- [x] A test-only second format proves successful Migration and rollback continuity without being advertised as a supported production format.

## Comments

Implemented exact reader/writer format selection, explicit human and unattended Migration planning, read-only JSON dry runs, authenticated target-format Snapshot Rebuild and validation, parentless compare-and-swap publication, downgrade/concurrency rejection, and a non-advertised test format that proves recovery and Rollback Checkpoint continuity.
