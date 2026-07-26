# Verify Git remote-helper and encrypted-pack feasibility

Type: research
Status: resolved

## Question

Can one `git-remote-cloak` binary use Git's remote-helper protocol plus ordinary HTTPS/SSH Git transport to present a normal plaintext object graph locally while storing an opaque, incrementally updateable encrypted-pack representation remotely?

Using Git's official documentation and source code where needed, determine:

- the viable helper capability set and process boundaries for clone, fetch, push, pull, refs, tags, deletion, and force updates;
- whether pack-first, encrypt-second can preserve exact original Git objects and IDs on recovery;
- how a helper could bootstrap, discover refs, and update a Ciphertext Repository without provider APIs;
- what must be downloaded for a fresh clone and what incremental fetch/push can avoid;
- whether ordinary shallow-clone semantics fall out naturally, without a Cloak-specific restriction;
- where concurrency, atomicity, recursion, or transport limitations create architectural risk.

Separate facts guaranteed by Git from hypotheses that require a prototype.

## Answer

Yes, with a constrained architecture: use the direct `list` / `fetch` /
`push` remote-helper path locally, and store an authenticated encrypted
logical-ref manifest plus immutable, bounded encrypted Git-pack chunks behind
one transactional synthetic ref on the ordinary Git host. This can restore
byte-exact Git objects and original OIDs without provider APIs. Persistent
wrapper cache state is required for efficient incremental transfer; pack
granularity, cross-push delta loss, concurrency mapping, and shallow efficiency
remain prototype/benchmark questions. Shallow clone needs no Cloak-specific
restriction: its ordinary current-snapshot semantics are expressible through
the helper's documented depth options.

See [the full findings](../research/02-remote-helper-pack-feasibility.md).
