#!/usr/bin/env bash
# OpenBrain: repoint the four service units at /usr/local/bin, wire the
# two-tier health check and automatic rollback into the apply flow, and
# support pinning/reverting to a specific released version (Phase 3, OB-063,
# plan-1-release-binary-deploy).
#
# Scope (locked decisions, see plans/plan-1-release-binary-deploy):
#   - bigmon-only, systemd --user model, same four service binaries as the
#     Phase-2 installer (scripts/install-release.sh): openbrain-web,
#     openbrain-telegram, openbrain-slack, openbrain-watchd.
#   - The shipped unit files under deploy/*.service are NEVER edited in
#     place; ExecStart is overridden via a systemd drop-in
#     (<unit>.service.d/override.conf) instead.
#   - EnvironmentFile and every other unit directive besides ExecStart are
#     left untouched. The repo .env is never read, moved, or written here.
#   - This script never executes a real `systemctl --user` command against
#     the live bigmon units on its own initiative: that only happens when an
#     operator runs `apply`/`rollback` for real. Automated tests exercise
#     every code path against a mocked systemctl and curl; see
#     tests/repoint-unit.bats. The live cutover itself is a separate,
#     operator-gated step, not part of this card.
#
# Usage:
#   scripts/repoint-unit.sh [--dry-run] apply VERSION
#   scripts/repoint-unit.sh [--dry-run] rollback VERSION
#
#   apply     Install VERSION (via the Phase-2 installer), repoint the drop-
#             ins at /usr/local/bin, daemon-reload, restart, and run the
#             two-tier health check. A genuine health-check failure triggers
#             an automatic rollback to the previously-installed version.
#   rollback  Revert on demand to VERSION: reinstall it, repoint, reload,
#             restart, and recheck health. Does not itself auto-rollback
#             further on failure; it reports a clear, actionable error.
#   --dry-run Print the drop-in path, its contents, and the exact
#             `systemctl --user` command sequence that would run, for either
#             subcommand. Writes nothing, invokes neither systemctl nor curl.
#
# Environment overrides (for testing; production runs with defaults):
#   OPENBRAIN_REPO                  GitHub repo, default windingriverholdings/openbrain
#   OPENBRAIN_INSTALL_DIR           Install target, default /usr/local/bin
#   OPENBRAIN_SYSTEMD_USER_DIR      systemd --user unit dir, default ~/.config/systemd/user
#   OPENBRAIN_HEALTH_LOCAL_URL      Local health endpoint, default http://127.0.0.1:10203/health
#   OPENBRAIN_HEALTH_REMOTE_URL     Remote MCP endpoint, default https://openbrain.wr-s.net/mcp
#   OPENBRAIN_INSTALL_RELEASE_SCRIPT Path to the Phase-2 installer, default
#                                    scripts/install-release.sh next to this file
#
# This file is written so its functions can be sourced and unit tested
# directly; see tests/repoint-unit.bats. main() only runs when the script is
# executed, not when it is sourced.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

OPENBRAIN_REPO="${OPENBRAIN_REPO:-windingriverholdings/openbrain}"
OPENBRAIN_INSTALL_DIR="${OPENBRAIN_INSTALL_DIR:-/usr/local/bin}"
OPENBRAIN_SYSTEMD_USER_DIR="${OPENBRAIN_SYSTEMD_USER_DIR:-${HOME}/.config/systemd/user}"
OPENBRAIN_HEALTH_LOCAL_URL="${OPENBRAIN_HEALTH_LOCAL_URL:-http://127.0.0.1:10203/health}"
OPENBRAIN_HEALTH_REMOTE_URL="${OPENBRAIN_HEALTH_REMOTE_URL:-https://openbrain.wr-s.net/mcp}"
OPENBRAIN_INSTALL_RELEASE_SCRIPT="${OPENBRAIN_INSTALL_RELEASE_SCRIPT:-${SCRIPT_DIR}/install-release.sh}"

SERVICE_UNITS=(openbrain-web openbrain-telegram openbrain-slack openbrain-watchd)
PRIMARY_UNIT="openbrain-web"
SECONDARY_UNITS=(openbrain-telegram openbrain-slack openbrain-watchd)

# health_check return codes, named so callers don't compare against bare
# integers scattered through the file.
readonly HEALTH_OK=0
readonly HEALTH_FAIL=1
readonly HEALTH_REMOTE_UNREACHABLE=2

log_info() {
  printf '[repoint-unit] %s\n' "$*"
}

