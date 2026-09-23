# git-remote-cloak

`git-remote-cloak` stores a private Git backup on an ordinary Repository Host without exposing original files, paths, commit messages, or branch names. The owner works in a normal Git repository; the host sees one `cloak-storage` branch containing opaque ciphertext.

Binary version `v0.1.1` writes Ciphertext Repository format `v1.0`. These versions are independent. The current operationally verified target is Linux amd64.

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
  | CLOAK_VERSION=v0.1.1 CLOAK_INSTALL_DIR="$HOME/.local/bin" bash
```

To inspect the script first, download it to a file and run `bash install.sh` after
reviewing it. Archives and `checksums.txt` remain available for [manual installation](docs/linux.md#manual-installation).

## Quick start

Create a completely empty private repository on the Repository Host. Configure ordinary Git authentication first; Cloak uses the same SSH, HTTPS, and credential-helper behavior as Git.

Inside the existing Git repository to protect:

```sh
git-remote-cloak init backup https://github.com/OWNER/EMPTY-PRIVATE-REPOSITORY.git
```

Cloak displays a Recovery Mnemonic once. Save the complete `cloak-v1:` value and all 24 words outside the Git repository, then type `SAVED`. Store it in a mode-0600 file without placing it in shell history:

```sh
secret_file="${XDG_CONFIG_HOME:-$HOME/.config}/git-remote-cloak/repository.recovery"
install -m 700 -d "$(dirname "$secret_file")"
if test -e "$secret_file"; then
  echo "using existing Recovery Secret file: $secret_file"
else
  install -m 600 /dev/null "$secret_file"
  read -r -s -p "Recovery Mnemonic: " recovery_mnemonic
  printf '\n'
  printf '%s\n' "$recovery_mnemonic" >"$secret_file"
  unset recovery_mnemonic
fi
export CLOAK_RECOVERY_SECRET_FILE="$secret_file"
```

Push through the configured Cloak remote using ordinary Git:

```sh
git push -u backup main
```

Replace `main` with the current local branch name when necessary. The branch must contain at least one commit before it can be pushed.

Recover on another authorized Linux host:

```sh
git-remote-cloak clone \
  https://github.com/OWNER/EMPTY-PRIVATE-REPOSITORY.git \
  recovered \
  --secret-file "$secret_file"
```

Do not configure both `CLOAK_RECOVERY_SECRET_FILE` and `--secret-file` for the same command. Cloak rejects ambiguous Recovery Secret sources.

## Read next

- [Linux operation and recovery guide](docs/linux.md)
- [Release scope, security limits, and cryptography](docs/release.md)
- [Repository Host certification](docs/provider-certification.md)

Git LFS and partial clones are rejected. Ordinary Git binary blobs are supported. Use an independent Ciphertext Repository and Recovery Secret for each private submodule.
