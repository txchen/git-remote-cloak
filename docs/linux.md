# Linux operation and recovery guide

This guide is the configuration source of truth for the currently supported Linux amd64 path.

## Prerequisites

- Linux on x86-64 (`uname -m` reports `x86_64`);
- a standard `git` executable;
- an empty private repository on a Repository Host; and
- working Git authentication for that repository.

For GitHub HTTPS, authenticate Git before initializing Cloak:

```sh
gh auth login
gh auth setup-git
git ls-remote https://github.com/OWNER/REPOSITORY.git
```

The repository must have no refs. A README, license, or provider-created initial commit makes it non-empty.

## Install and verify

Follow the [README installation steps](../README.md#install-on-linux). Keep the binary on `PATH`; Git locates it by the required name `git-remote-cloak` when a `cloak::` remote is used.

Verify the exact binary before configuring a repository:

```sh
git-remote-cloak version --json
git-remote-cloak version --formats
```

For release `v0.1.0`, expect Linux amd64, CGo disabled, and exact read/write support for format v1.0.

## Initialize a backup

Run initialization inside the Plaintext Workspace:

```sh
git-remote-cloak init backup https://github.com/OWNER/EMPTY-PRIVATE-REPOSITORY.git
```

Interactive initialization generates a random Recovery Secret, shows its 24-word Recovery Mnemonic once, and publishes only after `SAVED` is entered. Initialization configures:

```text
remote.backup.url=cloak::https://github.com/OWNER/EMPTY-PRIVATE-REPOSITORY.git
```

It does not push a local branch, tag, commit, index entry, or worktree change. Make the first backup explicitly:

```sh
export CLOAK_RECOVERY_SECRET_FILE=/absolute/path/to/mode-0600-recovery-file
git push -u backup main
```

The Recovery Secret belongs in the Authorized Host's secret store. Keep it out of Git configuration, command arguments, logs, the repository, and shell history. The remote helper never prompts, so unattended `git push` and `git fetch` require `CLOAK_RECOVERY_SECRET` or `CLOAK_RECOVERY_SECRET_FILE`.

## Verify host privacy

Query the underlying Repository Host URL, not the `cloak::` remote:

```sh
git ls-remote https://github.com/OWNER/REPOSITORY.git
```

Only `refs/heads/cloak-storage` and the host's matching `HEAD` should be visible. Original branch names such as `main` must not appear.

## Daily Git operations

Keep the binary on `PATH` and the Recovery Secret source configured:

```sh
export PATH="$HOME/.local/bin:$PATH"
export CLOAK_RECOVERY_SECRET_FILE=/absolute/path/to/mode-0600-recovery-file

git push backup main
git fetch backup
git pull --ff-only backup main
```

Branches, tags, deletions, force pushes, and force-with-lease use ordinary Git syntax. Cloak does not choose a merge strategy or create logical merges.

Inspect the trusted local Rollback Checkpoint:

```sh
git-remote-cloak status
git-remote-cloak status --json
```

## Recover on another host

Install the same or a format-compatible binary, configure Git authentication, and provide the Recovery Secret:

```sh
git-remote-cloak clone \
  https://github.com/OWNER/REPOSITORY.git \
  recovered \
  --secret-file /absolute/path/to/mode-0600-recovery-file

git -C recovered fsck --full
```

A fresh clone authenticates the returned Ciphertext Snapshot but cannot independently prove that the Repository Host returned the newest valid snapshot. After the first observation, the local Rollback Checkpoint protects future observations.

Diagnose a repository without changing it:

```sh
export CLOAK_RECOVERY_SECRET_FILE=/absolute/path/to/mode-0600-recovery-file
git-remote-cloak doctor https://github.com/OWNER/REPOSITORY.git
git-remote-cloak doctor https://github.com/OWNER/REPOSITORY.git --json
```

## Maintenance

Compact live ciphertext while preserving the Recovery Secret and Logical Repository:

```sh
git-remote-cloak compact backup
```

Rekey replaces the Ciphertext Repository from the complete selected local refs with a new Recovery Secret and Repository ID:

```sh
unset CLOAK_RECOVERY_SECRET CLOAK_RECOVERY_SECRET_FILE
git-remote-cloak rekey backup
```

Read the displayed ref plan before confirming. Interactive Rekey displays a new Recovery Mnemonic once; save its complete `cloak-v1:` value before confirming it. To perform unattended Rekey, configure a newly generated Secret source rather than the current repository Secret. Repository Host retention may preserve superseded ciphertext after Compaction or Rekey.

Clear only reconstructable local ciphertext cache:

```sh
git-remote-cloak cache clear
```

## Upgrade

Download and verify the new release, replace the executable atomically, then check its exact capabilities:

```sh
install -m 755 git-remote-cloak "$HOME/.local/bin/git-remote-cloak.new"
mv "$HOME/.local/bin/git-remote-cloak.new" "$HOME/.local/bin/git-remote-cloak"
git-remote-cloak version
```

An ordinary binary upgrade never rewrites repository format. Format Migration is explicit.

## Common failures

`multiple Recovery Secret sources configured`
: Keep exactly one of `CLOAK_RECOVERY_SECRET`, `CLOAK_RECOVERY_SECRET_FILE`, or command-specific `--secret-file`.

`Recovery Secret is required in non-interactive mode`
: Export a Secret source before ordinary Git operations. The remote helper intentionally never prompts.

`suspected rollback`
: Preserve `.git/cloak/state` and run `doctor --json`. Cloak fails closed when authenticated state contradicts the trusted checkpoint or required pre-Compaction Storage commits are unavailable.

Initialization rejects the Repository Host
: Confirm the host repository has no refs and that authentication permits creating and force-updating `refs/heads/cloak-storage`.

Push rejects Git LFS or partial clone state
: Store ordinary blobs directly in Git and use a full, non-promisor repository. There is no bypass flag.
