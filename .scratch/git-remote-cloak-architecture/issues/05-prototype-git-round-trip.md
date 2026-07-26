# Prototype the full Git round trip

Type: prototype
Status: resolved
Blocked by: 04

## Question

Does a throwaway implementation of the selected representation actually behave like an ordinary Git remote while round-tripping exact original objects?

Exercise a local bare server through clone, fetch, push, pull/merge, branches, annotated and lightweight tags, ref deletion, force-push, divergent updates, signed objects if available, submodule metadata, empty repositories, interrupted operations, and shallow clone. Record which behaviors are guaranteed, emulated, inefficient, or unsupported.

## Answer

The throwaway prototype on branch `codex/prototype-git-round-trip` at commit `64ec94e` validated the selected wrapper-repository state model against real Git 2.55 using a local `file://` bare Repository Host. The user independently ran `python3 tui.py --all` and confirmed that every scripted scenario behaved normally.

Verified behavior included ordinary clone, fetch, push, pull/merge, logical branches, annotated and lightweight tags, ref deletion, divergent-push rejection, explicit force push, force-with-lease, interruption before Storage Ref publication followed by retry, depth-one clone with a complete checkout and shallow boundary, submodule metadata and gitlink recovery, empty repositories, an ephemeral SSH-signed commit, and `git fsck --full`.

A final fresh recovery produced the same 29 reachable original Git object IDs as the logical shadow repository. Inspection of every reachable outer object found none of the known original paths, contents, Logical Ref names, tag messages, or commit messages. The Repository Host exposed only `refs/heads/cloak-storage`, and every Storage History commit used the constant message `cloak storage update`.

The prototype also established important limits:

- shallow semantics are correct, but a cache miss currently transfers every live Pack Payload;
- self-contained incremental packs give up cross-push delta compression;
- atomic multi-ref push, partial clone, compaction, SHA-256-format Git repositories, LFS rejection, a forced two-writer race, and real provider transports remain unvalidated;
- the Python authenticated opaque codec and canonical JSON are state-model substitutes, not production AES-256-GCM-SIV or deterministic CBOR/CDDL.

The state model is accepted. The production implementation target is Go; none of the Python prototype code should be merged into the production implementation.
