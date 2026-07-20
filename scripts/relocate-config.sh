#!/usr/bin/env bash
# OpenBrain: relocate secret-bearing config from the repo checkout to the
# FHS location for host-local config (Phase 3.5, OB-066,
# plan-1-release-binary-deploy). Copies SOURCE (defaults to the repo's own
# .env) to DEST (defaults to /etc/openbrain/openbrain.env), mode 0600,
# owned by the run-as user, never committed to git.
#
# Usage:
#   scripts/relocate-config.sh dry-run [SOURCE] [DEST]
#   scripts/relocate-config.sh apply [SOURCE] [DEST]
#
#   dry-run Print exactly what apply would do (source, dest, mode 0600,
#           owner) and write/execute NOTHING. No privilege check. This is the
#           preview that lets the secret-config move be inspected before it
#           runs, mirroring openknowledge's relocate-openknowledge-config.sh
#           dry-run subcommand.
#   apply   Perform the relocation: copy SOURCE to DEST atomically at mode
#           0600, owned by the run-as user.
#
#   SOURCE  Optional path to the source env file. Default: the repo's own
#           .env, resolved relative to this script's location.
#   DEST    Optional path to the destination env file. Default:
#           /etc/openbrain/openbrain.env.
#
# What this does, in order:
#   1. Verifies SOURCE exists and is readable. Nothing is written if it does
#      not (fail closed: a missing source is a hard error, never silently
#      skipped).
#   2. Ensures DEST's parent directory exists (install -d -m 0755): world
#      traversable (a system unit's EnvironmentFile lookup needs the
#      directory itself readable/executable), but the file inside it is
#      0600 so its contents stay private.
#   3. Idempotent: if DEST already holds byte-identical content, correct
#      mode, and correct owner, no write happens at all (matching the
#      write_drop_in idiom in scripts/deploy-system.sh: running this twice
#      produces no diff and no mtime change on the second run).
#   4. Otherwise writes DEST atomically: a temp file in DEST's own
#      directory (same filesystem, so the final `mv` is a rename, not a
#      cross-filesystem copy), mode 0600 set BEFORE any content is written
#      to it, then renamed onto DEST in one step. A partially-written
#      secrets file is never visible at DEST.
#   5. Owner is set to OWNER only when this process can actually chown
#      (running as root, or via sudo). When DEST's directory is already
#      writable by the invoking user without sudo (the common case in
#      tests, and the common case for the same-user production deploy),
#      chown is skipped: the file the invoking user just wrote is already
#      owned by that user, and there is nothing to escalate for.
#
# What this NEVER does:
#   - Read, move, or overwrite anything other than the one SOURCE file
#     named on the command line (or defaulted to the repo .env).
#   - Stage SOURCE or DEST through git in any way.
#   - Leave a partially-written file visible at DEST on any failure path.
#
# Environment overrides (for testing; production runs with defaults):
#   OPENBRAIN_RELOCATE_OWNER  Target owner for DEST, default craig8
#   OPENBRAIN_RELOCATE_SUDO   sudo binary name, default sudo
#
# This file is written so its functions can be sourced and unit tested
# directly; see tests/relocate-config.bats. main() only runs when the
# script is executed, not when it is sourced.
#
# Executing this against the real /etc/openbrain is explicitly OUT OF
# SCOPE here: tests always target a sandbox DEST. The live config move is a
# separate, operator-gated step Craig runs later.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

OPENBRAIN_RELOCATE_OWNER="${OPENBRAIN_RELOCATE_OWNER:-craig8}"
OPENBRAIN_RELOCATE_SUDO="${OPENBRAIN_RELOCATE_SUDO:-sudo}"

DEFAULT_SOURCE="${SCRIPT_DIR}/../.env"
DEFAULT_DEST="/etc/openbrain/openbrain.env"

log_info() {
  printf '[relocate-config] %s\n' "$*"
}

log_error() {
  local stage="$1"
  shift
  printf '[relocate-config] ERROR (%s): %s\n' "$stage" "$*" >&2
}

# check_source fails closed when SOURCE is missing or unreadable, before
# anything at DEST is touched.
check_source() {
  local source="$1"

  if [[ ! -f "$source" ]]; then
    log_error source "source file does not exist: ${source}"
    return 1
  fi

  if [[ ! -r "$source" ]]; then
    log_error source "source file is not readable: ${source}"
    return 1
  fi

  return 0
}

