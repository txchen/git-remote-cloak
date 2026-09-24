# git-remote-cloak

`git-remote-cloak` stores a private Git backup on an ordinary Repository Host without exposing original files, paths, commit messages, or branch names. The owner works in a normal Git repository; the host sees one `cloak-storage` branch containing opaque ciphertext.

Binary version `v0.2.2` writes Ciphertext Repository format `v1.0`. These versions are independent. The current operationally verified target is Linux amd64.

## Install

On Linux or macOS (x86-64 or ARM64), with Git, Bash, curl, and tar available:

```sh
curl -fsSL https://github.com/txchen/git-remote-cloak/releases/latest/download/install.sh | bash
```

The installer selects the latest release for your machine, verifies its SHA-256
checksum, and installs it to `$HOME/.local/bin` without sudo. Running it again
upgrades the binary atomically. A failed download or checksum check preserves the
existing installation. Linux amd64 remains the operationally verified target;
other published platforms have the support limits described in [the release contract](docs/release.md).

If that directory is not already on your `PATH`, add this to your shell
configuration (`~/.bashrc` or `~/.zshrc`) and run it in the current terminal:

```sh
export PATH="$HOME/.local/bin:$PATH"
git-remote-cloak version
```

Choose a version or an existing writable installation directory when needed:

```sh
curl -fsSL https://github.com/txchen/git-remote-cloak/releases/latest/download/install.sh \
  | CLOAK_VERSION=v0.2.2 CLOAK_INSTALL_DIR="$HOME/.local/bin" bash
```

To inspect the script first, download it to a file and run `bash install.sh` after
reviewing it. Archives and `checksums.txt` remain available for [manual installation](docs/linux.md#manual-installation).

## Quick start

Create a completely empty private repository on the Repository Host. Configure ordinary Git authentication first; Cloak uses the same SSH, HTTPS, and credential-helper behavior as Git.

Inside the existing Git repository to protect:

```sh
git-remote-cloak init backup https://github.com/OWNER/EMPTY-PRIVATE-REPOSITORY.git
```

Cloak displays a Recovery Mnemonic once. Back up the complete `cloak-v1:` value and all 24 words outside this machine, then type `SAVED`. Cloak automatically saves the local working copy in `.git/cloak/secret` with permissions `0600`. Each repository has its own Secret; changing directories automatically selects the right one. Linked worktrees share their common Git directory's Secret.

Push through the configured Cloak remote using ordinary Git:

```sh
git push -u backup master
```

Replace `master` with the current local branch name when necessary. The branch must contain at least one commit before it can be pushed.

To investigate a slow or failed push, enable stage timings and aggregate counts for one command:

```sh
CLOAK_LOG=debug git push backup master
```

Diagnostic lines go to stderr. They omit the Recovery Secret, original paths, commit messages, and Git command arguments. See the [operations guide](docs/linux.md#diagnose-slow-or-failed-operations).

Recover on another authorized Linux host:

```sh
git-remote-cloak clone https://github.com/OWNER/EMPTY-PRIVATE-REPOSITORY.git
```

At the hidden prompt, enter the **complete** saved Recovery Mnemonic: the literal `cloak-v1:` prefix followed by all 24 space-separated words. The words alone are rejected. Without a directory argument, Cloak creates `EMPTY-PRIVATE-REPOSITORY` in the current directory. Clone saves the Recovery Secret locally; subsequent `git push`, `git fetch`, and `git pull` need no environment variables.

For automation, supply one of `CLOAK_RECOVERY_SECRET`, `CLOAK_RECOVERY_SECRET_FILE`, or `--secret-file` (init/clone). An explicit source overrides the local Secret; multiple explicit sources are rejected. Init and clone also save supplied Secrets locally. Keep the offline backup: deleting the local repository deletes its local Secret.

## Read next

- [Linux operation and recovery guide](docs/linux.md)
- [Release scope, security limits, and cryptography](docs/release.md)
- [Repository Host certification](docs/provider-certification.md)

Git LFS and partial clones are rejected. Ordinary Git binary blobs are supported. Use an independent Ciphertext Repository and Recovery Secret for each private submodule.
