#!/usr/bin/env bash
# OpenBrain: install or upgrade the four service binaries from a GitHub
# Release (Phase 2, OB-062, plan-1-release-binary-deploy).
#
# Scope (locked decisions, see plans/plan-1-release-binary-deploy):
#   - bigmon-only. No SSH, no cross-host machinery.
#   - Self-contained, per-repo script. No shared installer component.
#   - Manages exactly four binaries: openbrain-web, openbrain-telegram,
#     openbrain-slack, openbrain-watchd. openbrain and openbrain-mcp are
#     dev-only and are never downloaded or installed by this script.
#   - Installs to /usr/local/bin: the FHS location for software installed by
#     the local administrator outside the distribution package manager.
#   - Never reads, moves, or writes the repo .env or any secret, and never
#     touches the systemd --user unit files (unit repoint is Phase 3).
#
# Usage:
#   scripts/install-release.sh [VERSION]
#
#   VERSION   Optional released tag, for example v0.7.1. With no argument,
#             the latest GitHub Release is resolved and installed.
#
# Sequence: resolve version, download the four service-binary assets plus
# SHA256SUMS into a scratch directory, verify every checksum, verify every
# binary reports the expected version via --version, then install atomically
# (temp file on the same filesystem as the install directory, then rename).
# Every verification step runs BEFORE any file in the install directory is
# touched: a checksum or version mismatch aborts with nothing changed on disk.
#
# Privilege model: sudo is used only for the write into the install
# directory (creating the temp file, copying into it, and the final rename).
# Resolving the version, downloading, and both verification passes run
# unprivileged. When the install directory is already writable by the
# invoking user (for example a test fixture directory), sudo is skipped
# entirely.
#
# Environment overrides (for testing; production runs with defaults):
#   OPENBRAIN_REPO         GitHub repo, default windingriverholdings/openbrain
#   OPENBRAIN_INSTALL_DIR  Install target, default /usr/local/bin
#   OPENBRAIN_PLATFORM     Asset platform suffix, default linux-amd64
#
# This file is written so its verification and install functions can be
# sourced and unit tested directly; see tests/install-release.bats. main()
# only runs when the script is executed, not when it is sourced.

set -euo pipefail

OPENBRAIN_REPO="${OPENBRAIN_REPO:-windingriverholdings/openbrain}"
OPENBRAIN_INSTALL_DIR="${OPENBRAIN_INSTALL_DIR:-/usr/local/bin}"
OPENBRAIN_PLATFORM="${OPENBRAIN_PLATFORM:-linux-amd64}"
SERVICE_BINARIES=(openbrain-web openbrain-telegram openbrain-slack openbrain-watchd)
SUMS_FILE_NAME="SHA256SUMS"

log_info() {
  printf '[install-release] %s\n' "$*"
}

# log_error prints a message tagged with the failing stage, so download,
# checksum, version, install, and prereq failures are distinguishable in the
# output rather than reading as one undifferentiated error.
log_error() {
  local stage="$1"
  shift
  printf '[install-release] ERROR (%s): %s\n' "$stage" "$*" >&2
}

# asset_filename builds the exact GitHub release asset name for a binary at a
# given version, matching the Makefile dist target's naming convention:
# <binary>-<version>-<platform>, for example openbrain-web-v0.7.1-linux-amd64.
asset_filename() {
  local binary="$1" version="$2"
  printf '%s-%s-%s\n' "$binary" "$version" "$OPENBRAIN_PLATFORM"
}

have_gh_auth() {
  command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1
}

# check_prereqs refuses to run with a clear, non-zero-exit error when there is
# no viable download path or no viable write path, before any network call.
check_prereqs() {
  local install_dir="$1"

  if ! have_gh_auth && ! command -v curl >/dev/null 2>&1; then
    log_error prereq "gh is not installed or not authenticated, and curl is not installed; no download path is available"
    return 1
  fi

  if [[ ! -d "$install_dir" ]]; then
    log_error prereq "install directory ${install_dir} does not exist"
    return 1
  fi

  if [[ ! -w "$install_dir" ]] && ! command -v sudo >/dev/null 2>&1; then
    log_error prereq "no write permission to ${install_dir}, and sudo is not available to elevate"
    return 1
  fi

  return 0
}

# resolve_version echoes the version to install: the requested tag if given
# (after confirming it exists, when gh is available to check), or the latest
# release tag otherwise. A requested tag that does not exist is a prereq
# failure, reported clearly rather than surfacing later as a download error.
resolve_version() {
  local repo="$1" requested="$2"

  if [[ -n "$requested" ]]; then
    if have_gh_auth; then
      if ! gh release view "$requested" --repo "$repo" >/dev/null 2>&1; then
        log_error prereq "requested tag ${requested} was not found in ${repo}"
        return 1
      fi
    fi
    printf '%s\n' "$requested"
    return 0
  fi

  if have_gh_auth; then
    gh release view --repo "$repo" --json tagName -q .tagName
    return $?
  fi

  if command -v curl >/dev/null 2>&1 && command -v jq >/dev/null 2>&1; then
    curl -fsSL "https://api.github.com/repos/${repo}/releases/latest" | jq -r '.tag_name'
    return $?
  fi

  log_error prereq "cannot resolve the latest release: gh is unavailable and the curl/jq fallback is unavailable"
  return 1
}

