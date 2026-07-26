# 16 — Compact a fragmented Ciphertext Repository

**What to build:** Let a Repository Owner safely replace fragmented live Pack Payloads with one validated optimized Ciphertext Snapshot without changing the Recovery Secret or Logical Repository, and make the [v1 specification](../spec.md) compaction thresholds enforceable in ordinary and service workflows.

**Blocked by:** 15 — Protect concurrent publication and known rollback.

**Status:** ready-for-agent

- [ ] `git-remote-cloak compact <remote-name>` rebuilds one optimized self-contained Pack Payload from the complete current Logical Repository.
- [ ] The shared Snapshot Rebuild path restores the candidate, compares every Logical Ref and reachable original Git object ID, confirms Logical HEAD, and runs `git fsck --full` before publication.
- [ ] Successful Compaction preserves the Recovery Secret, Repository ID, repository format, Logical Refs, Logical HEAD, and original Git object IDs while increasing generation and publishing a parentless Storage History root.
- [ ] New immutable ciphertext uploads before a compare-and-swap force-re-root; interruption or concurrent remote change leaves the previous Storage Ref authoritative.
- [ ] Automatic Compaction runs synchronously before the push that would create a thirty-third live Pack Payload or reach 50% of the previous compacted snapshot size in added ciphertext.
- [ ] A service can disable automatic Compaction, run it in a maintenance window, and receive explicit capacity warnings when pushes continue beyond a threshold.
- [ ] Progress distinguishes packing, encryption, upload, validation, and publication without exposing Protected Plaintext.
- [ ] The deterministic Markdown-heavy benchmark records fragmented, compacted, Storage History, and transfer measurements for the Go implementation and demonstrates one live Pack Payload after Compaction.
- [ ] The benchmark prevents a material regression from the accepted compacted-storage target while reporting results as workload evidence rather than a universal guarantee.
- [ ] User-facing output states that Repository Host retention may prevent immediate quota recovery or physical erasure of superseded ciphertext.
