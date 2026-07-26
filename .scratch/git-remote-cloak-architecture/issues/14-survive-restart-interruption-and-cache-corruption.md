# 14 — Survive restart, interruption, and cache corruption

**What to build:** Make ordinary Git operations safe and repeatable across service restart, process interruption, lost responses, partial uploads, and damaged local cache state, using the persistent Secret-free Local State contract from the [v1 specification](../spec.md).

**Blocked by:** 12 — Complete Logical Ref workflows.

**Status:** ready-for-agent

- [ ] Persistent local cache state contains only reconstructable wrapper Git objects, ciphertext, encrypted indexes, and authenticated metadata; it contains no Recovery Secret, derived key, or plaintext Git payload.
- [ ] A service can restart with its configured Recovery Secret and continue incremental fetch and push without interactive input or rebuilding valid cache entries.
- [ ] Missing or corrupted cache entries are isolated and reconstructed from the current Ciphertext Snapshot without changing Logical Refs or recoverability.
- [ ] `git-remote-cloak cache clear` removes only reconstructable cache data and the next operation rebuilds it successfully.
- [ ] Secret-free crash journals record the intended Logical Ref transaction, starting Storage Ref, and prepared Storage commit sufficiently to resume or recognize completion.
- [ ] Interruption before Storage Ref publication leaves logical state unchanged; interruption after successful publication but before the response is recognized as success on retry.
- [ ] Fetch installs objects and permits ref updates only after all required metadata, chunks, packs, and expected targets validate.
- [ ] Safe immutable-object uploads use bounded retries, while authentication and ref-update rejection do not retry indefinitely.
- [ ] Clone failure removes Cloak-created temporary plaintext, retains only independently validated ciphertext cache entries, and never overwrites a non-empty destination.
- [ ] Fault-injection tests cover upload failure, lost process, lost response, cache corruption, invalid journal state, and local write failure while proving that the previous Ciphertext Snapshot remains recoverable.