# download_via_gh fetches exactly the requested assets via gh release
# download, using one --pattern per asset so no other release asset
# (including openbrain and openbrain-mcp) is ever downloaded.
download_via_gh() {
  local repo="$1" version="$2" scratch_dir="$3"
  shift 3
  local -a assets=("$@")
  local -a patterns=()
  local asset
  for asset in "${assets[@]}"; do
    patterns+=(-p "$asset")
  done
  gh release download "$version" --repo "$repo" --dir "$scratch_dir" --clobber "${patterns[@]}"
}

# download_via_curl is the documented fallback when gh is unavailable or
# unauthenticated. It first tries the public, unauthenticated release
# download URL. If that 404s and a token is present (GH_TOKEN or
# GITHUB_TOKEN), it falls back further to the GitHub API asset endpoint,
# which is the documented path for a private-repo release asset: resolve the
# asset's numeric id from the release, then GET it with an
# application/octet-stream Accept header and the token.
download_via_curl() {
  local repo="$1" version="$2" scratch_dir="$3"
  shift 3
  local -a assets=("$@")
  local token="${GH_TOKEN:-${GITHUB_TOKEN:-}}"
  local asset url

  # When a token is present, write the Authorization header to a 0600 temp
  # file and pass it to curl as `-H @file` rather than `-H "Authorization:
  # token ..."` on the command line: argv (and therefore the token) is
  # visible to other local users via `ps`, a bearer token must not be. The
  # RETURN trap cleans the header file up on every exit path of this
  # function, not just the happy one.
  #
  # A RETURN trap is not scoped to the function that set it: once armed it
  # stays active and fires again on every later function return, including
  # the CALLER's. Since the trap body references header_file by name, that
  # later firing (with header_file already out of the local scope that
  # declared it) aborts under set -u with "header_file: unbound variable"
  # (OB-084). The `trap - RETURN` appended to the trap body disarms it
  # immediately after this function's own return fires it once, so it never
  # fires again for a caller's return.
  local header_file=""
  if [[ -n "$token" ]]; then
    header_file="$(mktemp)"
    chmod 600 "$header_file"
    printf 'Authorization: token %s\n' "$token" > "$header_file"
  fi
  trap '[[ -n "$header_file" ]] && rm -f "$header_file"; trap - RETURN' RETURN

  for asset in "${assets[@]}"; do
    url="https://github.com/${repo}/releases/download/${version}/${asset}"
    if curl -fsSL -o "${scratch_dir}/${asset}" "$url"; then
      continue
    fi

    if [[ -n "$token" ]] && command -v jq >/dev/null 2>&1; then
      local asset_id
      asset_id="$(curl -fsSL \
        -H "@${header_file}" \
        -H "Accept: application/vnd.github+json" \
        "https://api.github.com/repos/${repo}/releases/tags/${version}" \
        | jq -r --arg name "$asset" '.assets[] | select(.name == $name) | .id' 2>/dev/null || true)"
      if [[ -n "$asset_id" && "$asset_id" != "null" ]]; then
        if curl -fsSL \
          -H "@${header_file}" \
          -H "Accept: application/octet-stream" \
          -o "${scratch_dir}/${asset}" \
          "https://api.github.com/repos/${repo}/releases/assets/${asset_id}"; then
          continue
        fi
      fi
    fi

    log_error download "curl fallback failed for ${asset}"
    return 1
  done

  return 0
}

# download_release prefers gh (handles auth and private repos transparently)
# and falls back to curl only when gh is missing, unauthenticated, or itself
# fails. Distinct from checksum and install failures: a download failure is
# reported at this stage, before any verification is attempted.
download_release() {
  local repo="$1" version="$2" scratch_dir="$3"
  shift 3
  local -a assets=("$@")

  if have_gh_auth; then
    if download_via_gh "$repo" "$version" "$scratch_dir" "${assets[@]}"; then
      return 0
    fi
    log_info "gh release download failed, falling back to curl"
  else
    log_info "gh unavailable or unauthenticated, using curl fallback"
  fi

  if ! command -v curl >/dev/null 2>&1; then
    log_error download "gh unavailable and curl is not installed; no download path available"
    return 1
  fi

  download_via_curl "$repo" "$version" "$scratch_dir" "${assets[@]}"
}

