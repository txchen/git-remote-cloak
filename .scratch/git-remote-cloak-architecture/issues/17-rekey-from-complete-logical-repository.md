# 17 — Rekey from a complete Logical Repository

**What to build:** Let a Repository Owner who still has a complete local Logical Repository replace the remote backup with a new Ciphertext Repository identity and Recovery Secret without needing the old Secret, while preserving the selected original Git history and the same Repository Host URL as defined by the [v1 specification](../spec.md).

**Blocked by:** 16 — Compact a fragmented Ciphertext Repository.

**Status:** ready-for-agent

- [ ] The explicit Rekey workflow operates from a complete local Logical Repository and does not require or attempt to authenticate with the old Recovery Secret.
- [ ] Default ref selection includes all local heads and tags, excludes remote-tracking and local operational refs, and accepts explicit refspecs for notes or custom refs.
- [ ] Before destructive confirmation, Cloak displays the complete selected ref set and warns about remote-tracking branches without corresponding local branches.
- [ ] Rekey generates or accepts a new Recovery Secret, creates a new random Repository ID, selects the current default writable format, and builds a compacted generation-one Ciphertext Snapshot.
- [ ] Candidate validation restores every selected Logical Ref, compares every reachable original Git object ID, confirms Logical HEAD, and runs `git fsck --full`.
- [ ] New ciphertext uploads before a compare-and-swap replacement of the existing Storage Ref, so interruption or concurrent remote change leaves the old backup recoverable.
- [ ] Successful Rekey keeps the Repository Host URL, installs the new Rollback Checkpoint only after confirmed publication, and can be cloned using only the new Recovery Secret.
- [ ] The old Recovery Secret cannot authenticate or decrypt the new Ciphertext Repository identity.
- [ ] Output explicitly states that Repository Host retention may preserve ciphertext recoverable with the old Secret and that Rekey cannot guarantee erasure or immediate quota recovery.
- [ ] Tests cover user cancellation, incomplete local objects, validation failure, interruption, stale Storage Ref, successful replacement, and exact selected-ref recovery.
