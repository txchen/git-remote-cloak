# 12 — Complete Logical Ref workflows

**What to build:** Make the production Cloaking Layer behave like an ordinary Git remote for daily single-owner work across the complete Logical Ref workflow in the [v1 specification](../spec.md), while continuing to publish every logical transaction atomically through the one Storage Ref.

**Blocked by:** 11 — Publish and recover one protected branch through ordinary Git.

**Status:** resolved

- [x] Incremental push, fetch, and pull with a user-created merge preserve exact reachable Git objects and advertise the current Logical Refs and Logical HEAD.
- [x] Multiple branches, annotated tags, lightweight tags, ref deletion, and an explicit Logical HEAD change round-trip with their original names and targets visible only to an Authorized Host.
- [x] Normal non-fast-forward updates are rejected, explicit forced updates are accepted, and force-with-lease succeeds or fails against the authenticated current Logical Ref target.
- [x] Every requested update in a multi-ref push is represented by one Encrypted Manifest and one Storage Ref update, so all requested Logical Refs succeed or none does.
- [x] Cloak never creates a logical merge or silently chooses an integration strategy when a push diverges.
- [x] Empty and unborn Logical Repositories remain usable before their first branch publication.
- [x] Signed commits and annotated tags recover byte-exactly and remain verifiable through ordinary Git after clone.
- [x] Repeated pushes reuse unchanged live ciphertext objects while each new Pack Payload remains self-contained.
- [x] A fresh recovery after the complete scenario has exactly the expected Logical Refs and reachable original Git object IDs and passes `git fsck --full`.
- [x] The outer repository still exposes only the fixed Storage Ref and passes the Protected Plaintext scan after every supported ref transition.