# log_error prints a message tagged with the failing stage, matching the
# install-release.sh convention so drop-in write, daemon-reload, restart,
# health-check, and rollback failures are distinguishable in the output.
log_error() {
  local stage="$1"
  shift
  printf '[repoint-unit] ERROR (%s): %s\n' "$stage" "$*" >&2
}

# drop_in_content renders the override.conf body for a unit. The empty
# ExecStart= line is required before the replacement: systemd APPENDS
# ExecStart= directives by default, so without the clearing line the
# original repo-checkout ExecStart from the shipped unit file would still
# run alongside (or instead of) the /usr/local/bin one.
drop_in_content() {
  local exec_path="$1"
  printf '[Service]\nExecStart=\nExecStart=%s' "$exec_path"
}

drop_in_path() {
  local systemd_dir="$1" unit="$2"
  printf '%s/%s.service.d/override.conf\n' "$systemd_dir" "$unit"
}

# write_drop_in is idempotent: if the target file already holds exactly the
# desired content, it is left untouched (no mtime change, no rewrite), which
# is what the AC's "running twice produces no drop-in diff" requires.
#
# Guarded explicitly throughout (not left to the surrounding `set -e`): this
# function is invoked as `write_drop_in ... || return 1` from
# repoint_and_restart, and bash suspends errexit for a function's entire body
# when the function call is the left operand of `||`. An unguarded mkdir or
# write failure here would otherwise be silently swallowed and the caller
# would proceed as if the drop-in were in place.
write_drop_in() {
  local systemd_dir="$1" unit="$2" install_dir="$3"
  local drop_in_dir="${systemd_dir}/${unit}.service.d"
  local drop_in_file
  drop_in_file="$(drop_in_path "$systemd_dir" "$unit")"
  local content
  content="$(drop_in_content "${install_dir}/${unit}")"

  if [[ -f "$drop_in_file" ]]; then
    local existing
    if existing="$(cat "$drop_in_file" 2>/dev/null)" && [[ "$existing" == "$content" ]]; then
      log_info "drop-in for ${unit} already matches the target (idempotent, no write): ${drop_in_file}"
      return 0
    fi
  fi

  if ! mkdir -p "$drop_in_dir"; then
    log_error dropin "failed to create ${drop_in_dir} for ${unit}"
    return 1
  fi

  if ! printf '%s\n' "$content" > "$drop_in_file"; then
    log_error dropin "failed to write ${drop_in_file} for ${unit}"
    return 1
  fi

  log_info "wrote drop-in for ${unit}: ${drop_in_file}"
  return 0
}

daemon_reload() {
  systemctl --user daemon-reload
}

restart_unit() {
  local unit="$1"
  systemctl --user restart "$unit"
}

# unit_is_active reports the unit's current active/inactive state as a plain
# boolean via the exit code; a nonzero from `systemctl is-active` here is
# expected state information, not a failure to guard against, so this is not
# wrapped in the errexit-suspension discipline used for abort-worthy steps.
unit_is_active() {
  local unit="$1"
  systemctl --user is-active --quiet "$unit"
}

# repoint_and_restart writes every unit's drop-in, reloads the daemon once,
# always restarts the primary unit (openbrain-web, the one the health check
# depends on), and restarts each secondary unit ONLY if it was already
# active: an inactive unit stays inactive, matching the AC's "apply does not
# change their active or inactive state."
#
# Every step is guarded explicitly: this function runs as
# `repoint_and_restart ... || return 1` from its callers, which suspends
# errexit for its whole body. Without the explicit guards, a daemon-reload
# failure could fall through to a restart against not-yet-reloaded unit
# definitions, and a restart failure could fall through to the health check
# as if nothing were wrong.
repoint_and_restart() {
  local systemd_dir="$1" install_dir="$2"
  local unit

  for unit in "${SERVICE_UNITS[@]}"; do
    if ! write_drop_in "$systemd_dir" "$unit" "$install_dir"; then
      return 1
    fi
  done

  if ! daemon_reload; then
    log_error dropin "daemon-reload failed after writing drop-ins"
    return 1
  fi

  if ! restart_unit "$PRIMARY_UNIT"; then
    log_error dropin "restart failed for ${PRIMARY_UNIT}"
    return 1
  fi

  for unit in "${SECONDARY_UNITS[@]}"; do
    if unit_is_active "$unit"; then
      if ! restart_unit "$unit"; then
        log_error dropin "restart failed for already-active ${unit}"
        return 1
      fi
    fi
  done

  return 0
}

