# Existing private Git backup solutions

Research date: 2026-07-25

## Question

Which maintained existing tools or designs can satisfy, or usefully inform, the Cloak contract: an ordinary plaintext Git repository locally; a standard GitHub, GitLab, or self-hosted Git remote that exposes no plaintext file content, original path, or commit message; and exact recovery of the pushed Git object graph?

Only first-party project documentation, source repositories, and Git specifications were used.

## Executive finding

No maintained tool should be adopted unchanged.

`git-remote-gcrypt` is the only surveyed product that directly demonstrates the right architectural shape: Git invokes a remote helper; the helper converts Git objects into packs; packs and ref metadata are encrypted before an arbitrary Git transport stores them. Its documented format encrypts each Git pack under a random symmetric key, names the ciphertext by its SHA-256 hash, and places refs plus pack keys in an encrypted, signed manifest. It can bridge this representation through an arbitrary Git URL, including a hosted Git repository ([official README](https://github.com/spwhitton/git-remote-gcrypt#description), [repository format](https://github.com/spwhitton/git-remote-gcrypt#repository-format)).

That is strong feasibility evidence, but not an acceptable v1 implementation:

- For arbitrary Git URLs—the mode required for GitHub and GitLab—the project warns that every push uploads the entire repository history and eventually becomes impractical. The more efficient `rsync://` backend does not work with GitHub or GitLab ([performance warning](https://github.com/spwhitton/git-remote-gcrypt#performance)).
- It documents a longstanding bug where every push is effectively a force push, with an opt-in flag that merely requires the caller to spell `--force` ([configuration](https://github.com/spwhitton/git-remote-gcrypt#configuration), [known issues](https://github.com/spwhitton/git-remote-gcrypt#known-issues)).
- Its trust and credential model is GPG participants plus signatures, not Cloak's single per-repository 256-bit Recovery Secret. The executable is a POSIX shell program and additionally requires GnuPG and backend-specific programs, conflicting with the one-binary Linux/macOS service contract ([dependencies and GnuPG](https://github.com/spwhitton/git-remote-gcrypt#notes)).
- Its source is GPL-licensed, so copying implementation rather than independently adopting the design would bring GPL obligations ([license](https://github.com/spwhitton/git-remote-gcrypt#license)).

The recommendation is therefore **build a new `git-remote-cloak`, while adopting the remote-helper and pack-first/encrypt-second architecture—not the `git-remote-gcrypt` code, GPG key model, remote format, or push semantics**.

## Comparison

| Candidate | Protected at Repository Host | Exact Git recovery and ordinary Git UX | Efficiency and operational constraints | Decision |
| --- | --- | --- | --- | --- |
| `git-remote-gcrypt` | Protects pack contents and encrypted-manifest refs. Because paths and messages live inside the original Git packs, both are protected. | Push/pull are exposed through `gcrypt::`; decrypting the original packs implies the original Git objects and IDs are recovered. Arbitrary Git transports are supported. | Hosted-Git mode uploads the whole history on each push; every push is effectively forced; GPG, shell, and helper dependencies do not match the Recovery Secret/service contract. | **Adopt the architectural precedent; reject unchanged product/code.** |
| `git-crypt` | Encrypts selected blob contents only. It explicitly does **not** encrypt filenames, commit messages, symlink targets, gitlinks, or other metadata. | Local checkout is transparent after unlock, but the hosted repository remains the original Git graph with visible paths/messages. | Its own documentation says filters are not suited to encrypting most/all files. Deterministic encrypted blobs are not compressible, so a small change stores the whole encrypted file rather than a Git delta. | **Reject.** |
| `transcrypt` | Encrypts selected file contents through clean/smudge filters; tracked paths, `.gitattributes`, commits, and messages stay visible. | Normal Git works for configured files, but it is not whole-repository protection or exact cloaking. | The project warns that its default AES-256-CBC mode is unauthenticated and malleable, and says there are better options for whole-repository encryption. | **Reject.** |
| `git-secret` | Produces tracked `.secret` files for explicitly listed plaintext files. Original paths are recorded by the setup and Git history/messages remain ordinary plaintext metadata. | Requires explicit hide/reveal workflow and only restores selected files, not a cloaked Git object graph. | GPG-oriented multi-recipient secret-file workflow; no Git transport layer. | **Reject.** |
| SOPS | Encrypts file values or a whole binary payload. Structured-file keys are intentionally left in cleartext; filenames and Git commit objects are outside its boundary. | Useful for versioning selected configuration secrets, not for cloning/fetching a protected repository. | Binary mode base64-encodes encrypted bytes and can increase file size; per-file data-key envelopes do not match the selected direct-key model. | **Reject.** |
| gocryptfs reverse mode / rclone crypt | Both can protect file contents and filenames in an encrypted directory/remote view. | They can copy an encrypted view of a directory, but do not implement Git ref negotiation, fetch, push, or clone. Backing up `.git` this way is a filesystem snapshot workflow, not an ordinary Git remote. | gocryptfs reverse mode is read-only and FUSE-based; rclone requires all access to pass through its crypt remote. Their filename formats are useful design evidence but not a Git representation. | **Use as filename/storage references only.** |
| restic / Borg | Client-side encryption covers backup data and metadata; both support deduplication against untrusted storage. | They restore backup snapshots, not Git refs through `git clone`, `fetch`, `pull`, and `push`. Their repositories are custom backup stores, not standard hosted Git repositories. | Both demonstrate encrypted, deduplicated object stores and authenticated metadata. Borg's native remote is SSH with Borg on the server; restic uses its own backends/repository format. | **Use as repository-format and failure-ordering references only.** |
| git-annex encrypted special remotes | Protects annexed file contents and hashes their remote filenames, but the primary Git repository still exposes all filenames and history. | Requires git-annex semantics and covers annexed payloads, not the ordinary Git object graph. | Its documentation explicitly says anyone with the Git repository can see filenames/history; encryption is for content held by a separate special remote. | **Reject.** |
| Git bundle/pack plus encryption | Once the bundle/pack is encrypted, all enclosed blob, tree, commit, and tag plaintext is protected. | A full bundle contains reachable refs/objects and can be cloned/fetched; incremental bundles are supported. Git cannot push into a bundle. | Preserves Git's own compression and delta selection before encryption. A helper still has to manage immutable encrypted packs, encrypted refs, concurrency, and compaction. | **Adopt Git pack plumbing as the core substrate; build the missing helper/format.** |

## Evidence by family

### 1. Git remote encryption helper

Git's official remote-helper protocol is explicitly extensible without relinking Git. A `cloak::<address>` URL causes Git to invoke `git-remote-cloak`; helper capabilities cover ref listing, object fetch, push, and the `depth` option used by shallow operations ([Git remote-helper invocation](https://git-scm.com/docs/gitremote-helpers#_invocation), [push/fetch capabilities](https://git-scm.com/docs/gitremote-helpers#_capabilities), [miscellaneous capabilities](https://git-scm.com/docs/gitremote-helpers#_miscellaneous_capabilities)).

`git-remote-gcrypt` proves that this seam can put an encrypted representation behind standard Git transports. Its stored plaintext packs are the original Git packs after decryption, not a rewritten working-tree export. Therefore, **as an inference from the documented format**, importing those packs restores the original object bytes and object IDs. Its encrypted manifest also carries the original ref names and object IDs, so those values are not exposed outside the manifest ([format write/read sequence](https://github.com/spwhitton/git-remote-gcrypt#repository-format), [manifest fields](https://github.com/spwhitton/git-remote-gcrypt#manifest-file)).

The right lesson is not “fork gcrypt.” It is:

1. Let Git produce or consume native packs.
2. Authenticate and encrypt whole packs before crossing the trust boundary.
3. Keep the authoritative ref-to-original-object mapping inside authenticated ciphertext.
4. Store only opaque, content-addressed ciphertext files in the hosted Git repository.

Cloak must then improve on the precedent with immutable incremental pack addition, real non-fast-forward validation, one direct Recovery Secret, a versioned authenticated manifest, and a single compiled binary.

### 2. Selective Git encryption

`git-crypt` is unusually explicit about the mismatch: it uses Git filters for selected files, does not encrypt filenames or commit messages, and recommends a whole-repository system such as `git-remote-gcrypt` when the entire repository must be protected. It also documents equality/change leakage, weak delta behavior for changed ciphertext, filter/GUI failure modes, and the need for repository-level tamper protection because an attacker can modify `.gitattributes` ([official security and limitations](https://github.com/AGWA/git-crypt#security), [current status](https://github.com/AGWA/git-crypt#current-status)).

`transcrypt` has the same filter boundary: `.gitattributes` names which paths are encrypted, and raw Git still addresses the encrypted blob by its original path. More importantly, its own caveats say whole-repository encryption has better options and that the default AES-256-CBC construction lacks authentication, allowing limited malicious manipulation of plaintext ([official overview and filter setup](https://github.com/elasticdog/transcrypt#overview), [caveats](https://github.com/elasticdog/transcrypt#caveats)).

`git-secret` asks users to add filenames, adds the plaintext files to `.gitignore`, and checks in corresponding `.secret` files plus repository metadata. Decryption is an explicit `reveal`; updating ciphertext is an explicit `hide` operation ([official usage](https://git-secret.io/git-secret), [reveal implementation](https://git-secret.io/git-secret-reveal)). It solves “selected secret files alongside public Git history,” not a private backup of the Git history itself.

SOPS is likewise scoped to encrypted documents. For YAML/JSON/ENV/INI it intentionally leaves keys in cleartext, uses their paths as authenticated context, and encrypts leaf values; binary mode wraps encrypted bytes in base64 JSON. It can read age identities from an environment variable, which is operationally useful evidence, but filenames and Git metadata remain unprotected ([official SOPS documentation](https://github.com/getsops/sops#readme), [project summary](https://getsops.io/)).

No filter/document tool can meet the contract because Git tree objects themselves carry filenames, while commit objects carry the message and identity metadata ([Git tree and commit object format](https://git-scm.com/book/en/v2/Git-Internals-Git-Objects#_tree_objects), [commit object format](https://git-scm.com/book/en/v2/Git-Internals-Git-Objects#_git_commit_objects)).

### 3. Encrypted filesystems and remotes

gocryptfs forward mode encrypts content and filenames, including special handling for ciphertext path components that exceed filesystem limits. Reverse mode exposes a deterministic, read-only encrypted view of a plaintext directory specifically for backups and uses AES-SIV for content ([forward-mode format](https://nuetzlich.net/gocryptfs/forward_mode_crypto/), [reverse-mode format](https://nuetzlich.net/gocryptfs/reverse_mode_crypto/)). This is valuable evidence for component-safe encodings, deterministic backup views, and long-name side records. It does not provide Git's remote protocol; macOS also introduces a FUSE dependency that conflicts with the one-binary contract.

rclone crypt encrypts both content and directory/file names when all access goes through the crypt wrapper. Its standard filename mode is deterministic, case-insensitive-safe modified base32; it explicitly distinguishes this from weak “obfuscation,” which must not be relied on for protection. It also warns that direct access to the wrapped remote bypasses encryption ([official crypt documentation](https://rclone.org/crypt/)). This supports Cloak's decision to use a keyed legal path-component encoding, but rclone's storage API is not a Git remote and its encrypted directory hierarchy is not a recoverable Git object graph.

Either tool could encrypt a point-in-time copy of an entire `.git` directory. That would be a viable generic filesystem backup, but it would require a second synchronization workflow, would not let the service use ordinary `git push/fetch/pull`, and would place crash-consistent `.git` snapshotting outside Git's own transfer semantics.

### 4. Backup repositories

restic and Borg satisfy much of the confidentiality problem under a different product contract. Restic uses encrypted, authenticated immutable files, opaque storage IDs, packs, indexes, and snapshots; its format specifies a safe write order of packs, then indexes, then snapshots so interrupted writes do not create reachable missing data ([restic design](https://github.com/restic/restic/blob/master/doc/design.rst)). Borg performs content-defined chunking, repository-wide deduplication, compression, and client-side authenticated encryption for untrusted targets ([Borg overview](https://borgbackup.readthedocs.io/en/stable/), [Borg internals](https://borgbackup.readthedocs.io/en/stable/internals.html)).

These are useful precedents for immutable ciphertext blobs, authenticated reachability metadata, deduplication, compaction, and failure ordering. They cannot be combined as a transparent GitHub/GitLab transport: Borg's efficient remote requires Borg on an SSH server, while restic uses its own repository backends and commands. A Git host cannot negotiate their snapshots as Git refs.

### 5. git-annex

git-annex encryption applies to payloads sent to a special remote. Its own documentation states that git-annex mostly does not use encryption and that anyone with the Git repository can see all filenames and history; encrypted special remotes encrypt file content and HMAC their remote filenames ([git-annex encryption](https://git-annex.branchable.com/encryption/)). Special remotes store and retrieve annexed content rather than replace ordinary Git object transport ([special remotes](https://git-annex.branchable.com/special_remotes/)). This directly violates the Cloak path/message threat model and the “no Git LFS-like alternate content model” product boundary.

### 6. Native Git bundles and packs

`git bundle` is the strongest reusable standard component. Git defines a bundle as a pack file plus a header describing refs. It supports self-contained full backups, incremental bundles with prerequisites, cloning, fetching, and listing refs; a full `--all` bundle captures all reachable refs/objects while intentionally excluding worktree/index/stash/hooks/config state, matching Cloak's recovery boundary. Git also states that there is no push-into-bundle support ([git-bundle description and format](https://git-scm.com/docs/git-bundle#_description), [full and incremental backup examples](https://git-scm.com/docs/git-bundle#_examples)).

Encrypting one full bundle and committing it to a private hosted repository would protect the required plaintext and recover exact Git objects, but would be a manual snapshot artifact, not a bidirectional Git remote. Replacing that bundle on each push also loses incremental storage behavior.

The better construction is bundle-like, not necessarily the bundle file format itself:

- append immutable encrypted native packs for newly reachable objects;
- publish a small authenticated encrypted manifest that names packs and maps refs;
- represent each hosted update as an ordinary outer Git commit with a constant, non-sensitive message and opaque paths;
- validate remote ref generations and original Git fast-forward rules before publishing;
- compact packs only as a separate, explicit operation.

This preserves Git's compression and delta selection **inside** each pack before encryption. As an inference from the formats, raw cryptographic expansion should be fixed bytes per pack/manifest and therefore well below 1% for multi-megabyte packs; the meaningful capacity risks are pack fragmentation, duplicate objects across increments, and retained superseded ciphertext after compaction—not AEAD tags themselves. Exact overhead must be benchmarked against realistic Markdown-heavy histories and occasional binary files.

## Adopt / combine / build recommendation

### Adopt

- Git's `git-remote-<transport>` discovery and fetch/push capability protocol.
- Native Git pack generation/import and object verification.
- The `git-remote-gcrypt` high-level precedent of encrypted packs plus an encrypted authenticated ref manifest.
- Immutable opaque ciphertext objects and safe publication ordering inspired by restic/Borg.
- Component-safe, deterministic keyed filename encoding lessons from gocryptfs/rclone, while keeping Cloak's repository-wide equality behavior and direct Recovery Secret model.

### Do not adopt

- Git clean/smudge filters, document encryption, or per-file secret tooling.
- A mounted encrypted filesystem or generic backup repository as the user-facing remote.
- git-annex special-remotes or any alternate large-file/content-pointer semantics.
- `git-remote-gcrypt`'s GPG participant/envelope model, POSIX-shell dependency stack, repository format, force-push behavior, or full-history-per-push hosted-Git strategy.
- A single replace-in-place encrypted bundle.

### Build

Build a new, independently specified `git-remote-cloak` and ciphertext repository format. The next architecture work should treat the following as mandatory acceptance criteria:

1. Decrypt/import produces byte-identical original Git objects and therefore identical object IDs.
2. Repository Host inspection finds no plaintext content, original path component, commit/tag message, or original ref name if ref names are considered protected by the final format.
3. Ordinary clone/fetch/pull/push, tags, force pushes, deletes, and shallow behavior route through the helper with Git-compatible results.
4. A normal push uploads only new encrypted pack/manifest data in steady state; full history upload is exceptional and explicit.
5. Concurrent or interrupted publication cannot make a new manifest reachable before every referenced encrypted pack is durable.
6. Authentication failure, missing secret, format mismatch, rollback evidence, or non-fast-forward update fails closed.

## Bottom line

The market already validates the concept, not the finished product. `git-remote-gcrypt` shows that a remote helper can preserve original Git objects while hiding their packs and refs behind standard Git hosting. Git bundle/pack plumbing supplies the exact-recovery substrate. Modern backup and encrypted-filesystem tools supply useful storage-format lessons. None simultaneously delivers Cloak's direct Recovery Secret, one-binary service operation, exact Git semantics, filename/message privacy, and efficient standard-host pushes. A focused new helper is justified.
