# 15 — Protect concurrent publication and known rollback

**What to build:** Make the Repository Engine safe when Authorized Hosts write concurrently or a Repository Host returns an older authenticated state, and give the Repository Owner read-only diagnostics and trusted checkpoint export as specified by the [v1 specification](../spec.md).

**Blocked by:** 12 — Complete Logical Ref workflows; 14 — Survive restart, interruption, and cache corruption.

**Status:** ready-for-agent

- [ ] Two writers race through a real Storage Ref compare-and-swap rather than an in-process lock or provider-specific API.
- [ ] Compatible concurrent updates are refetched, revalidated, rebuilt, and retried no more than three times, while true divergence and stale leases return ordinary Git-compatible failures.
- [ ] A concurrent multi-ref push remains all-or-none, and failed attempts may leave only unreachable ciphertext rather than a partially published Logical Ref set.
- [ ] Local operation locking prevents conflicting push or maintenance construction from the same Logical Repository without pretending to coordinate other Authorized Hosts.
- [ ] The Rollback Checkpoint records Repository ID, highest authenticated generation, and last-seen Storage Ref only after successful validation and confirmed publication.
- [ ] Generation regression, same-generation substitution, and unexplained Storage History reversal fail closed when a trusted checkpoint exists.
- [ ] Machine-readable status output can export the checkpoint for an Authorized Host that retains trusted external state.
- [ ] `git-remote-cloak doctor` is read-only, supports structured output, and diagnoses header and manifest authentication, missing or damaged ciphertext, pack/index consistency, LFS-backed content, Logical Ref targets, and the recovered Git object graph.
- [ ] An older authenticated recoverable generation can be recovered only through an explicit operation into a separate local repository; normal clone, fetch, push, and doctor never silently downgrade or mutate the remote.
- [ ] Diagnostics redact Recovery Secrets, derived keys, Secret-bearing environment values, and Protected Plaintext while retaining safe provider errors, ciphertext identifiers, and ciphertext sizes.
- [ ] Tests document that a fresh clone without a trusted checkpoint cannot prove newest-state freshness.