# health_check_local runs the local /health probe and treats anything other
# than a reachable "ok" body as a failure. Local has no "unreachable but
# benign" tier the way the remote check does (there is no cloudflared, no
# third-party network hop between this box and 127.0.0.1): any failure here,
# whether a connection refusal or a non-2xx response, is genuinely broken,
# so a single pass/fail signal via curl -f is correct and does not share the
# remote check's exit-code-conflation flaw (see health_check_remote).
# Guarded explicitly: invoked as `health_check_local ... || ...` from
# run_health_check, which suspends errexit for this body.
health_check_local() {
  local url="$1"
  local body
  if ! body="$(curl -fsS --max-time 5 "$url" 2>/dev/null)"; then
    return 1
  fi
  body="$(printf '%s' "$body" | tr -d '[:space:]')"
  [[ "$body" == "ok" ]]
}

# health_check_remote POSTs a minimal MCP `initialize` request and reports
# three distinct outcomes via its exit code, so the caller can tell a
# genuine failure apart from an unrelated tunnel outage:
#   0  reachable and returned HTTP 200
#   1  reachable but returned a non-200 status: a genuine failure
#   2  unreachable (curl itself could not complete the request: DNS failure,
#      connection refused, TLS failure, timeout): treat as remote-unreachable,
#      not a genuine failure, per the data-invariants boundary-check
#      discipline (this is the "distinguish the boundary itself" case: a
#      cloudflared outage is not the same defect class as a broken
#      openbrain-web).
#
# Deliberately does NOT pass -f/--fail to curl. curl -f exits non-zero on
# ANY 4xx/5xx response, which is indistinguishable from curl's exit code on
# a genuine transport failure (connection refused is also non-zero). An
# earlier version used -f and branched only on curl's exit status, which
# misclassified a reachable-but-broken openbrain-web (HTTP 500) as
# HEALTH_REMOTE_UNREACHABLE, silently skipping the rollback a real outage
# requires. Reachability and HTTP status are two separate signals, checked
# separately below: curl_status says whether the request completed at all;
# http_code says what the server answered once it did.
health_check_remote() {
  local url="$1"
  local payload='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2026-06-18","capabilities":{},"clientInfo":{"name":"repoint-unit-healthcheck","version":"1"}}}'
  local http_code=""
  local curl_status=0

  # Guarded explicitly with the `|| curl_status=$?` capture pattern used
  # throughout this file: a bare assignment statement is still subject to
  # this script's own `set -euo pipefail`, so an unguarded non-zero curl
  # exit here would abort the caller before curl_status is ever inspected.
  http_code="$(curl -sS --max-time 10 -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' -d "$payload" "$url" 2>/dev/null)" || curl_status=$?

  if [[ "$curl_status" -ne 0 ]]; then
    return 2
  fi

  [[ "$http_code" == "200" ]] && return 0
  return 1
}

# run_health_check is the two-tier gate. A local failure is always a
# genuine failure (return HEALTH_FAIL). When local passes, a remote failure
# is a genuine failure but a remote connection failure (health_check_remote
# returning 2) is reported as HEALTH_REMOTE_UNREACHABLE, not a genuine
# failure, so a cloudflared outage does not trigger a rollback.
run_health_check() {
  local local_url="$1" remote_url="$2"

  if ! health_check_local "$local_url"; then
    log_error healthcheck "local health check failed: ${local_url}"
    return "$HEALTH_FAIL"
  fi
  log_info "local health check passed: ${local_url}"

  # Guarded explicitly: health_check_remote's own nonzero returns (1 for a
  # genuine failure, 2 for unreachable) are both meaningful states this
  # function must branch on, not aborts. Called as a bare statement under
  # this script's own `set -euo pipefail` (active even when sourced), an
  # unguarded nonzero return here would trip errexit and abort the whole
  # sourcing/caller before the case statement below ever ran, silently
  # losing the local-healthy-remote-unreachable distinction the AC requires.
  local remote_status=0
  health_check_remote "$remote_url" || remote_status=$?
  case "$remote_status" in
    0)
      log_info "remote health check passed: ${remote_url}"
      return "$HEALTH_OK"
      ;;
    2)
      log_info "remote health check UNREACHABLE (not a genuine failure, likely a tunnel outage): ${remote_url}"
      return "$HEALTH_REMOTE_UNREACHABLE"
      ;;
    *)
      log_error healthcheck "remote health check reachable but failed: ${remote_url}"
      return "$HEALTH_FAIL"
      ;;
  esac
}

