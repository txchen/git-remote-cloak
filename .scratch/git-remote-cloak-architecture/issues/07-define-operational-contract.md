# Define the credential, recovery, and failure contract

Type: grilling
Status: resolved
Blocked by: 03, 05

## Question

What exact v1 operational contract makes Cloak safe enough and convenient for both a person and a rebooting service?

Specify `init` and `clone`, environment/file credential inputs and precedence, secret masking and validation, fail-closed behavior, local cache lifecycle, concurrent writers, interrupted push/fetch recovery, rollback and corruption diagnosis, full re-cloak rotation, provider rejection handling, and LFS rejection. Define automatic and manual compaction triggers, safe replacement-snapshot validation and Storage Ref re-rooting, and truthful reporting when Repository Host retention delays physical quota recovery. Keep ordinary Git CLI behavior as the service-facing interface.

## Answer

V1 uses one non-interactive remote-helper contract for ordinary Git and friendly explicit commands for human setup and recovery. An Authorized Host must always possess the per-repository Recovery Secret outside the repository; Cloak never persists the Secret in Git configuration, its cache, its journal, or the Ciphertext Repository.

### Secret acquisition and validation

The accepted Secret sources are:

- `CLOAK_RECOVERY_SECRET`, containing the complete versioned mnemonic;
- `CLOAK_RECOVERY_SECRET_FILE`, containing a path whose file content is the mnemonic; and
- `--secret-file PATH` on the explicit `init` and `clone` commands.

Cloak does not accept a literal Secret command-line argument because process lists and shell history can expose it. More than one configured source is an error rather than a precedence rule. A Secret file that is group- or world-readable produces a security warning but remains usable for container and service compatibility. Directories, empty files, unreadable files, and invalid encodings fail.

`git-remote-cloak init` and `git-remote-cloak clone` may use a masked terminal prompt when no source is configured. Git-invoked remote-helper operations never prompt; they fail immediately so unattended services cannot hang. Interactive `init` generates a new 256-bit Recovery Secret with the operating system CSPRNG, displays the versioned 24-word mnemonic once, and requires confirmation that it was saved before publication. Non-interactive `init` requires a pre-supplied Secret and never prints one.

Mnemonic prefix, word encoding, and checksum errors are reported as invalid format. A well-formed Secret that cannot authenticate the Bootstrap Header reports that the Secret does not unlock the repository or that repository metadata is damaged; Cloak cannot safely distinguish those cases. Secrets, derived keys, and Secret-bearing environment values are always redacted.

### Initialization and clone

The human initialization form is:

```text
git-remote-cloak init <remote-name> <repository-url>
```

It runs inside an existing Git repository, including an unborn repository, initializes an empty Ciphertext Snapshot, and configures `<remote-name>` as `cloak::<repository-url>`. It does not modify the worktree, index, commits, branches, or tags and does not automatically push any local ref. An existing remote with the same Cloak URL is idempotent; Cloak does not overwrite a remote name bound to another URL.

The Repository Host repository must have no refs, or it must contain only a valid `refs/heads/cloak-storage` belonging to the same Repository ID and Secret. Any other ref causes `init` to fail, and v1 has no `init --force`. `init` records the current symbolic local `HEAD` as encrypted Logical HEAD without publishing its branch. A detached `HEAD` requires `--default-branch`. A later `git-remote-cloak set-head <remote-name> <branch>` may select an existing remote Logical Ref.

Humans may clone with:

```text
git-remote-cloak clone <repository-url> [directory]
```

Services may use:

```text
git clone cloak::<repository-url> [directory]
```

Both recover the same ordinary plaintext repository and configure `origin` as the Cloak URL. Recovery is staged in a mode-`0700` temporary directory. Cloak authenticates all metadata and chunks, imports the packs, restores the advertised Logical Refs and Logical HEAD, compares expected ref targets, and runs `git fsck --full` before atomically renaming the completed directory into place. A non-empty destination is never merged or overwritten. Failure removes Cloak-created temporary plaintext while retaining only independently validated ciphertext cache entries.

### Persistent local state and crash recovery

Each Logical Repository owns a persistent `.git/cloak/cache/` containing the wrapper Git objects, encrypted chunks, indexes, and authenticated metadata needed for efficient incremental operation. It contains no Recovery Secret, survives service restart, is never committed, and is never shared globally in v1. Loss or corruption degrades performance only: Cloak isolates invalid cache state and rebuilds it from the current Ciphertext Snapshot. `git-remote-cloak cache clear` removes only reconstructable cache data.

`.git/cloak/transactions/` contains Secret-free crash journals. A push records its intended Logical Ref transaction, starting Storage Ref, and prepared Storage commit. Interruption before Storage Ref publication leaves logical state unchanged. If publication succeeded but the response was lost, a retry recognizes the prepared commit in current Storage History or rechecks the authenticated Logical Refs. Fetch installs objects and updates refs only after all integrity checks pass. Cloak reports push success only after Storage Ref publication is confirmed.

