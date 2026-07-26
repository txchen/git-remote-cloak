# Git remote-helper and encrypted-pack feasibility

## Conclusion

The design is feasible without a GitHub- or GitLab-specific API, but the viable
v1 shape is more specific than “proxy the Git protocol and encrypt the bytes.”

`git-remote-cloak` should expose Git's direct `list` / `fetch` / `push`
remote-helper interface to the Plaintext Workspace. Behind that interface, it
should use ordinary Git transport against a small local shadow of the
Ciphertext Repository. The hosted repository should have one well-known storage
ref whose synthetic commits point to:

- an authenticated encrypted manifest containing the real refs and pack
  catalog; and
- immutable, authenticated chunks of Git packfiles created from the original
  object graph.

The original ref names, object IDs, paths, contents, commit messages, authors,
and timestamps must occur only inside the encrypted payload. The hosted
repository's own refs, tree names, commit messages, and identities must be
fixed or synthetic.

This representation supports exact recovery of the original objects and their
IDs. It also supports incremental transfer when an Authorized Host retains its
shadow/cache. It does **not** make encrypted data delta-compressible, and it
does not make shallow clones bandwidth-efficient automatically.

## Facts guaranteed by Git

### Invocation and process boundary

- A `cloak::address` URL explicitly causes Git to execute
  `git-remote-cloak`, passing `address` as its URL argument. The helper is an
  independent process, and Git supplies `GIT_DIR`, so one binary can inspect
  and populate the caller's object database without linking into Git.
  [Git remote-helper documentation](https://git-scm.com/docs/gitremote-helpers#_invocation)
- Every helper implements `capabilities`. A helper advertising `fetch` must
  implement `list` and `fetch`; one advertising `push` must implement
  `list for-push` and `push`.
  [Git remote-helper capabilities](https://git-scm.com/docs/gitremote-helpers#_capabilities)
- `list` can return ordinary object IDs, ref names, and symbolic refs.
  `fetch <oid> <name>` directs the helper to write the necessary objects into
  the local object database. Push requests arrive as a batch of refspecs and
  are answered per destination ref with `ok` or `error`.
  [Git remote-helper commands](https://git-scm.com/docs/gitremote-helpers#_commands)
- Git's helper transport passes a forced update with a leading `+`, represents
  deletion with an empty source, and passes compare-and-swap expectations used
  by `--force-with-lease` through the helper option named `cas`. It also
  requires a helper to accept `option atomic true` before using helper-level
  atomic push. These details are visible in Git's implementation.
  [Git `transport-helper.c`](https://github.com/git/git/blob/master/transport-helper.c)
- `git pull` needs no additional helper command: it is ordinary fetch followed
  by integration in the local repository. Clone likewise uses ref discovery
  and fetch. A human-facing `git-remote-cloak clone` can therefore orchestrate
  `git clone cloak::...`; it is not a second transport protocol.

### Why direct `fetch` / `push` is the right capability set

The recommended v1 capabilities are:

```text
fetch
push
option
check-connectivity
object-format
```

`option` is needed for progress/verbosity, tag following, and ordinary shallow
options. `check-connectivity` lets the helper affirm that a clone is
self-contained and connected. `object-format` is needed if the product claims
support for repositories whose object format is not SHA-1.
[Git remote-helper options and object format](https://git-scm.com/docs/gitremote-helpers#_options)

The helper should not advertise `connect` merely to forward the underlying
Git service. `connect` hands Git a full-duplex native `git-upload-pack` or
`git-receive-pack` connection; forwarding that connection to the Repository
Host would send the original object graph rather than a cloaked one.
[Git remote-helper `connect`](https://git-scm.com/docs/gitremote-helpers#_commands)

`import` / `export` are intended to reconstruct history through fast-import
streams. They introduce marks and signed-tag handling that the direct object
path does not need. Direct pack preservation is the simpler path to the
Recovered Repository's exact-object requirement.
[Git remote-helper import/export capabilities](https://git-scm.com/docs/gitremote-helpers#_capabilities)

### Exact objects and object IDs survive pack encryption

Git packfiles store commit, tree, tag, and blob objects either in full or as a
delta from a base object. A normal pack is self-contained, and an index gives
random access to the objects. `git index-pack` can build that index from a
decrypted pack and install it in the repository.
[Git pack format](https://git-scm.com/docs/gitformat-pack),
[git-pack-objects](https://git-scm.com/docs/git-pack-objects#_description),
[git-index-pack](https://git-scm.com/docs/git-index-pack#_description)

Therefore, authenticated encryption of the packfile bytes is an outer storage
encoding: decrypting the bytes and indexing the resulting pack reconstructs
the same Git object contents and hence the same object IDs. Pack encoding is
not the Git object's identity. This conclusion depends on byte-exact
decryption and validation before installation, not on recreating commits or
trees from parsed fields.

This directly preserves:

- commit and annotated-tag signatures;
- commit messages, authors, committers, timestamps, parent ordering, tree
  ordering, file modes, symlinks, and gitlinks; and
- the original ref target OIDs, when the encrypted manifest restores them.

No original OID should be used as a hosted filename or hosted ref: even though
an OID is not the plaintext itself, publishing it would disclose a stable
fingerprint of the original object.

### Native ref and push semantics are expressible

The helper can report decrypted branches, tags, and `HEAD` through `list`, then
receive creates, updates, forced updates, and deletions through `push`. Git
performs client-side fast-forward/lease preparation from that advertised ref
set, but the helper remains responsible for authoritative validation after it
has refreshed remote state.
[Git remote-helper push command](https://git-scm.com/docs/gitremote-helpers#_commands),
[Git push refspec rules](https://git-scm.com/docs/git-push#_options)

On the storage side, Git receive-pack validates that a ref still has the
advertised old object ID before updating it. This permits a single hosted
storage ref to act as a compare-and-swap serialization point.
[Git pack protocol push validation](https://git-scm.com/docs/gitprotocol-pack#_pushing_data_to_a_server)

## Viable hosted representation

The following is an architecture consequence of the protocol facts, not a
format specification:

```text
refs/heads/cloak-v1
  -> synthetic state commit N
       parent: synthetic state commit N-1
       tree:
         format                 # non-secret compatibility marker
         manifest/<opaque-id>   # authenticated encrypted logical refs/catalog
         packs/<opaque-id>/...  # authenticated encrypted pack chunks
```

One fixed branch maximizes compatibility with ordinary Git hosts. Each state
commit uses a constant non-sensitive message and identity. Its parent is the
previous storage state, so an ordinary fast-forward push gives optimistic
concurrency: two writers starting from the same tip cannot both advance the
storage ref.

All accepted logical ref updates in one helper push batch are encoded in one
new manifest and one new storage commit. This makes the logical update atomic
at the storage boundary even though many plaintext refs changed. If a
non-atomic logical push contains both accepted and rejected refs, the manifest
can contain the accepted subset and the helper can report per-ref statuses.
If `--atomic` was requested, any logical rejection must prevent the entire
state update.

The Repository Host only sees the fixed storage branch, synthetic commits,
opaque paths, ciphertext sizes, and update timing. Original history topology
and refs remain recoverable from the encrypted manifest and packs.

### Bootstrap and ordinary transports

`git-remote-cloak init` should seed the known storage ref. Thereafter the
helper can:

1. unwrap `cloak::https://...`, `cloak::ssh://...`, or another native Git URL;
2. fetch only the known storage ref into a local shadow repository using the
   installed Git client and its normal HTTPS/SSH authentication;
3. decrypt the latest manifest locally;
4. answer `list` with the logical plaintext refs; and
5. fetch or push encrypted pack chunks through ordinary Git commands against
   that shadow.

This avoids provider APIs. It also avoids helper recursion because child Git
commands use the unwrapped native URL, not the `cloak::` URL.

Branch protection or repository rules applied to the fixed storage branch can
still reject updates. That is normal host policy, not something the helper
protocol bypasses.

## Transfer behavior

### Fresh full clone

The helper first needs only the storage tip and encrypted manifest to answer
`list`. When Git asks for the advertised tips, the helper must download,
authenticate, decrypt, and index every encrypted pack chunk needed for the
selected reachable logical graph.

A fresh full clone has no prior ciphertext objects with which to negotiate, so
it ultimately transfers the entire live encrypted pack set. It does not need
obsolete packs that a later compaction has made unreachable from the current
storage state, subject to the Repository Host's own retention behavior.

### Incremental fetch

An Authorized Host that retains the previous storage tip and shadow objects can
use ordinary Git negotiation on the **wrapper graph** to fetch only new state
commits, manifests, and encrypted pack chunks. It then decrypts and installs
only packs not already present locally.

This efficiency depends on a persistent local cache or shadow ref. If the
helper discards all wrapper state after every command, the backend cannot know
which encrypted blobs the client already has and will tend toward repeated
downloads. The cache is therefore an architectural requirement for efficient
routine use, not merely an optimization.

### Incremental push

After refreshing and decrypting the remote manifest, the helper knows the
logical remote refs and object inventory. It can select objects reachable from
the requested local tips but absent from that inventory, produce a Git pack,
encrypt it, add its chunks and a new encrypted manifest to one synthetic state
commit, and fast-forward the fixed storage ref.

If the hosted storage tip changed during this operation, ordinary receive-pack
rejects the stale update. The helper must not report success before the hosted
update is durable. An automatic retry is safe only after refetching and
revalidating every logical ref's fast-forward or lease condition.

## Shallow clone semantics

There is no reason to impose a Cloak-specific ban on shallow clones.

Git defines a shallow repository by listing boundary commit OIDs in
`$GIT_DIR/shallow`; the commit objects still contain their real parent fields,
but traversal treats the listed commits as roots. A depth-limited clone
contains commit chains only to the requested depth and writes this boundary
file.
[Git technical shallow documentation](https://github.com/git/git/blob/master/Documentation/technical/shallow.adoc),
[git-clone `--depth`](https://git-scm.com/docs/git-clone#Documentation/git-clone.txt---depthltdepthgt)

The remote-helper protocol explicitly supplies `depth`, `deepen-since`,
`deepen-not`, and `deepen-relative` through `option`. Thus the helper has the
information needed to expose ordinary shallow semantics.
[Git remote-helper shallow options](https://git-scm.com/docs/gitremote-helpers#_options)

For `--depth=1`, the helper must install the selected tip commit, its complete
tree and all blobs needed for the current checkout, then record that tip as a
shallow boundary. The current file snapshot is therefore complete; older
commits are what is omitted. Deepen and unshallow should behave like ordinary
Git.

What does **not** follow automatically is transfer efficiency. If one encrypted
blob contains a large pack spanning all history, the helper may have to
download and decrypt that whole blob even though it installs only depth-one
objects. Smaller immutable packs plus an encrypted object-to-pack catalog can
reduce this overfetch, but only a prototype can determine the correct
granularity. This is a performance concern, not a user-visible semantic
restriction.

## Storage and delta consequences of pack-first encryption

Pack-first encryption keeps Git's compression and delta selection **inside
each plaintext pack**. The cryptographic framing overhead should be small and
linear in the number of authenticated chunks, but its exact byte cost belongs
to the cryptographic-format decision.

The dominant risks are elsewhere:

1. **Do not encrypt a new full-repository pack on every push.** Authenticated
   ciphertext is intentionally high-entropy; a small plaintext change changes
   the ciphertext, so the hosted Git repository cannot usefully delta or
   compress successive encrypted snapshots. Storage and upload would approach
   the sum of all snapshots.
2. **Append immutable incremental packs.** Reusing prior encrypted blobs and
   adding only a pack of new logical objects preserves incremental network and
   storage behavior.
3. **Self-contained incremental packs lose cross-push deltas.** Git can delta
   objects within the new pack, but an old Markdown blob in a previous
   encrypted pack is unavailable to pack generation as an included base.
   Repeated small edits may therefore occupy more space than a fully repacked
   ordinary repository.
4. **Thin packs trade space for dependency complexity.** Git can create a thin
   pack whose delta bases are omitted, and `git index-pack --fix-thin` can add
   bases when installing it. This can recover cross-push deltas, but restore
   order and base availability become part of the format.
   [git-index-pack `--fix-thin`](https://git-scm.com/docs/git-index-pack#Documentation/git-index-pack.txt---fix-thin)
5. **Compaction recovers delta quality but rewrites ciphertext.** Periodically
   creating a new full pack can rediscover deltas across all live objects.
   Old ciphertext remains stored while old synthetic commits keep it
   reachable, so compaction must deliberately define a new storage-root
   retention policy.
6. **Encrypted packs must be chunked for hosted Git.** GitHub currently enforces
   a 100 MiB maximum for a single Git object and a 2 GiB maximum push. Even a
   small-file repository can eventually produce a pack larger than the
   single-object limit. Cloak therefore needs bounded authenticated chunks,
   not one hosted blob per arbitrarily large pack.
   [GitHub repository limits](https://docs.github.com/en/repositories/creating-and-managing-repositories/repository-limits)

For a mostly-Markdown personal repository, encryption bytes themselves should
be negligible relative to the Git pack. The capacity multiplier cannot be
promised before benchmarking because it is controlled mainly by pack
granularity, cross-push delta loss, and compaction—not by the authentication tag
or nonce.

## Integrity and failure boundaries

- Authenticated encryption can detect modified packs and manifests, but the
  ordinary Git host validates only the synthetic wrapper graph. It cannot
  validate logical refs or plaintext object connectivity.
- A malicious or faulty host can delete data, refuse service, or replay an
  older valid storage state. A returning Authorized Host can reject
  non-descendant rollback using its cached last-seen storage tip. A fresh
  machine holding only the Recovery Secret has no external freshness anchor
  and cannot distinguish the latest valid state from a replayed older valid
  state.
- A push must upload objects before publishing the manifest/state that
  references them, and must report logical success only after the storage ref
  update succeeds. Temporary uploaded-but-unreferenced wrapper objects are
  acceptable garbage after a failed race.
- The helper must validate decrypted pack integrity and logical graph
  connectivity before updating local refs. Git's fetch helper interface allows
  a `.keep` file to protect a newly installed pack from concurrent repack until
  ref updates finish.
  [Git remote-helper fetch command](https://git-scm.com/docs/gitremote-helpers#_commands)
- A direct plaintext push to the hosted repository's storage branch can corrupt
  protocol state or expose plaintext. Repository setup should make accidental
  use conspicuous, and all readers must fail closed on an invalid format marker,
  manifest, authentication tag, or pack.

## Prototype hypotheses to validate

The following are not guaranteed merely by choosing the remote-helper API:

1. A helper advertising `fetch`, `push`, `option`, and
   `check-connectivity` can pass round trips for clone, fetch, pull, ordinary
   push, new/delete/tag/force updates, `--force-with-lease`, and `--atomic`
   while preserving exact original OIDs.
2. A one-ref synthetic state chain correctly serializes two concurrent writers
   on local bare, GitHub, GitLab, HTTPS, and SSH remotes, and maps a stale
   storage rejection back to useful logical-ref errors.
3. `--depth=1`, deepen, and unshallow produce the same worktree and shallow
   boundary as a normal repository; pushing a new commit from a shallow clone
   follows ordinary Git behavior.
4. The helper can safely write/index packs and update `$GIT_DIR/shallow` within
   the process lifecycle Git gives a direct `fetch` helper.
5. Persistent wrapper cache negotiation avoids re-downloading unchanged
   encrypted chunks, and cache loss degrades only performance, not correctness.
6. Self-contained incremental packs versus thin incremental packs plus
   compaction are benchmarked on Markdown-heavy history with a few binaries.
7. Authenticated chunk sizing stays below provider object limits and permits
   streaming encryption/decryption without loading a full pack in memory.

## Decision for the next ticket

Proceed with the remote-helper architecture and pack-first storage hypothesis.
Choose a ciphertext representation that has:

- one fixed transactional storage ref;
- an authenticated encrypted logical-ref and pack catalog;
- immutable, bounded encrypted pack chunks;
- persistent local wrapper/cache state;
- exact pack-byte recovery and validation; and
- explicit versioning, compaction, rollback-detection, and shallow-boundary
  rules.

Do not claim production feasibility until the round-trip and benchmark tickets
validate the hypotheses above.