# dest_dir_writable reports (via exit code) whether the invoking user can
# already write into DEST's parent directory, creating it first if it does
# not exist yet and the grandparent is writable. This is the sole signal
# used to decide whether sudo is needed for the directory-creation and
# write steps: it does not assume anything about EUID.
dest_dir_writable() {
  local dest_dir="$1"

  if [[ -d "$dest_dir" ]]; then
    [[ -w "$dest_dir" ]]
    return $?
  fi

  local parent
  parent="$(dirname "$dest_dir")"
  [[ -w "$parent" ]]
}

# check_privilege fails closed when DEST's directory is not writable by the
# invoking user AND no sudo binary is on PATH: there is no path to complete
# the relocation.
check_privilege() {
  local dest_dir="$1" sudo_bin="$2"

  if dest_dir_writable "$dest_dir"; then
    return 0
  fi

  if command -v "$sudo_bin" >/dev/null 2>&1; then
    return 0
  fi

  log_error privilege "no write permission to ${dest_dir}, and '${sudo_bin}' is not on PATH; cannot create or write it"
  return 1
}

# ensure_dest_dir creates DEST's parent directory, mode 0755 (traversable,
# so a system unit resolving EnvironmentFile can reach the file inside;
# the file itself is 0600 and carries the actual privacy).
ensure_dest_dir() {
  local dest_dir="$1" use_sudo="$2" sudo_bin="$3"
  local -a priv=()
  [[ "$use_sudo" == "1" ]] && priv=("$sudo_bin")

  if [[ -d "$dest_dir" ]]; then
    return 0
  fi

  if ! "${priv[@]}" install -d -m 0755 "$dest_dir" 2>&1; then
    log_error dir "failed to create ${dest_dir}"
    return 1
  fi

  return 0
}

# needs_write reports (via exit code) whether DEST differs from SOURCE in
# content, mode, or owner. Returns 0 (a write IS needed) if DEST is absent,
# its content differs, its mode is not 0600, or its owner is not OWNER.
# This is what makes relocate_config idempotent: an unchanged second run
# performs no write and no mtime change at all.
needs_write() {
  local source="$1" dest="$2" owner="$3"

  if [[ ! -f "$dest" ]]; then
    return 0
  fi

  if ! cmp -s "$source" "$dest"; then
    return 0
  fi

  local mode
  mode="$(stat -c '%a' "$dest" 2>/dev/null || true)"
  if [[ "$mode" != "600" ]]; then
    return 0
  fi

  local current_owner
  current_owner="$(stat -c '%U' "$dest" 2>/dev/null || true)"
  if [[ "$current_owner" != "$owner" ]]; then
    return 0
  fi

  return 1
}

# write_dest atomically writes SOURCE's content to DEST: a temp file in
# DEST's own directory (same filesystem as DEST, so the final rename is
# atomic), mode 0600 set before any content lands in it, then renamed onto
# DEST in one step. Guarded explicitly throughout (not left to the
# surrounding `set -e`): this function is invoked as
# `write_dest ... || return 1` from relocate_config, which suspends errexit
# for its whole body, so every abort-worthy command here is checked
# explicitly rather than relying on that suspended errexit to catch it.
write_dest() {
  local source="$1" dest="$2" owner="$3" use_sudo="$4" sudo_bin="$5"
  local dest_dir
  dest_dir="$(dirname "$dest")"
  local -a priv=()
  [[ "$use_sudo" == "1" ]] && priv=("$sudo_bin")

  local tmp_path
  if ! tmp_path="$("${priv[@]}" mktemp "${dest_dir}/.openbrain.env.XXXXXX" 2>&1)"; then
    log_error write "could not create a temp file in ${dest_dir}"
    return 1
  fi

  if ! "${priv[@]}" chmod 0600 "$tmp_path" 2>&1; then
    log_error write "could not set mode 0600 on ${tmp_path}"
    "${priv[@]}" rm -f "$tmp_path" 2>/dev/null || true
    return 1
  fi

  if ! "${priv[@]}" cp -- "$source" "$tmp_path" 2>&1; then
    log_error write "could not copy ${source} to ${tmp_path}"
    "${priv[@]}" rm -f "$tmp_path" 2>/dev/null || true
    return 1
  fi

  # Owner is set only when we can actually chown (root, or sudo). When
  # writing unprivileged (use_sudo=0), the file the invoking user just
  # wrote is already owned by that user; there is nothing to escalate to.
  if [[ "$use_sudo" == "1" ]]; then
    if ! "${priv[@]}" chown "${owner}:${owner}" "$tmp_path" 2>&1; then
      log_error write "could not chown ${tmp_path} to ${owner}"
      "${priv[@]}" rm -f "$tmp_path" 2>/dev/null || true
      return 1
    fi
  fi

  if ! "${priv[@]}" mv -f -- "$tmp_path" "$dest" 2>&1; then
    log_error write "atomic rename of ${tmp_path} to ${dest} failed"
    "${priv[@]}" rm -f "$tmp_path" 2>/dev/null || true
    return 1
  fi

  return 0
}