### Concurrent publication

Writers use optimistic concurrency around the one Storage Ref. Each push reads and authenticates the latest manifest, validates ordinary fast-forward, force, and force-with-lease rules against the current Logical Refs, uploads immutable ciphertext, and compare-and-swap updates `cloak-storage`.

If another writer wins, Cloak refetches and revalidates. Compatible updates, such as different branches, may be rebuilt and retried up to three times. Divergent updates return ordinary non-fast-forward or stale-lease failures; Cloak never merges logical commits. A multi-ref push is published through one manifest and one Storage Ref update, so every requested Logical Ref update succeeds or none does. Failed attempts may leave unreachable ciphertext but cannot make it part of the current snapshot.

### Rollback and corruption

`.git/cloak/state` stores the authenticated Repository ID, highest observed storage generation, and last-seen Storage Ref. Generation regression, same-generation substitution, or unexplained Storage History reversal fails closed as suspected rollback. A legitimate compaction uses a higher generation and authenticates the Storage Ref it replaces.

`git-remote-cloak status` can export the checkpoint in a machine-readable form for services that keep trusted external state. Without either a local or external checkpoint, a fresh clone can prove that a snapshot authenticates under the Recovery Secret but cannot prove that the Repository Host returned the newest snapshot. Cloak must state that limitation rather than claiming rollback protection.

`git-remote-cloak doctor <repository-url>` is read-only and supports structured output. It diagnoses header and manifest authentication, missing ciphertext, chunk integrity, pack/index consistency, Logical Ref targets, and the recovered Git object graph. It may identify an older authenticated recoverable generation, but normal operations never silently downgrade. Explicit recovery of an older generation creates a separate local repository and does not mutate the remote; remote repair or rollback requires a separate destructive command.

### Rekey

Rekey treats a complete local Logical Repository as the authoritative source and does not require the old Recovery Secret. By default it republishes all local `refs/heads/*` and `refs/tags/*`. It excludes remote-tracking refs, reflogs, stash, bisect state, and other local operational refs; Git notes or custom refs require explicit refspecs. Cloak prints the complete selected ref set before destructive confirmation and warns when remote-tracking branches have no corresponding local branch.

Rekey generates or accepts a new Recovery Secret and a new random Repository ID, builds one complete compacted Ciphertext Snapshot from the selected local refs, restores it locally, compares every selected ref and reachable object ID, and runs `git fsck --full`. Only then does it compare-and-swap force-replace `cloak-storage` with the new parentless snapshot at generation one. The Git remote URL stays the same, but this is deliberately a new Ciphertext Repository identity; the confirmed destructive operation replaces the local Rollback Checkpoint. New ciphertext is uploaded before the atomic replacement, so interruption cannot erase the current backup. Repository Host retention may preserve superseded ciphertext decryptable with the old Secret; Cloak cannot promise erasure or immediate quota recovery.

### Compaction

Compaction keeps the Recovery Secret, Repository ID, Logical Refs, Logical HEAD, commits, and original object IDs unchanged. It replaces fragmented Pack Payloads with one validated optimized Pack Payload and force-re-roots only Storage History.

Automatic compaction is synchronous work performed by the remote helper before a logical push, not a daemon. It triggers before creating a thirty-third live Pack Payload or when ciphertext added since the last compaction reaches 50% of the previous compacted snapshot size. The second trigger protects binary-heavy repositories. A failed compaction leaves the old Storage Ref and the requested logical push unchanged. Progress reports packing, encryption, upload, validation, and publication separately.

Users may run `git-remote-cloak compact <remote-name>` at any time. A service may set `remote.<name>.cloakAutoCompact=false` and compact during its own maintenance window; pushes continue with explicit capacity warnings after the threshold.

### Provider and feature failures

Underlying Git, SSH, HTTPS, and credential helpers own Repository Host authentication. Cloak never mixes those credentials with the Recovery Secret. Object upload failure leaves Storage Ref unchanged. Quota, object-size, authentication, branch-protection, and ref-update rejection are reported without bypass attempts. Diagnostics may include provider errors, ciphertext identifiers, and ciphertext sizes but no Protected Plaintext or Secret material. Safe immutable-object uploads may be retried a bounded number of times; ref or authentication rejection is never retried indefinitely.

V1 detects actual Git LFS pointer blobs among newly reachable objects and rejects the entire push before Storage Ref publication, naming affected local paths in the local error. Clone, fetch, and `doctor` also reject an existing Logical Repository that depends on LFS content. There is no bypass flag; ordinary binary blobs stored in Git remain supported.

Shallow clone is supported with ordinary semantics: limited commit history and a complete current checkout. Partial clone filters and promisor behavior are explicitly unsupported in v1 and fail rather than silently becoming a full clone.
