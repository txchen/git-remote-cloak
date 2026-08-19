# git-remote-cloak

`git-remote-cloak` stores a private Git backup on an ordinary Repository Host without exposing original files, paths, commit messages, or branch names. The owner works in a normal Git repository; the host sees one `cloak-storage` branch containing opaque ciphertext.

Binary version `v0.1.0` writes Ciphertext Repository format `v1.0`. These versions are independent. The current operationally verified target is Linux amd64.

## Install on Linux

Download `checksums.txt` and the archive matching your machine from the [latest release](https://github.com/txchen/git-remote-cloak/releases/latest). For Linux x86-64:

```sh
version=v0.1.0
curl -fLO "https://github.com/txchen/git-remote-cloak/releases/download/${version}/checksums.txt"
curl -fLO "https://github.com/txchen/git-remote-cloak/releases/download/${version}/git-remote-cloak_${version}_linux_amd64.tar.gz"
sha256sum --check --ignore-missing checksums.txt
tar -xzf "git-remote-cloak_${version}_linux_amd64.tar.gz"
install -m 700 -d "$HOME/.local/bin"
install -m 755 git-remote-cloak "$HOME/.local/bin/git-remote-cloak"
export PATH="$HOME/.local/bin:$PATH"
git-remote-cloak version
```

Persist `$HOME/.local/bin` in the shell's `PATH`. The version output must report `v0.1.0`, `linux/amd64`, `cgo: disabled`, and `v1.0 read=yes write=yes`.

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