# relocate_config is the end-to-end sequence: check source, decide
# privilege, ensure the dest directory, skip if already up to date
# (idempotent), otherwise write atomically. Every abort-worthy step is
# guarded explicitly with an if/return, not left to `set -e`, because
# main() invokes this as `relocate_config ... || exit 1`, which suspends
# errexit for this function's entire body.
relocate_config() {
  local source="$1" dest="$2" owner="$3" sudo_bin="$4"
  local dest_dir
  dest_dir="$(dirname "$dest")"

  if ! check_source "$source"; then
    return 2
  fi

  if ! check_privilege "$dest_dir" "$sudo_bin"; then
    return 3
  fi

  local use_sudo=0
  if ! dest_dir_writable "$dest_dir"; then
    use_sudo=1
  fi

  if ! ensure_dest_dir "$dest_dir" "$use_sudo" "$sudo_bin"; then
    return 4
  fi

  if ! needs_write "$source" "$dest" "$owner"; then
    log_info "already up to date, no write needed: ${dest}"
    return 0
  fi

  if ! write_dest "$source" "$dest" "$owner" "$use_sudo" "$sudo_bin"; then
    return 5
  fi

  log_info "relocated ${source} -> ${dest} (mode 0600, owner ${owner})"
  return 0
}

# dry_run prints exactly what apply would do and writes/executes nothing. It
# performs NO privilege check and touches NO file: it reads only the SOURCE
# path string and the configured owner, and prints the source, dest, mode
# (always 0600), and owner. The secret's CONTENTS are never read or printed;
# only the path is. This is the preview that makes the secret-config move
# inspectable before it runs.
dry_run() {
  local source="$1" dest="$2" owner="$3"

  cat <<EOF
[relocate-config] DRY RUN: nothing below is written or executed; no privilege check performed.
[relocate-config] would copy source: ${source}
[relocate-config]            to dest: ${dest}
[relocate-config]               mode: 0600 (owner read/write only)
[relocate-config]              owner: ${owner}
EOF
  return 0
}

usage() {
  cat <<'EOF'
Usage:
  relocate-config.sh dry-run [SOURCE] [DEST]
  relocate-config.sh apply [SOURCE] [DEST]

  dry-run Print what apply would do (source, dest, mode 0600, owner) and
          write/execute nothing. No privilege check.
  apply   Copy SOURCE to DEST atomically at mode 0600, owned by the run-as
          user.

  SOURCE  Path to the source env file. Default: the repo's own .env.
  DEST    Path to the destination env file. Default:
          /etc/openbrain/openbrain.env.
EOF
}

main() {
  local cmd="${1:-}"

  case "$cmd" in
    -h | --help)
      usage
      return 0
      ;;
    "")
      log_error usage "a command is required (dry-run or apply)"
      usage >&2
      return 1
      ;;
    dry-run)
      dry_run "${2:-$DEFAULT_SOURCE}" "${3:-$DEFAULT_DEST}" "$OPENBRAIN_RELOCATE_OWNER"
      return $?
      ;;
    apply)
      relocate_config "${2:-$DEFAULT_SOURCE}" "${3:-$DEFAULT_DEST}" "$OPENBRAIN_RELOCATE_OWNER" "$OPENBRAIN_RELOCATE_SUDO"
      return $?
      ;;
    *)
      log_error usage "unrecognized command '${cmd}' (expected dry-run or apply)"
      usage >&2
      return 1
      ;;
  esac
}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  main "$@"
  exit $?
fi
