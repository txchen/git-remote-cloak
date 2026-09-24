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

Follow the [one-command installation steps](../README.md#install). Keep the binary on `PATH`; Git locates it by the required name `git-remote-cloak` when a `cloak::` remote is used.

Verify the exact binary before configuring a repository:

```sh
git-remote-cloak version --json
git-remote-cloak version --formats
```

For release `v0.3.0`, expect Linux amd64, CGo disabled, and exact read/write support for format v1.0.

### Manual installation

The installer is optional. To install Linux x86-64 manually, download into a fresh
directory, verify the archive, and put the executable on `PATH`:

```sh
version=v0.3.0
curl -fLO "https://github.com/txchen/git-remote-cloak/releases/download/${version}/checksums.txt"
curl -fLO "https://github.com/txchen/git-remote-cloak/releases/download/${version}/git-remote-cloak_${version}_linux_amd64.tar.gz"
sha256sum --check --ignore-missing checksums.txt
tar -xzf "git-remote-cloak_${version}_linux_amd64.tar.gz"
mkdir -p "$HOME/.local/bin"
install -m 755 git-remote-cloak "$HOME/.local/bin/git-remote-cloak"
export PATH="$HOME/.local/bin:$PATH"
git-remote-cloak version
```

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
git push -u backup main
```

Init automatically saves the generated or supplied Recovery Secret in `.git/cloak/secret` (file `0600`, directory `0700`) before publishing. Git does not track this metadata file. Keep a separate Recovery Mnemonic backup outside this machine: losing the repository also loses its local Secret.

Each repository uses its own local Secret. Linked worktrees use the Secret in their common Git directory. Moving a normal repository with its `.git` directory preserves the Secret. Keep it out of tracked files, Git configuration, command arguments, logs, and shell history.

For automation, supply exactly one explicit source: `CLOAK_RECOVERY_SECRET`, `CLOAK_RECOVERY_SECRET_FILE`, or `--secret-file` for init/clone. These override the local Secret; multiple explicit sources are an error. Daily commands do not replace the saved Secret when using an override. Init refuses to overwrite a different saved Secret. Remove old global Secret exports from your shell configuration to enable automatic repository selection.

For an existing v0.1.x checkout, run `git-remote-cloak init backup URL --secret-file PATH` once using its existing remote name, URL and Secret (unset other Secret sources first). This saves the Secret locally. A non-interactive first init without a supplied Secret still fails.

## Verify host privacy

Query the underlying Repository Host URL, not the `cloak::` remote:

```sh
git ls-remote https://github.com/OWNER/REPOSITORY.git
```

Only `refs/heads/cloak-storage` and the host's matching `HEAD` should be visible. Original branch names such as `main` must not appear.

## Daily Git operations

Keep the binary on `PATH`. Cloak automatically finds the current repository’s Secret, including from subdirectories:

```sh
export PATH="$HOME/.local/bin:$PATH"

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

## Diagnose slow or failed operations

Set `CLOAK_LOG=debug` for one command to print elapsed stage timings and aggregate counts to stderr:

```sh
CLOAK_LOG=debug git push backup main
CLOAK_LOG=debug git fetch backup
```

For a push that pauses before `Packing`, inspect the `remote inspection`, `storage clone`, `storage blob prefetch`, and `snapshot decode` lines. A push with new commits also reports restoration, candidate validation, and Storage Ref publication. A no-change push ends after remote inspection. Debug output does not include the Recovery Secret, original file paths, commit messages, Logical Ref names, or Git command arguments. It does reveal operation timing and aggregate object counts. Ordinary Git or Repository Host errors may include additional details, so review captured stderr before sharing it.

## Recover on another host

Install the same or a format-compatible binary, configure Git authentication, and run:

```sh
git-remote-cloak clone \
  https://github.com/OWNER/REPOSITORY.git \
  recovered

git -C recovered fsck --full
```

Enter the complete saved Recovery Mnemonic at the hidden terminal prompt. The validated clone stores it in its own `.git/cloak/secret`. Clone never borrows a Secret from the directory you started in. For unattended recovery, add `--secret-file PATH` or configure one environment source. Direct `git clone cloak::URL` never prompts and requires an environment source for the initial clone; it also saves the Secret locally.

A fresh clone authenticates the returned Ciphertext Snapshot but cannot independently prove that the Repository Host returned the newest valid snapshot. After the first observation, the local Rollback Checkpoint protects future observations.

From the recovered repository, diagnose its remote (an unrelated URL requires an explicit Secret source):

```sh
cd recovered
git-remote-cloak doctor https://github.com/OWNER/REPOSITORY.git
git-remote-cloak doctor https://github.com/OWNER/REPOSITORY.git --json
```

## Maintenance

Automatic Compaction is enabled by default. An ordinary push compacts before it
would create a thirty-third live Pack Payload. It also compacts when the new
snapshot has at least eight live Pack Payloads and ciphertext added since the
last Compaction reaches both 1 MiB and 50% of the previous compacted snapshot
size. Compaction runs during the push, rebuilds the reachable Logical Repository
as one encrypted pack, and replaces the visible Storage History with a new root
commit. It preserves the original branches, commits, and Recovery Secret. A
repository with legacy Compaction metadata may compact once to establish a new
baseline.

Compact live ciphertext while preserving the Recovery Secret and Logical Repository:

```sh
git-remote-cloak compact backup
```

To schedule Compaction yourself, disable the automatic trigger for that local
remote and run `compact` when convenient. Pushes that exceed a threshold report
a capacity warning while automatic Compaction is disabled:

```sh
git config remote.backup.cloakAutoCompact false
git-remote-cloak compact backup
```

Rekey replaces the Ciphertext Repository from the complete selected local refs with a new Recovery Secret and Repository ID:

```sh
unset CLOAK_RECOVERY_SECRET CLOAK_RECOVERY_SECRET_FILE
git-remote-cloak rekey backup
```

Read the displayed ref plan before confirming. Interactive Rekey displays a new Recovery Mnemonic once; save its complete `cloak-v1:` value before confirming it. To perform unattended Rekey, configure a newly generated Secret source rather than the current repository Secret. Successful Rekey automatically replaces the locally saved Secret. Before publication, Cloak saves the candidate in a separate protected `.git/cloak/secret.pending` file. If a process exits after publication, the next automatic Secret lookup authenticates the published candidate and completes the local update. If publication did not happen, the active Secret stays unchanged; retrying Rekey reuses the pending candidate. Keep the pending file until the operation resolves. An unexplained remote history still fails closed. Repository Host retention may preserve superseded ciphertext after Compaction or Rekey.

Clear only reconstructable local ciphertext cache; this preserves the Secret and Rollback Checkpoint:

```sh
git-remote-cloak cache clear
```

## Upgrade

Re-run the installer to download, verify, and atomically install the latest release:

```sh
curl -fsSL https://github.com/txchen/git-remote-cloak/releases/latest/download/install.sh | bash
git-remote-cloak version
```

For a manual upgrade, download and verify the new release, then replace the executable atomically:

```sh
install -m 755 git-remote-cloak "$HOME/.local/bin/git-remote-cloak.new"
mv "$HOME/.local/bin/git-remote-cloak.new" "$HOME/.local/bin/git-remote-cloak"
git-remote-cloak version
```

An ordinary binary upgrade never rewrites repository format. Format Migration is explicit.

## Common failures

`multiple Recovery Secret sources configured`
: Keep exactly one of `CLOAK_RECOVERY_SECRET`, `CLOAK_RECOVERY_SECRET_FILE`, or command-specific `--secret-file`.

`no Recovery Secret configured` / `non-interactive init requires a configured Recovery Secret`
: Initialize or recover the repository once, or supply one explicit Secret source for automation. The remote helper intentionally never prompts.

Managed Recovery Secret file is damaged or has unsafe permissions
: Preserve the file and restore it from the offline Recovery Mnemonic backup, or correct its permissions to `0600`. Cloak never silently generates a replacement for a damaged saved Secret.

`suspected rollback`
: Preserve `.git/cloak/state` and run `doctor --json`. Cloak fails closed when authenticated state contradicts the trusted checkpoint or required pre-Compaction Storage commits are unavailable.

Initialization rejects the Repository Host
: Confirm the host repository has no refs and that authentication permits creating and force-updating `refs/heads/cloak-storage`.

Host rejects the Storage commit email
: Cloak uses the invoking Git repository's configured `user.name` and `user.email` for Storage History commits. When no Git identity is configured, it uses `git-remote-cloak <cloak@invalid>`. If the host requires a work email, set it with ordinary Git configuration, for example `git config user.email you@your-company.example` in the Plaintext Workspace, then retry `init`. Git identity environment variables also work. The Repository Host can see this outer commit identity; original commit identities remain encrypted and unchanged.

Push rejects Git LFS or partial clone state
: Store ordinary blobs directly in Git and use a full, non-promisor repository. There is no bypass flag.
