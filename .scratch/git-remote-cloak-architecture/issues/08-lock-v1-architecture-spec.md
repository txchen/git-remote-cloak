# Lock the decision-ready v1 architecture and product spec

Type: grilling
Status: resolved
Blocked by: 01, 05, 06, 07, 09

## Question

Do the accumulated evidence and prototypes support a coherent v1 specification that is ready to hand off for implementation?

Consolidate the threat model, adopt/combine/build decision, component boundaries, remote representation, cryptographic format, CLI workflows, provider compatibility, recovery guarantees, supported Git behavior, capacity expectations, failure semantics, explicit exclusions, and a verification matrix. Any remaining uncertainty must be converted into a named follow-up decision rather than hidden in implementation prose.

## Answer

Yes. The accumulated research, accepted decisions, Git round-trip prototype, and storage benchmark support a coherent single-owner v1 architecture. The decision-ready specification is [git-remote-cloak v1 Architecture and Product Specification](../spec.md).

V1 builds an independent Go remote helper around one public Storage Ref, complete authenticated Ciphertext Snapshots, encrypted self-contained native Git packs, a direct 256-bit Recovery Secret, and ordinary Git transport. The architecture is divided into deep Modules around a Repository Engine, exact Format Registry, Git Database, Local State, and a Storage Transport Seam.

Production encryption uses a pinned Tink Go v2 AES-256-GCM-SIV no-prefix/RAW primitive. Each encrypted record carries a random 96-bit nonce generated through Tink's public API; record identity, chunk index, final marker, and length remain authenticated as associated data. Cloak does not use Tink internal APIs, maintain a cryptographic fork, or reuse prototype crypto and JSON framing.

No product or architecture question remains open for v1 implementation. Unvalidated production claims are explicitly named as release gates for byte-format conformance, hosted providers, concurrency, maintenance, rollback, credentials, Git object formats, LFS rejection, and failure injection.
