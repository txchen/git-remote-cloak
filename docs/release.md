# Release and operating contract

## Supported scope

V1 is a single-owner private Git backup for small repositories. Release `v0.1.0` is operationally verified on Linux amd64 with GitHub HTTPS. Linux arm64 and macOS archives are published, but they are not part of the current manual provider-validation claim. Windows and WSL are not currently certified. Each private submodule uses an independent Ciphertext Repository and Recovery Secret.

From v0.2.0, an Authorized Host normally reads its repository-local `.git/cloak/secret`. Init and clone persist generated or supplied Secrets with permissions `0600`; linked worktrees share their common Git directory's Secret. Explicit `CLOAK_RECOVERY_SECRET`, `CLOAK_RECOVERY_SECRET_FILE`, or `--secret-file` inputs override local storage. The remote helper never prompts. Dedicated `secret` and `secret.pending` files contain credentials; Git configuration, command arguments, logs, caches, and general transaction journals must not.

Git LFS and partial clones/promisor objects are rejected. Ordinary Git binary blobs are supported.

## Security and operational limits

The Repository Host can observe the fixed Storage Ref, public format capabilities, random Repository ID, ciphertext identifiers and sizes, Storage History commit identities, topology and commit count, timing, change patterns, and repository growth. V1 does not hide traffic, sizes, or access patterns.

A fresh clone can authenticate a Ciphertext Snapshot but cannot prove that it is the newest valid Ciphertext Snapshot. Known rollback is detected only after an Authorized Host has retained a trusted Rollback Checkpoint. Host availability, quotas, authentication, branch protection, history retention, and garbage collection remain provider concerns. Compaction, Rekey, deletion, and Format Migration cannot guarantee that a Repository Host erases superseded ciphertext or immediately returns quota.

An Authorized Host whose Rollback Checkpoint predates a parentless Compaction may need the Repository Host to retain the authenticated prior Storage commits linked by that Compaction. If the host no longer provides a required commit, Cloak fails closed instead of discarding trusted rollback state.

## Cryptography and binary dependencies

Format v1.0 uses AES-256-GCM-SIV through the pinned pure-Go Tink dependency, with key derivation from `golang.org/x/crypto`. The checksummed release binary is built with `CGO_ENABLED=0`, `-mod=readonly`, Go 1.26.5, and the module versions in `go.sum`. It does not require CGo, a native cryptographic library, a Python prototype, or another Cloak executable. It does require the standard `git` executable for Git plumbing and transport.

`git-remote-cloak version` reports release version, source commit, build time, Go version, target platform, CGo status, and exact readable/writable format capabilities. From the directory containing the downloaded archive and `checksums.txt`, verify before extraction:

```sh
sha256sum --check checksums.txt
```

On macOS, use `shasum -a 256 -c checksums.txt`.

## Release gate

The accepted `v0.1.0` release gate is:

1. Complete the local production matrix on Linux.
2. Run the production release binary through init, clone, push, fetch, concurrent publication, Compaction, Rekey, format inspection, and privacy inspection against a disposable private GitHub repository over HTTPS.
3. Build checksummed CGo-free archives from pinned Go and module dependencies.
4. Create an annotated `vMAJOR.MINOR.PATCH` tag on the verified commit.
5. Download the published assets, verify every checksum, and run the published Linux amd64 binary.

Broader GitHub/GitLab × SSH/HTTPS certification remains available through the [provider certification runbook](provider-certification.md), but is outside the current `v0.1.0` support claim.

## v0.4.0 changes

Fetch, pull, and push reuse the authenticated Ciphertext Snapshot from the remote helper's initial inspection. A concurrent Storage Ref update still triggers a fresh read and compare-and-swap retry. Fetch no longer repeats full Logical Repository validation after the snapshot has already passed it. Git object existence checks and Git LFS pointer reads now use bounded batch operations, reducing Git subprocesses as repositories grow. Ciphertext Repository format remains v1.0.

