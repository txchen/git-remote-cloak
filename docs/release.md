# Release and operating contract

## Supported scope

V1 is a single-owner private Git backup for small repositories. Linux and macOS release archives are produced for amd64 and arm64. WSL is the supported Windows path; there is no native Windows binary. Each private submodule uses an independent Ciphertext Repository and Recovery Secret.

An Authorized Host may obtain the Recovery Secret from `CLOAK_RECOVERY_SECRET`, `CLOAK_RECOVERY_SECRET_FILE`, or `--secret-file` where the command permits it. A service is expected to persist one of the non-interactive sources in its own secret store. The remote helper never prompts, and the Recovery Secret must not be placed in Git configuration, command arguments, logs, caches, or journals.

Git LFS and partial clones/promisor objects are rejected. Ordinary Git binary blobs are supported.

## Security and operational limits

The Repository Host can observe the fixed Storage Ref, public format capabilities, random Repository ID, ciphertext identifiers and sizes, Storage History topology and commit count, timing, change patterns, and repository growth. V1 does not hide traffic, sizes, or access patterns.

A fresh clone can authenticate a Ciphertext Snapshot but cannot prove that it is the newest valid Ciphertext Snapshot. Known rollback is detected only after an Authorized Host has retained a trusted Rollback Checkpoint. Host availability, quotas, authentication, branch protection, history retention, and garbage collection remain provider concerns. Compaction, Rekey, deletion, and Format Migration cannot guarantee that a Repository Host erases superseded ciphertext or immediately returns quota.

## Cryptography and binary dependencies

Format v1.0 uses AES-256-GCM-SIV through the pinned pure-Go Tink dependency, with key derivation from `golang.org/x/crypto`. The checksummed release binary is built with `CGO_ENABLED=0`, `-mod=readonly`, Go 1.26.5, and the module versions in `go.sum`. It does not require CGo, a native cryptographic library, a Python prototype, or another Cloak executable. It does require the standard `git` executable for Git plumbing and transport.

`git-remote-cloak version` reports release version, source commit, build time, Go version, target platform, CGo status, and exact readable/writable format capabilities. Verify an archive before extraction:

```sh
sha256sum --check checksums.txt
```

On macOS, use `shasum -a 256 -c checksums.txt`.

## Release gate

Before tagging a release:

1. Complete the local production matrix on Linux and macOS.
2. Complete all four GitHub/GitLab × SSH/HTTPS provider-certification environments and save their run URLs.
3. Complete the WSL smoke test below.
4. Confirm the provider claim recorded in the release notes matches the passing matrix. A failed compare-and-swap, force-re-root, quota, or branch-protection fixture narrows the provider claim; it does not justify a provider API.
5. Create an annotated `vMAJOR.MINOR.PATCH` tag on the certified commit. The release workflow builds and checksums all four archives from that tag.

## WSL smoke test

On a clean, supported WSL distribution with Git installed, download the Linux archive matching `uname -m`, verify its checksum, put `git-remote-cloak` on `PATH`, and run:

```sh
git-remote-cloak version
scripts/smoke-release.sh "$(command -v git-remote-cloak)"
```

Record the Windows build, WSL version, distribution, kernel (`uname -a`), Git version, archive checksum, and test output in the release notes. This smoke test supports only the Linux binary inside WSL.