# current_installed_version queries the primary unit's currently-installed
# binary via its self-identifying --version contract (Phase 1). Returns
# non-zero with no output when there is nothing installed yet to version;
# this is not an error, it just means there is no rollback target.
current_installed_version() {
  local install_dir="$1"
  local binary="${install_dir}/${PRIMARY_UNIT}"

  if [[ ! -x "$binary" ]]; then
    return 1
  fi

  local got
  if ! got="$("$binary" --version 2>&1)"; then
    return 1
  fi
  printf '%s' "$got" | tr -d '[:space:]'
  return 0
}

# install_version delegates to the Phase-2 installer (scripts/install-release.sh)
# for the download/checksum-verify/version-verify/atomic-install pipeline,
# rather than reimplementing it. Tests override
# OPENBRAIN_INSTALL_RELEASE_SCRIPT with a fixture stub so this file's tests
# stay scoped to repoint/rollback/health-check behavior, not the installer's
# own (already-covered) download logic.
install_version() {
  local repo="$1" install_dir="$2" version="$3"
  OPENBRAIN_REPO="$repo" OPENBRAIN_INSTALL_DIR="$install_dir" "$OPENBRAIN_INSTALL_RELEASE_SCRIPT" "$version"
}

# dry_run_plan prints exactly what apply/rollback would do: every drop-in's
# path and contents, and the exact systemctl --user command sequence, all
# without writing a file or invoking systemctl. The secondary units' restart
# is conditional on live state, which dry-run must not query (that would be
# a real systemctl invocation), so it is described rather than resolved.
dry_run_plan() {
  local systemd_dir="$1" install_dir="$2" version="$3"
  local unit drop_in_file content

  log_info "DRY RUN: no file will be written, no systemctl or curl command will be run"
  log_info "target version: ${version}"

  for unit in "${SERVICE_UNITS[@]}"; do
    drop_in_file="$(drop_in_path "$systemd_dir" "$unit")"
    content="$(drop_in_content "${install_dir}/${unit}")"
    log_info "would write ${drop_in_file}:"
    printf '%s\n' "$content"
  done

  log_info "would run: systemctl --user daemon-reload"
  log_info "would run: systemctl --user restart ${PRIMARY_UNIT}"
  log_info "would run, only for a secondary unit already active (dry-run does not query live state; an inactive unit is left inactive):"
  for unit in "${SECONDARY_UNITS[@]}"; do
    log_info "  systemctl --user restart ${unit}"
  done
  log_info "would then run the two-tier health check for ${PRIMARY_UNIT}: local ${OPENBRAIN_HEALTH_LOCAL_URL}, remote ${OPENBRAIN_HEALTH_REMOTE_URL}"
}

# rollback_to_version reinstalls a specific prior version, repoints/restarts,
# and rechecks health. Used both by cmd_apply's automatic-rollback path and
# by cmd_rollback's on-demand path. It does not recurse into a further
# rollback on its own failure: the caller surfaces a clear, actionable error
# instead of looping.
rollback_to_version() {
  local repo="$1" install_dir="$2" systemd_dir="$3" local_url="$4" remote_url="$5" version="$6"

  if ! install_version "$repo" "$install_dir" "$version"; then
    log_error rollback "failed to reinstall prior version ${version}"
    return 1
  fi

  if ! repoint_and_restart "$systemd_dir" "$install_dir"; then
    log_error rollback "failed to repoint/restart during rollback to ${version}"
    return 1
  fi

  # Guarded explicitly (see the matching comment in run_health_check): a
  # bare, unguarded call here would let a genuine-failure or
  # remote-unreachable return trip this script's own `set -euo pipefail`
  # and abort before the status is ever inspected below.
  local status=0
  run_health_check "$local_url" "$remote_url" || status=$?
  if [[ "$status" -eq "$HEALTH_OK" || "$status" -eq "$HEALTH_REMOTE_UNREACHABLE" ]]; then
    return 0
  fi

  log_error rollback "health check still failing after rollback to ${version}"
  return 1
}

