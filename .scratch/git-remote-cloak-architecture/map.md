Label: wayfinder:map

## Destination

Produce a decision-ready, evidence-validated architecture and product specification for `git-remote-cloak`: a private Git backup workflow whose authorized local repositories are ordinary plaintext Git repositories while standard Git hosts receive no plaintext file contents, original paths, or commit messages.

## Notes

- This effort is planning and research only; it does not implement the product.
- Use the `research`, `prototype`, `grilling`, and `domain-modeling` skills as indicated by each ticket.
- The Repository Host is untrusted with Protected Plaintext even when the hosted repository is private.
- Accepted leakage includes ciphertext sizes and counts, timing, commit count, history topology, and change patterns.
- A Recovered Repository preserves pushed reachable refs and exact original Git objects, including paths, contents, messages, authors, timestamps, and object IDs.
- Daily local use is ordinary Git. The single Linux/macOS binary is `git-remote-cloak`; Git invokes it for `cloak::` remotes, and humans invoke its `init` and `clone` subcommands.
- GitHub, GitLab, and standard HTTPS/SSH Git servers must work without provider-specific APIs.
- A single Repository Owner supplies a per-repository Recovery Secret, represented primarily as a versioned 24-word mnemonic. Authorized services may persist or receive it through an environment variable and must work after restart without interactive unlocking.
- The current storage hypothesis is pack-first, encrypt-second. Encryption overhead itself should be small; preserving Git's compression and delta behavior is a first-class architecture and benchmark question.

## Decisions so far

- [Survey existing private Git backup solutions](issues/01-survey-existing-solutions.md) — Build an independent helper around Git's remote-helper and pack plumbing, borrowing encrypted-pack/manifest precedent rather than adopting an existing tool unchanged.
- [Verify Git remote-helper and encrypted-pack feasibility](issues/02-verify-remote-helper-and-pack-feasibility.md) — Direct `list`/`fetch`/`push` can front an authenticated encrypted pack catalog behind a transactional synthetic ref; exact OID recovery is viable, while incremental efficiency and concurrency need prototypes.
- [Select cryptographic building blocks and Recovery Secret format](issues/03-select-cryptographic-building-blocks.md) — Use a random 256-bit secret, versioned 24-word entropy encoding, domain-separated keys, authenticated chunk encryption, and keyed portable identifiers; whole-repository rollback needs external trusted state.
- [Choose the Ciphertext Repository representation](issues/04-choose-ciphertext-representation.md) — Publish complete snapshots through one transactional Storage Ref using a public authenticated header, encrypted refs/manifest/indexes, and self-contained native Git packs split into immutable 32 MiB ciphertext-addressed chunks.

## Not yet specified

- The local cache, encrypted-pack compaction, and format-migration strategy cannot be made precise until the Ciphertext Repository representation is selected and benchmarked.
- Efficient partial-clone behavior may become a separate decision if the remote-helper protocol and chosen representation expose a useful object-level frontier.
- The exact service embedding surface beyond ordinary Git CLI usage depends on the operational failure model found by the round-trip prototype.

## Out of scope

- Product implementation in this Wayfinder effort.
- Multi-owner sharing, member invitation, and per-member revocation.
- Native Windows binaries; Windows use is through WSL.
- Git LFS. Cloak must detect and reject LFS-backed content rather than silently bypass protection.
- Guaranteeing erasure of old ciphertext retained by a Repository Host after Recovery Secret rotation.
- Recovering uncommitted worktree changes, stash, reflog, hooks, local Git configuration, or other non-pushed local state.
- Recursive shared-secret orchestration for submodules; each private submodule is an independently cloaked repository.
