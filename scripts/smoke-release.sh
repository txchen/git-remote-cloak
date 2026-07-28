#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 || ! -x "$1" ]]; then
  echo "usage: scripts/smoke-release.sh <git-remote-cloak-binary>" >&2
  exit 2
fi

readonly cloak_binary="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"
readonly smoke_root="$(mktemp -d)"
trap 'rm -rf -- "${smoke_root}"' EXIT
readonly recovery_mnemonic='cloak-v1:abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon art'
export CLOAK_RECOVERY_SECRET="${recovery_mnemonic}"
export GIT_CONFIG_NOSYSTEM=1
export PATH="$(dirname "${cloak_binary}"):${PATH}"

git init --bare "${smoke_root}/host.git" >/dev/null
git init -b main "${smoke_root}/owner" >/dev/null
git -C "${smoke_root}/owner" config user.name 'Cloak Release Smoke Test'
git -C "${smoke_root}/owner" config user.email 'cloak@example.invalid'
printf '%s\n' 'release smoke plaintext' >"${smoke_root}/owner/release-smoke.txt"
git -C "${smoke_root}/owner" add release-smoke.txt
git -C "${smoke_root}/owner" commit -m 'release smoke commit' >/dev/null
"${cloak_binary}" version >/dev/null
(
  cd "${smoke_root}/owner"
  "${cloak_binary}" init backup "${smoke_root}/host.git"
  git push backup main
) >/dev/null
"${cloak_binary}" clone "${smoke_root}/host.git" "${smoke_root}/recovered" >/dev/null
cmp "${smoke_root}/owner/release-smoke.txt" "${smoke_root}/recovered/release-smoke.txt"