# cmd_apply installs VERSION, repoints/restarts, and health-checks. On a
# genuine health-check failure it automatically rolls back to whatever
# version was installed immediately before this call (captured BEFORE
# install_version overwrites it), then reports a loud, actionable error if
# the rollback itself also fails, rather than looping.
cmd_apply() {
  local dry_run="$1" version="$2"
  local repo="$OPENBRAIN_REPO"
  local install_dir="$OPENBRAIN_INSTALL_DIR"
  local systemd_dir="$OPENBRAIN_SYSTEMD_USER_DIR"
  local local_url="$OPENBRAIN_HEALTH_LOCAL_URL"
  local remote_url="$OPENBRAIN_HEALTH_REMOTE_URL"

  if [[ -z "$version" ]]; then
    log_error apply "apply requires an explicit VERSION argument"
    return 1
  fi

  if [[ "$dry_run" == "1" ]]; then
    dry_run_plan "$systemd_dir" "$install_dir" "$version"
    return 0
  fi

  local previous_version=""
  previous_version="$(current_installed_version "$install_dir")" || previous_version=""

  if ! install_version "$repo" "$install_dir" "$version"; then
    log_error apply "failed to install version ${version}; aborting before any unit change"
    return 1
  fi

  if ! repoint_and_restart "$systemd_dir" "$install_dir"; then
    log_error apply "failed to repoint/restart units for version ${version}"
    return 1
  fi

  # Guarded explicitly (see the matching comment in run_health_check): a
  # bare, unguarded call here would let a genuine-failure or
  # remote-unreachable return trip this script's own `set -euo pipefail`
  # and abort before the rollback logic below ever ran.
  local health_status=0
  run_health_check "$local_url" "$remote_url" || health_status=$?

  if [[ "$health_status" -eq "$HEALTH_OK" || "$health_status" -eq "$HEALTH_REMOTE_UNREACHABLE" ]]; then
    log_info "apply of ${version} succeeded (health_status=${health_status})"
    return 0
  fi

  log_error healthcheck "genuine health-check failure at ${version}; attempting automatic rollback"

  if [[ -z "$previous_version" ]]; then
    log_error rollback "no previously-installed version was recorded; cannot auto-rollback. Manual intervention required: check 'systemctl --user status ${PRIMARY_UNIT}' and 'journalctl --user -u ${PRIMARY_UNIT} -n 100'"
    return 1
  fi

  if ! rollback_to_version "$repo" "$install_dir" "$systemd_dir" "$local_url" "$remote_url" "$previous_version"; then
    log_error rollback "automatic rollback to ${previous_version} FAILED after a health-check failure at ${version}; manual intervention required: check 'systemctl --user status ${PRIMARY_UNIT}' and 'journalctl --user -u ${PRIMARY_UNIT} -n 100'"
    return 1
  fi

  log_info "automatic rollback to ${previous_version} succeeded; service recovered"
  # Intentional: cmd_apply still reports failure here even though the
  # rollback itself succeeded. The requested version never became healthy;
  # the service being back on the prior version is a successful recovery,
  # not a successful apply of what the caller asked for.
  return 1
}

# cmd_rollback is the on-demand path: revert to an explicit prior version
# regardless of current health. It reuses rollback_to_version so the
# repoint/restart/health-check sequence is identical to the automatic path.
cmd_rollback() {
  local dry_run="$1" version="$2"
  local repo="$OPENBRAIN_REPO"
  local install_dir="$OPENBRAIN_INSTALL_DIR"
  local systemd_dir="$OPENBRAIN_SYSTEMD_USER_DIR"
  local local_url="$OPENBRAIN_HEALTH_LOCAL_URL"
  local remote_url="$OPENBRAIN_HEALTH_REMOTE_URL"

  if [[ -z "$version" ]]; then
    log_error rollback "rollback requires an explicit VERSION argument"
    return 1
  fi

  if [[ "$dry_run" == "1" ]]; then
    dry_run_plan "$systemd_dir" "$install_dir" "$version"
    return 0
  fi

  rollback_to_version "$repo" "$install_dir" "$systemd_dir" "$local_url" "$remote_url" "$version"
}

usage() {
  cat <<'EOF'
Usage:
  repoint-unit.sh [--dry-run] apply VERSION
  repoint-unit.sh [--dry-run] rollback VERSION
EOF
}

main() {
  local dry_run=0
  local -a args=()
  local a
  for a in "$@"; do
    case "$a" in
      --dry-run) dry_run=1 ;;
      *) args+=("$a") ;;
    esac
  done

  local cmd="${args[0]:-}"
  local version="${args[1]:-}"

  case "$cmd" in
    apply)
      cmd_apply "$dry_run" "$version" || exit 1
      ;;
    rollback)
      cmd_rollback "$dry_run" "$version" || exit 1
      ;;
    *)
      usage >&2
      exit 1
      ;;
  esac
}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  main "$@"
fi