# expected_checksum prints the SHA256 recorded for filename in sums_path, or
# returns non-zero when no matching entry exists. Matching is done by exact
# field comparison, not a regex match against the filename, so filenames
# containing regex metacharacters (the version dots) cannot produce a false
# match.
expected_checksum() {
  local sums_path="$1" filename="$2"
  awk -v f="$filename" '
    {
      name = $2
      sub(/^\*/, "", name)
      if (name == f) { print $1; found = 1 }
    }
    END { exit !found }
  ' "$sums_path"
}

# verify_checksums is the fail-closed gate: every listed asset must be
# present in scratch_dir, have a matching entry in the SHA256SUMS file, and
# its recomputed SHA256 must equal that entry exactly. Any single failure
# aborts and names the offending asset; nothing downstream (install) runs.
verify_checksums() {
  local scratch_dir="$1" sums_file_name="$2"
  shift 2
  local -a assets=("$@")
  local sums_path="${scratch_dir}/${sums_file_name}"

  if [[ ! -f "$sums_path" ]]; then
    log_error checksum "missing ${sums_file_name} in the download"
    return 1
  fi

  local asset asset_path expected actual
  for asset in "${assets[@]}"; do
    asset_path="${scratch_dir}/${asset}"
    if [[ ! -f "$asset_path" ]]; then
      log_error checksum "missing downloaded asset ${asset}"
      return 1
    fi

    if ! expected="$(expected_checksum "$sums_path" "$asset")"; then
      log_error checksum "no checksum entry for ${asset} in ${sums_file_name}"
      return 1
    fi

    # Guarded explicitly (not left to the surrounding `set -e`): this
    # function is invoked as `verify_checksums ... || exit 1` in main, and
    # bash suspends errexit for the entire duration of a function called as
    # the left operand of `||`. An unguarded sha256sum failure here would
    # otherwise leave $actual empty and fall through to the mismatch branch
    # below, misreporting a read failure as a checksum mismatch.
    if ! actual="$(sha256sum "$asset_path" | awk '{print $1}')" || [[ -z "$actual" ]]; then
      log_error checksum "failed to compute a checksum for ${asset}; unable to verify"
      return 1
    fi

    if [[ "$actual" != "$expected" ]]; then
      log_error checksum "checksum mismatch for ${asset}: expected ${expected}, got ${actual}"
      return 1
    fi
  done

  return 0
}

# verify_versions runs each downloaded binary with --version (the Phase 1
# self-identifying-binary contract) and confirms the reported version equals
# the requested or resolved version exactly. A mismatch aborts before
# install, distinctly from a checksum failure.
verify_versions() {
  local scratch_dir="$1" expected_version="$2"
  shift 2
  local -a binaries=("$@")
  local binary asset asset_path got

  for binary in "${binaries[@]}"; do
    asset="$(asset_filename "$binary" "$expected_version")"
    asset_path="${scratch_dir}/${asset}"

    # Guarded explicitly: this function runs under `verify_versions ... ||
    # exit 1` in main, which suspends errexit for its whole body (see the
    # matching comment in verify_checksums). An unguarded chmod failure here
    # would otherwise fall through to the --version invocation below, which
    # fails for a different reason (permission denied) and reports a
    # confusing "failed to execute" rather than naming the real cause.
    if ! chmod +x "$asset_path"; then
      log_error version "failed to set the exec bit on ${binary} before running --version"
      return 1
    fi

    if ! got="$("$asset_path" --version 2>&1)"; then
      log_error version "failed to execute ${binary} --version"
      return 1
    fi
    got="$(printf '%s' "$got" | tr -d '[:space:]')"

    if [[ "$got" != "$expected_version" ]]; then
      log_error version "${binary} reports version '${got}', expected '${expected_version}'"
      return 1
    fi
  done

  return 0
}

# report_partial_install prints, on any atomic_install failure, which
# binaries were already renamed into place at the new version (indices
# 0..failed_index-1 of binaries) and which remain at their prior version
# (failed_index..end, including the one that just failed: its rename never
# happened). Pure operator-triage output, not a control-flow signal.
report_partial_install() {
  local expected_version="$1" failed_index="$2"
  shift 2
  local -a binaries=("$@")
  local -a updated=("${binaries[@]:0:${failed_index}}")
  local -a prior=("${binaries[@]:${failed_index}}")
  log_info "partial install at ${expected_version}: updated -> ${updated[*]:-<none>}; left at prior version -> ${prior[*]:-<none>}"
}

