#!/usr/bin/env bash
set -euo pipefail

readonly required_go_version="go1.26.5"
readonly repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ $# -lt 1 || $# -gt 2 || ! "$1" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  echo "usage: scripts/build-release.sh <vMAJOR.MINOR.PATCH> [output-directory]" >&2
  exit 2
fi

readonly release_version="$1"
readonly output_directory="${2:-${repository_root}/dist}"
readonly actual_go_version="$(go env GOVERSION)"
if [[ "${actual_go_version}" != "${required_go_version}" ]]; then
  echo "release requires ${required_go_version}; found ${actual_go_version}" >&2
  exit 1
fi

readonly source_epoch="${SOURCE_DATE_EPOCH:-$(git -C "${repository_root}" show -s --format=%ct HEAD)}"
if [[ ! "${source_epoch}" =~ ^[0-9]+$ ]]; then
  echo "SOURCE_DATE_EPOCH must be an integer Unix timestamp" >&2
  exit 2
fi
if build_date="$(date -u -d "@${source_epoch}" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null)"; then
  readonly build_date
else
  readonly build_date="$(date -u -r "${source_epoch}" +%Y-%m-%dT%H:%M:%SZ)"
fi
readonly build_commit="$(git -C "${repository_root}" rev-parse HEAD)"
readonly temporary_directory="$(mktemp -d)"
trap 'rm -rf -- "${temporary_directory}"' EXIT

mkdir -p "${output_directory}"
find "${output_directory}" -maxdepth 1 -type f \( -name 'git-remote-cloak_*.tar.gz' -o -name 'checksums.txt' \) -delete

readonly linker_flags="-s -w -X main.buildVersion=${release_version} -X main.buildCommit=${build_commit} -X main.buildDate=${build_date} -X main.buildCGo=disabled"
for target in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64; do
  IFS=/ read -r target_os target_arch <<<"${target}"
  stage="${temporary_directory}/${target_os}-${target_arch}"
  mkdir -p "${stage}"
  (
    cd "${repository_root}"
    CGO_ENABLED=0 GOOS="${target_os}" GOARCH="${target_arch}" \
      go build -mod=readonly -trimpath -buildvcs=false -ldflags "${linker_flags}" \
      -o "${stage}/git-remote-cloak" ./cmd/git-remote-cloak
  )
  archive="git-remote-cloak_${release_version}_${target_os}_${target_arch}.tar.gz"
  go run -mod=readonly "${repository_root}/scripts/release-archive.go" \
    "${source_epoch}" "${stage}/git-remote-cloak" "${output_directory}/${archive}"
done

(
  cd "${output_directory}"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum git-remote-cloak_*.tar.gz >checksums.txt
  else
    shasum -a 256 git-remote-cloak_*.tar.gz >checksums.txt
  fi
)