## v0.3.3 changes

Repeated inspection now reuses the cached Bootstrap Header when its bytes match the current Storage commit's Git tree. An absent or damaged cache entry falls back to the Repository Host and is repaired after authentication. This removes the remaining per-inspection blob fetch for an unchanged snapshot; Ciphertext Repository format remains v1.0.

## v0.3.2 changes

Transaction journal reconciliation skips unused Compaction history checks and checks other prepared commits only in the fetched Storage History. This prevents an implicit remote fetch during an unchanged push. Ciphertext Repository format remains v1.0.

## v0.3.1 changes

Repeated inspection of an unchanged Ciphertext Snapshot now reuses the existing trusted Rollback Checkpoint and bootstrap cache file instead of rewriting them. Debug logging separates checkpoint validation, cache update, and transaction reconciliation timings to diagnose slow no-change pushes. The original snapshot authentication and rollback checks still run. Ciphertext Repository format remains v1.0.

## v0.3.0 changes

Automatic Compaction now waits until a push would create a thirty-third live Pack Payload, or until the snapshot has at least eight live Pack Payloads and added ciphertext reaches both 1 MiB and half the previous compacted snapshot size. Small incremental pushes and isolated large additions remain incremental. Manual Compaction and the Ciphertext Repository format are unchanged. This release also includes the filtered Storage Transport optimization and opt-in diagnostic logging from v0.2.2.

## v0.2.2 changes

Filtered Storage Transport clones now batch missing ciphertext blob fetches, reducing repeated network sessions during push inspection. Set `CLOAK_LOG=debug` on a Git or Cloak command to print stage timings and aggregate object counts to stderr for diagnosis. The diagnostic logger does not emit Recovery Secrets, Protected Plaintext, Git arguments, or Repository Host URLs. Ciphertext Repository format remains v1.0.

## v0.2.1 changes

Storage History commits now use the invoking Git repository's configured author and committer identity, including repository-local `user.name` and `user.email`. Without configured identity, they retain the anonymous `git-remote-cloak <cloak@invalid>` default. Hosts that require a recognized email can accept Cloak publications without a Cloak-specific setting. Original Logical Repository commit identities remain encrypted and unchanged. The chosen outer identity is visible to the Repository Host. Ciphertext Repository format remains v1.0.

## v0.2.0 changes

Repository-local Recovery Secrets remove environment switching from daily Git operations. Interactive clone asks for the Recovery Mnemonic once using hidden input. Init and clone save a protected local copy, while Rekey durably stages its new Secret before publication and automatically updates the active copy after success or recovery from a lost response. Offline Recovery Mnemonic backups remain required.

Acceptance tests cover independent repositories, directory moves, linked worktrees, hidden clone input, invalid local files, explicit overrides, and Rekey process-exit faults. Release smoke tests exercise push and fetch without Secret environment variables. Ciphertext Repository format remains v1.0; the historical provider-certification scope below is unchanged.

## v0.1.1 changes

Version `v0.1.1` fixes local HEAD changes during helper operations, Rollback
Checkpoint bypass in empty workspaces, and concurrent checkpoint regression.
Push sources now accept Git revisions, including detached `HEAD`, and remote
names may match CLI subcommands. Git subprocess repository-path isolation is
shared across storage and logical operations.

Each release also includes `install.sh` in `checksums.txt`. The installer selects
Linux/macOS amd64/arm64 archives and installs or upgrades without Go or sudo.
Ciphertext Repository format remains v1.0. These changes do not broaden the
manual provider-certification claim recorded for v0.1.0 above.

## Published release evidence

- [v0.1.0 release](https://github.com/txchen/git-remote-cloak/releases/tag/v0.1.0)
- [Linux, macOS, and release-artifact verification](https://github.com/txchen/git-remote-cloak/actions/runs/32281748056)
- [Immutable release publication](https://github.com/txchen/git-remote-cloak/actions/runs/32285486928)
