# 16 — Compact a fragmented Ciphertext Repository

**What to build:** Let a Repository Owner safely replace fragmented live Pack Payloads with one validated optimized Ciphertext Snapshot without changing the Recovery Secret or Logical Repository, and make the [v1 specification](../spec.md) compaction thresholds enforceable in ordinary and service workflows.

**Blocked by:** 15 — Protect concurrent publication and known rollback.

**Status:** resolved

- [x] `git-remote-cloak compact <remote-name>` rebuilds one optimized self-contained Pack Payload from the complete current Logical Repository.
- [x] The shared Snapshot Rebuild path restores the candidate, compares every Logical Ref and reachable original Git object ID, confirms Logical HEAD, and runs `git fsck --full` before publication.
- [x] Successful Compaction preserves the Recovery Secret, Repository ID, repository format, Logical Refs, Logical HEAD, and original Git object IDs while increasing generation and publishing a parentless Storage History root.
- [x] New immutable ciphertext uploads before a compare-and-swap force-re-root; interruption or concurrent remote change leaves the previous Storage Ref authoritative.
- [x] Automatic Compaction runs synchronously before the push that would create a thirty-third live Pack Payload or reach 50% of the previous compacted snapshot size in added ciphertext.
- [x] A service can disable automatic Compaction, run it in a maintenance window, and receive explicit capacity warnings when pushes continue beyond a threshold.
- [x] Progress distinguishes packing, encryption, upload, validation, and publication without exposing Protected Plaintext.
- [x] The deterministic Markdown-heavy benchmark records fragmented, compacted, Storage History, and transfer measurements for the Go implementation and demonstrates one live Pack Payload after Compaction.
- [x] The benchmark prevents a material regression from the accepted compacted-storage target while reporting results as workload evidence rather than a universal guarantee.
- [x] User-facing output states that Repository Host retention may prevent immediate quota recovery or physical erasure of superseded ciphertext.

## Comments

Implemented with authenticated v1 compaction baseline counters and a shared validated rebuild path. The deterministic Go benchmark fixture measured a 1.109x compacted-live/ordinary-pack ratio, 108,356 fragmented live bytes, 72,621 compacted live bytes, 121,924 fragmented Storage History bytes, 73,291 compacted Storage History bytes, and 111,173 cumulative transferred ciphertext bytes for this workload; these figures are workload evidence, not a universal guarantee.
