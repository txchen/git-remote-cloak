#!/usr/bin/env bash
set -euo pipefail

# Keep the body in a function: when piped into bash, no installation starts
# until the entire function has been received and parsed.
main() {
  if [[ $# -ne 0 ]]; then
    echo 'usage: [CLOAK_VERSION=vX.Y.Z] [CLOAK_INSTALL_DIR=DIR] bash install.sh' >&2
    return 2
  fi

  local command_name platform architecture
  for command_name in curl tar mktemp install git; do
    if ! command -v "${command_name}" >/dev/null 2>&1; then
      echo "Required command not found: ${command_name}" >&2
      return 1
    fi
  done
  case "$(uname -s)" in
    Linux) platform=linux ;;
    Darwin) platform=darwin ;;
    *) echo 'Supported systems: Linux and macOS.' >&2; return 1 ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64) architecture=amd64 ;;
    arm64|aarch64) architecture=arm64 ;;
    *) echo 'Supported architectures: x86-64 and ARM64.' >&2; return 1 ;;
  esac

  local version="${CLOAK_VERSION:-}" install_directory="${CLOAK_INSTALL_DIR:-${HOME}/.local/bin}"
  local version_pattern='v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?'
  if [[ -n "${version}" && ! "${version}" =~ ^${version_pattern}$ ]]; then
    echo 'CLOAK_VERSION must be a release tag such as v0.1.1.' >&2
    return 2
  fi
  if [[ "${install_directory}" != /* ]]; then
    echo 'CLOAK_INSTALL_DIR must be an absolute path.' >&2
    return 2
  fi
  local -a checksum_command
  if command -v sha256sum >/dev/null 2>&1; then
    checksum_command=(sha256sum)
  elif command -v shasum >/dev/null 2>&1; then
    checksum_command=(shasum -a 256)
  else
    echo 'SHA-256 verification requires sha256sum or shasum.' >&2
    return 1
  fi

  umask 077
  cloak_install_staged_binary=''
  cloak_install_temporary_directory="$(mktemp -d)"
  # These two cleanup paths remain available after main returns on failure.
  trap 'rm -rf -- "${cloak_install_temporary_directory}"; if [[ -n "${cloak_install_staged_binary}" ]]; then rm -f -- "${cloak_install_staged_binary}"; fi' EXIT

  local release_base='https://github.com/txchen/git-remote-cloak/releases'
  local checksum_url="${release_base}/latest/download/checksums.txt"
  if [[ -n "${version}" ]]; then
    checksum_url="${release_base}/download/${version}/checksums.txt"
  fi
  local -a download=(curl --proto '=https' --tlsv1.2 --fail --silent --show-error --location --retry 3)
  "${download[@]}" "${checksum_url}" -o "${cloak_install_temporary_directory}/checksums.txt"

  local digest filename extra archive='' expected_digest='' matched_version=''
  local archive_pattern="^git-remote-cloak_(${version_pattern})_${platform}_${architecture}\.tar\.gz$"
  while read -r digest filename extra; do
    if [[ "${filename}" =~ ${archive_pattern} ]]; then
      matched_version="${BASH_REMATCH[1]}"
      if [[ -n "${archive}" || ! "${digest}" =~ ^[0-9a-fA-F]{64}$ || -n "${extra}" ]]; then
        echo 'Invalid or ambiguous release checksum entry.' >&2
        return 1
      fi
      if [[ -n "${version}" && "${version}" != "${matched_version}" ]]; then
        echo 'Release checksum version does not match CLOAK_VERSION.' >&2
        return 1
      fi
      archive="${filename}"
      expected_digest="${digest}"
    fi
  done <"${cloak_install_temporary_directory}/checksums.txt"
  if [[ -z "${archive}" ]]; then
    echo "Release has no checksummed archive for ${platform}/${architecture}." >&2
    return 1
  fi
  version="${matched_version}"
  echo "Downloading git-remote-cloak ${version} for ${platform}/${architecture}..."
  # Pin the archive to the version in the manifest, even if latest changes now.
  "${download[@]}" "${release_base}/download/${version}/${archive}" -o "${cloak_install_temporary_directory}/${archive}"
  local actual_digest
  actual_digest="$("${checksum_command[@]}" "${cloak_install_temporary_directory}/${archive}")"
  actual_digest="${actual_digest%% *}"
  if [[ "${actual_digest}" != "${expected_digest}" ]]; then
    echo 'SHA-256 mismatch; installation cancelled.' >&2
    return 1
  fi

  tar -xzf "${cloak_install_temporary_directory}/${archive}" -C "${cloak_install_temporary_directory}" git-remote-cloak
  local extracted_binary="${cloak_install_temporary_directory}/git-remote-cloak"
  if [[ ! -f "${extracted_binary}" || -L "${extracted_binary}" ]]; then
    echo 'Release archive does not contain a regular binary.' >&2
    return 1
  fi
  chmod 755 "${extracted_binary}"
  "${extracted_binary}" version

  mkdir -p "${install_directory}"
  if [[ -d "${install_directory}/git-remote-cloak" ]]; then
    echo 'Installation destination is a directory.' >&2
    return 1
  fi
  cloak_install_staged_binary="$(mktemp "${install_directory}/.git-remote-cloak.XXXXXX")"
  install -m 755 "${extracted_binary}" "${cloak_install_staged_binary}"
  mv -f "${cloak_install_staged_binary}" "${install_directory}/git-remote-cloak"
  cloak_install_staged_binary=''
  echo "Installed ${install_directory}/git-remote-cloak"
  case ":${PATH}:" in
    *":${install_directory}:"*) ;;
    *)
      echo 'Add this to your shell configuration, then run it in this terminal:'
      printf 'export PATH=%q:"$PATH"\n' "${install_directory}"
      ;;
  esac
  rm -rf -- "${cloak_install_temporary_directory}"
  trap - EXIT
}

main "$@"