# atomic_install installs each binary by creating a temp file on the SAME
# filesystem as install_dir, copying the verified bytes into it, setting mode
# 0755, and renaming it into place. A partially-written or non-executable
# file is never visible at the final path: the rename is the only step that
# makes the new binary observable under its real name.
#
# sudo is used only for this function's own operations (temp file creation,
# copy, chmod, rename), and only when install_dir is not already writable by
# the invoking user. Every earlier stage (download, checksum verify, version
# verify) has already completed unprivileged.
#
# Every command below is explicitly guarded with its own `if ! ...; then
# return 1; fi` rather than relying on the script's top-level `set -e`: this
# function is invoked as `atomic_install ... || exit 1` in main, and bash
# suspends errexit for a function's entire body when the function call is
# the left operand of `||` (or the condition of if/while). Without an
# explicit guard, `chmod 0755 "$tmp"` failing silently would still let the
# loop reach `mv -f`, which succeeds, installing a non-executable binary and
# returning 0 as if nothing were wrong: the failure would only surface later,
# when systemd tries and fails to exec it.
#
# Note on partial-failure scope: because every binary was already checksum-
# and version-verified before this function runs, a failure partway through
# this loop can only ever leave the NOT-yet-installed binaries at their
# PRIOR version; no binary is ever left half-written. A fully transactional
# install across all four binaries (so a late failure rolls the already-
# renamed binaries back too) is out of scope here; broader rollback is
# Phase 3 (plan-1-release-binary-deploy).
atomic_install() {
  local scratch_dir="$1" install_dir="$2" expected_version="$3"
  shift 3
  local -a binaries=("$@")
  local -a sudo_cmd=()
  if [[ ! -w "$install_dir" ]]; then
    sudo_cmd=(sudo)
  fi

  local i binary asset src tmp
  for i in "${!binaries[@]}"; do
    binary="${binaries[$i]}"
    asset="$(asset_filename "$binary" "$expected_version")"
    src="${scratch_dir}/${asset}"

    if ! tmp="$("${sudo_cmd[@]}" mktemp "${install_dir}/.${binary}.XXXXXX")"; then
      log_error install "failed to create a temp file in ${install_dir} for ${binary}"
      report_partial_install "$expected_version" "$i" "${binaries[@]}"
      return 1
    fi

    if ! "${sudo_cmd[@]}" cp "$src" "$tmp"; then
      log_error install "failed to stage ${binary} into ${tmp}"
      "${sudo_cmd[@]}" rm -f "$tmp" || true
      report_partial_install "$expected_version" "$i" "${binaries[@]}"
      return 1
    fi

    if ! "${sudo_cmd[@]}" chmod 0755 "$tmp"; then
      log_error install "failed to set mode 0755 on staged ${binary} (${tmp}); aborting before install so a non-executable binary is never renamed into place"
      "${sudo_cmd[@]}" rm -f "$tmp" || true
      report_partial_install "$expected_version" "$i" "${binaries[@]}"
      return 1
    fi

    if ! "${sudo_cmd[@]}" mv -f "$tmp" "${install_dir}/${binary}"; then
      log_error install "failed to finalize ${binary} (staged copy left at ${tmp} for inspection)"
      report_partial_install "$expected_version" "$i" "${binaries[@]}"
      return 1
    fi
  done

  return 0
}

main() {
  local requested_version="${1:-}"
  local repo="$OPENBRAIN_REPO"
  local install_dir="$OPENBRAIN_INSTALL_DIR"
  local -a binaries=("${SERVICE_BINARIES[@]}")

  check_prereqs "$install_dir" || exit 1

  local version
  if ! version="$(resolve_version "$repo" "$requested_version")"; then
    exit 1
  fi
  log_info "installing openbrain service binaries, version ${version}, from ${repo} to ${install_dir}"

  local scratch_dir
  scratch_dir="$(mktemp -d)"
  # shellcheck disable=SC2064
  # Double-quoted (early) expansion is intentional here: it bakes the exact
  # scratch_dir path into the trap command at registration time, so cleanup
  # still targets the right directory even if a later edit introduces a
  # nested scope where the variable might not be visible when EXIT fires.
  # scratch_dir is never reassigned in this function, so deferred (single-
  # quoted) expansion would resolve to the same value; early expansion is
  # simply the more defensive choice.
  trap "rm -rf '$scratch_dir'" EXIT

  local -a binary_assets=()
  local b
  for b in "${binaries[@]}"; do
    binary_assets+=("$(asset_filename "$b" "$version")")
  done
  local -a assets=("${binary_assets[@]}" "$SUMS_FILE_NAME")

  download_release "$repo" "$version" "$scratch_dir" "${assets[@]}" || exit 1

  verify_checksums "$scratch_dir" "$SUMS_FILE_NAME" "${binary_assets[@]}" || exit 1

  verify_versions "$scratch_dir" "$version" "${binaries[@]}" || exit 1

  atomic_install "$scratch_dir" "$install_dir" "$version" "${binaries[@]}" || exit 1

  log_info "installed: ${binaries[*]} at ${version} in ${install_dir}"
}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  main "$@"
fi
