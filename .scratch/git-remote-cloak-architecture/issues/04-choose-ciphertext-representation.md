# Choose the Ciphertext Repository representation

Type: grilling
Status: resolved
Blocked by: 01, 02, 03

## Question

Given the solution survey, Git protocol facts, and cryptographic building blocks, what exact remote representation should v1 choose?

Decide how plaintext refs and Git objects map to authenticated encrypted packs, chunks, manifests, and remote refs; what remains stable or append-only; how original paths and messages remain absent; how exact object IDs are recovered; and which accepted metadata remains visible. Compare the pack-first hypothesis against any viable object-mirroring alternative.

## Answer

Choose a pack-first, encrypt-second wrapper repository with one public `refs/heads/cloak-storage` Storage Ref.

Each fast-forward storage commit publishes a complete Ciphertext Snapshot whose tree references:

- a public, authenticated Bootstrap Header carrying the format version, random Repository ID, fixed repository chunk size, and current manifest locator, but no Protected Plaintext;
- an authenticated encrypted manifest carrying every Logical Ref name and original OID, the storage generation, and the live Pack Payload catalog;
- one authenticated Encrypted Pack Index per Pack Payload so authorized clients can locate original Git objects without downloading every pack; and
- immutable Encrypted Pack Chunks named by a safe Base32 encoding of the ciphertext SHA-256.

V1 Pack Payloads are self-contained: objects in a new payload do not use objects from older payloads as omitted delta bases. This gives up cross-push delta efficiency in exchange for simple independent recovery and corruption boundaries. Git still performs compression and delta selection within each Pack Payload before encryption. The prototype and benchmark will determine the resulting capacity multiplier.

Pack Payloads are split into independently authenticated chunks with a default and per-repository fixed maximum plaintext chunk size of 32 MiB. Structured repository metadata uses deterministic CBOR governed by a versioned CDDL schema rather than implementation-specific serialization.

The current snapshot tree references every live ciphertext object, while unchanged Git blobs are reused across storage commits. Normal publication uploads immutable objects first and then compare-and-swap fast-forwards `cloak-storage`. Compaction may publish a new parentless optimized snapshot and force-update only the Storage Ref; it never changes the protected Git history, Logical Refs, or original Git object IDs. Repository Host retention means compaction cannot promise physical erasure of superseded ciphertext.

There is no per-file or per-path ciphertext mapping. Original paths and commit/tag contents remain only inside encrypted native Git packs; Logical Ref names and targets remain only inside the encrypted manifest. The Repository Host sees the fixed Storage Ref, public non-sensitive format metadata, ciphertext object identifiers and sizes, storage topology, timing, and update patterns.

This representation independently adopts the useful remote-helper and encrypted-pack precedent of `git-remote-gcrypt`, while using Cloak's direct Recovery Secret, incremental immutable payloads, explicit logical-ref validation, bounded chunks, provider-compatible transaction point, and service-oriented one-binary contract.
