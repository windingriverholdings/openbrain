#!/usr/bin/env bash
# OpenBrain: repoint/deploy tool for the four SYSTEM units (Phase 3.5,
# OB-066, plan-1-release-binary-deploy). Supersedes OB-063's
# scripts/repoint-unit.sh, which was --user-scoped and is retired by this
# same change. Modeled on openknowledge's
# scripts/repoint-openknowledge.sh (KM-355): drop-in/reload/restart/
# health-check/rollback discipline, exit-code contract, and dry-run shape,
# adapted here for FOUR units instead of one and for openbrain's existing
# two-tier health check (OB-063's local + remote MCP-initialize probe).
#
# Scope (locked decisions, see plans/plan-1-release-binary-deploy and
# phase-3.5-system-service-conversion.md):
#   - bigmon-only, four system units at /etc/systemd/system/:
#     openbrain-web, openbrain-telegram, openbrain-slack, openbrain-watchd.
#   - The shipped unit files under deploy/*.service are NEVER edited in
#     place; ExecStart is overridden via a systemd drop-in
#     (<unit>.service.d/override.conf), same convention as KM-355.
#   - EnvironmentFile and every other unit directive besides ExecStart are
#     left untouched. This tool never reads, moves, or writes
#     /etc/openbrain/openbrain.env or the repo .env (that is
#     scripts/relocate-config.sh's job, run separately).
#   - This script never executes a real systemctl/curl against the live
#     bigmon units on its own initiative: that only happens when an
#     operator runs apply/rollback for real. Automated tests exercise
#     every code path against a mocked systemctl and curl; see
#     tests/deploy-system.bats. The live cutover itself is a separate,
#     operator-gated step, not part of this card.
#
# Usage:
#   scripts/deploy-system.sh dry-run [VERSION]
#   scripts/deploy-system.sh apply [VERSION]
#   scripts/deploy-system.sh rollback VERSION
#
#   VERSION   For dry-run/apply: optional released tag. Omitted resolves
#             the latest GitHub Release (same semantics as the Phase 2
#             installer, scripts/install-release.sh). For rollback:
#             REQUIRED. On-demand rollback never guesses which prior
#             version to revert to.
#
# What "apply" does, in order:
#   1. Privilege check: these are SYSTEM units, so daemon-reload and
#      restart require root or sudo. Fails closed with a clear message if
#      neither is available. dry-run needs no privilege at all: it only
#      prints, never executes or writes.
#   2. Installs VERSION (or latest) via the Phase 2 installer
#      (scripts/install-release.sh), reused as-is: checksum verification,
#      the self-identifying-binary version check, and the atomic install
#      are the installer's job, not reimplemented here. The binary is
#      ALWAYS installed before any unit is repointed at it (install before
#      activate).
#   3. Writes an ExecStart-only systemd drop-in for EACH of the four units
#      (<unit>.service.d/override.conf under /etc/systemd/system/). The
#      shipped unit files are NEVER edited in place. Every other directive
#      (EnvironmentFile, ReadWritePaths, ProtectSystem, User, etc.) is
#      untouched by construction: the drop-in's rendered content is ONLY a
#      bare `ExecStart=` reset (systemd APPENDS ExecStart= across drop-ins
#      unless the list is cleared first) followed by the new `ExecStart=`
#      line.
#   4. `systemctl daemon-reload` once, then restarts openbrain-web (the
#      primary unit the health check depends on) unconditionally, and
#      restarts each of the other three units ONLY if it was already
#      active: an inactive secondary stays inactive, matching today's
#      operational reality (only openbrain-web is normally up).
#   5. Runs the two-tier health check: local http://127.0.0.1:10203/health,
#      then, only if local passes, the remote https://openbrain.wr-s.net/mcp
#      `initialize` POST. A remote connection failure (unreachable, not a
#      genuine HTTP failure) is treated as "cannot confirm, not a rollback
#      trigger" per the boundary-of-the-boundary discipline: a cloudflared
#      outage is not the same defect class as a broken openbrain-web.
#   6. On a genuine health-check failure: reinstalls whichever version was
#      active before this apply (captured before the new version was
#      installed) via the same Phase 2 installer, repoints/restarts, and
#      re-checks health exactly once more. A second failure is a loud,
#      actionable error; this tool never restart-loops trying to recover
#      on its own. At most one cutover attempt and at most one automatic
#      rollback attempt per apply.
#
# What "rollback VERSION" does: installs VERSION via the same idempotent
# Phase 2 installer, then runs the same drop-in/reload/restart/
# health-check cutover as apply. No automatic rollback-of-a-rollback is
# attempted on on-demand rollback failure: it surfaces a distinct,
# actionable error.
#
# What "dry-run" does: prints every drop-in path and its exact rendered
# contents, and the exact systemctl/curl commands apply would run, for
# VERSION or latest. Writes nothing, invokes nothing (not even a privilege
# check).
#
# Exit codes (distinct per failure class, matching KM-355's contract):
#   0  success
#   1  usage error
#   2  privilege check failed (not root, no sudo on PATH)
#   3  install of the requested/latest version failed (nothing repointed)
#   4  drop-in write failed for one or more units
#   5  daemon-reload failed
#   6  restart failed (primary or an already-active secondary)
#   7  health check failed and no further automatic rollback was possible
#      or attempted (rollback path; or apply path with no known previous
#      version to fall back to)
#   8  automatic rollback's reinstall of the previous version itself
#      failed (apply path only): service state is unknown, manual
#      intervention required
#   9  automatic rollback reinstalled the previous version but it still
#      failed its health check (apply path only): manual intervention
#      required
#  10  automatic rollback to the previous version SUCCEEDED (service is
#      healthy again), but the originally requested version never became
#      live: this run did not achieve its goal even though the service
#      recovered
#
# Environment overrides (for testing; production runs with defaults):
#   OPENBRAIN_REPO                   GitHub repo, default
#                                     windingriverholdings/openbrain
#   OPENBRAIN_INSTALL_DIR            Install target, default /usr/local/bin
#   OPENBRAIN_DS_SYSTEMD_DIR         systemd system unit dir, default
#                                     /etc/systemd/system
#   OPENBRAIN_DS_HEALTH_LOCAL_URL    Local health endpoint, default
#                                     http://127.0.0.1:10203/health
#   OPENBRAIN_DS_HEALTH_REMOTE_URL   Remote MCP endpoint, default
#                                     https://openbrain.wr-s.net/mcp
#   OPENBRAIN_DS_INSTALL_SCRIPT      Path to the Phase 2 installer, default
#                                     scripts/install-release.sh next to
#                                     this file
#   OPENBRAIN_DS_SYSTEMCTL           systemctl binary, default systemctl
#   OPENBRAIN_DS_SUDO                sudo binary, default sudo
#   OPENBRAIN_DS_EUID                Effective privilege identity. Defaults
#                                     to the real EUID; tests override this
#                                     to exercise both the "already root"
#                                     and "needs sudo" branches
#                                     deterministically.
#
# Testability: every function is a pure, sourceable unit taking overridable
# config and explicit arguments, so tests/deploy-system.bats sources this
# file and stubs `systemctl` and `curl` on PATH. No test ever invokes the
# real systemctl or writes into the real
# /etc/systemd/system/<unit>.service.d: OPENBRAIN_DS_SYSTEMD_DIR always
# points at a sandbox in tests, and OPENBRAIN_DS_EUID forces the
# unprivileged/no-sudo code path so nothing in the test suite can escalate.
#
# Dash-clean throughout (no em-dash, en-dash, or double-hyphen punctuation).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

OPENBRAIN_REPO="${OPENBRAIN_REPO:-windingriverholdings/openbrain}"
OPENBRAIN_INSTALL_DIR="${OPENBRAIN_INSTALL_DIR:-/usr/local/bin}"
OPENBRAIN_DS_SYSTEMD_DIR="${OPENBRAIN_DS_SYSTEMD_DIR:-/etc/systemd/system}"
OPENBRAIN_DS_HEALTH_LOCAL_URL="${OPENBRAIN_DS_HEALTH_LOCAL_URL:-http://127.0.0.1:10203/health}"
OPENBRAIN_DS_HEALTH_REMOTE_URL="${OPENBRAIN_DS_HEALTH_REMOTE_URL:-https://openbrain.wr-s.net/mcp}"
OPENBRAIN_DS_INSTALL_SCRIPT="${OPENBRAIN_DS_INSTALL_SCRIPT:-${SCRIPT_DIR}/install-release.sh}"
OPENBRAIN_DS_SYSTEMCTL="${OPENBRAIN_DS_SYSTEMCTL:-systemctl}"
OPENBRAIN_DS_SUDO="${OPENBRAIN_DS_SUDO:-sudo}"
OPENBRAIN_DS_EUID="${OPENBRAIN_DS_EUID:-$EUID}"
# Bounds current_installed_version's `--version` probe so a stalled or
# misbehaving previous binary cannot hang a cutover or rollback
# indefinitely.
OPENBRAIN_DS_VERSION_CHECK_TIMEOUT="${OPENBRAIN_DS_VERSION_CHECK_TIMEOUT:-5}"

SERVICE_UNITS=(openbrain-web openbrain-telegram openbrain-slack openbrain-watchd)
PRIMARY_UNIT="openbrain-web"
SECONDARY_UNITS=(openbrain-telegram openbrain-slack openbrain-watchd)

# health_check return codes, named so callers don't compare against bare
# integers scattered through the file.
readonly HEALTH_OK=0
readonly HEALTH_FAIL=1
readonly HEALTH_REMOTE_UNREACHABLE=2

log_info() {
  printf '[deploy-system] %s\n' "$*"
}

log_error() {
  local stage="$1"
  shift
  printf '[deploy-system] ERROR (%s): %s\n' "$stage" "$*" >&2
}

# ---------------------------------------------------------------------------
# Privilege
# ---------------------------------------------------------------------------

# check_privilege fails closed when the process is neither root nor has
# sudo on PATH: daemon-reload and restart of a system unit require one or
# the other. EUID is passed explicitly (not read from the shell's own
# $EUID here) so tests can exercise both branches without needing actual
# root or an actual working sudo.
check_privilege() {
  local sudo_bin="$1" euid="$2"

  if [[ "$euid" == "0" ]]; then
    return 0
  fi

  if command -v "$sudo_bin" >/dev/null 2>&1; then
    return 0
  fi

  log_error privilege "not running as root and '${sudo_bin}' is not on PATH; daemon-reload and restart of the system units require root or sudo"
  return 1
}

# use_sudo prints "0" when OPENBRAIN_DS_EUID is root, "1" otherwise.
# Centralizes the decision so every caller agrees on it.
use_sudo() {
  if [[ "$OPENBRAIN_DS_EUID" == "0" ]]; then
    printf '0'
  else
    printf '1'
  fi
}

# ---------------------------------------------------------------------------
# Drop-in rendering and writing
# ---------------------------------------------------------------------------

# drop_in_content renders the override.conf body for a unit. The empty
# ExecStart= line is required before the replacement: systemd APPENDS
# ExecStart= directives by default, so without the clearing line the
# original repo-checkout ExecStart from the shipped unit file would still
# run alongside (or instead of) the new /usr/local/bin one.
drop_in_content() {
  local exec_path="$1"
  cat <<EOF
# Managed by scripts/deploy-system.sh (OB-066). Do not hand-edit: repoint
# the install target by re-running that tool, not by editing this file or
# the shipped unit directly.
[Service]
ExecStart=
ExecStart=${exec_path}
EOF
}

drop_in_dir() {
  local systemd_dir="$1" unit="$2"
  printf '%s/%s.service.d\n' "$systemd_dir" "$unit"
}

drop_in_path() {
  local systemd_dir="$1" unit="$2"
  printf '%s/override.conf\n' "$(drop_in_dir "$systemd_dir" "$unit")"
}

# write_drop_in is idempotent: if the target file already holds exactly the
# desired content, it is left untouched (no mtime change, no rewrite).
# Otherwise it writes atomically: a temp file in the drop-in directory
# (same filesystem), then a single rename onto the final path, so a
# partially-written drop-in is never visible there.
#
# Guarded explicitly throughout (not left to the surrounding `set -e`):
# this function is invoked as `write_drop_in ... || return 1` from
# repoint_and_restart, which suspends errexit for its whole body. An
# unguarded mkdir/mktemp/write failure here would otherwise be silently
# swallowed and the caller would proceed as if the drop-in were in place.
write_drop_in() {
  local systemd_dir="$1" unit="$2" install_dir="$3" use_sudo_flag="$4" sudo_bin="$5"
  local -a priv=()
  [[ "$use_sudo_flag" == "1" ]] && priv=("$sudo_bin")
  local dir path content
  dir="$(drop_in_dir "$systemd_dir" "$unit")"
  path="$(drop_in_path "$systemd_dir" "$unit")"
  content="$(drop_in_content "${install_dir}/${unit}")"

  if [[ -f "$path" ]]; then
    local existing
    if existing="$(cat "$path" 2>/dev/null)" && [[ "$existing" == "$content" ]]; then
      log_info "drop-in for ${unit} already matches the target (idempotent, no write): ${path}"
      return 0
    fi
  fi

  if ! "${priv[@]}" mkdir -p "$dir" 2>&1; then
    log_error dropin "failed to create ${dir} for ${unit}"
    return 1
  fi

  local tmp_path
  if ! tmp_path="$("${priv[@]}" mktemp "${dir}/.override.conf.XXXXXX" 2>&1)"; then
    log_error dropin "failed to create a temp file in ${dir} for ${unit}"
    return 1
  fi

  if ! printf '%s' "$content" | "${priv[@]}" tee "$tmp_path" >/dev/null 2>&1; then
    log_error dropin "failed to write ${tmp_path} for ${unit}"
    "${priv[@]}" rm -f "$tmp_path" 2>/dev/null || true
    return 1
  fi

  if ! "${priv[@]}" chmod 0644 "$tmp_path" 2>&1; then
    log_error dropin "failed to set mode 0644 on ${tmp_path} for ${unit}"
    "${priv[@]}" rm -f "$tmp_path" 2>/dev/null || true
    return 1
  fi

  if ! "${priv[@]}" mv -f -- "$tmp_path" "$path" 2>&1; then
    log_error dropin "atomic rename of ${tmp_path} to ${path} failed for ${unit}"
    "${priv[@]}" rm -f "$tmp_path" 2>/dev/null || true
    return 1
  fi

  log_info "wrote drop-in for ${unit}: ${path}"
  return 0
}

# ---------------------------------------------------------------------------
# systemctl operations
# ---------------------------------------------------------------------------

daemon_reload() {
  local systemctl_bin="$1" use_sudo_flag="$2" sudo_bin="$3"
  local -a priv=()
  [[ "$use_sudo_flag" == "1" ]] && priv=("$sudo_bin")

  if ! "${priv[@]}" "$systemctl_bin" daemon-reload 2>&1; then
    log_error dropin "daemon-reload failed"
    return 1
  fi
  return 0
}

restart_unit() {
  local systemctl_bin="$1" unit="$2" use_sudo_flag="$3" sudo_bin="$4"
  local -a priv=()
  [[ "$use_sudo_flag" == "1" ]] && priv=("$sudo_bin")

  if ! "${priv[@]}" "$systemctl_bin" restart "${unit}.service" 2>&1; then
    log_error dropin "restart failed for ${unit}"
    return 1
  fi
  return 0
}

# unit_is_active reports the unit's current active/inactive state as a
# plain boolean via the exit code; a nonzero from `systemctl is-active` is
# expected state information, not a failure to guard against, so this is
# not part of the errexit-suspension discipline used for abort-worthy
# steps: it is only ever called as an if-condition.
unit_is_active() {
  local systemctl_bin="$1" unit="$2" use_sudo_flag="$3" sudo_bin="$4"
  local -a priv=()
  [[ "$use_sudo_flag" == "1" ]] && priv=("$sudo_bin")
  "${priv[@]}" "$systemctl_bin" is-active --quiet "${unit}.service"
}

# repoint_and_restart writes every unit's drop-in, reloads the daemon once,
# always restarts the primary unit (openbrain-web, the one the health
# check depends on), and restarts each secondary unit ONLY if it was
# already active: an inactive unit stays inactive, matching today's
# operational reality (only openbrain-web is normally up).
#
# Every step is guarded explicitly: this function runs as
# `repoint_and_restart ... || return N` from its callers, which suspends
# errexit for its whole body. Without the explicit guards, a daemon-reload
# failure could fall through to a restart against not-yet-reloaded unit
# definitions, and a restart failure could fall through to the health
# check as if nothing were wrong. Returns distinct codes (4 drop-in, 5
# daemon-reload, 6 restart) so callers can report the exact failing stage.
repoint_and_restart() {
  local systemd_dir="$1" install_dir="$2" systemctl_bin="$3" sudo_bin="$4"
  local use_sudo_flag
  use_sudo_flag="$(use_sudo)"
  local unit

  for unit in "${SERVICE_UNITS[@]}"; do
    if ! write_drop_in "$systemd_dir" "$unit" "$install_dir" "$use_sudo_flag" "$sudo_bin"; then
      return 4
    fi
  done

  if ! daemon_reload "$systemctl_bin" "$use_sudo_flag" "$sudo_bin"; then
    return 5
  fi

  if ! restart_unit "$systemctl_bin" "$PRIMARY_UNIT" "$use_sudo_flag" "$sudo_bin"; then
    return 6
  fi

  for unit in "${SECONDARY_UNITS[@]}"; do
    if unit_is_active "$systemctl_bin" "$unit" "$use_sudo_flag" "$sudo_bin"; then
      if ! restart_unit "$systemctl_bin" "$unit" "$use_sudo_flag" "$sudo_bin"; then
        return 6
      fi
    fi
  done

  return 0
}

# ---------------------------------------------------------------------------
# Two-tier health check (local, then remote MCP initialize)
# ---------------------------------------------------------------------------

# health_check_local runs the local /health probe and treats anything
# other than a reachable "ok" body as a failure. Local has no
# "unreachable but benign" tier the way the remote check does (there is no
# cloudflared, no third-party network hop between this box and
# 127.0.0.1): any failure here, whether a connection refusal or a non-2xx
# response, is genuinely broken.
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
#   2  unreachable (curl itself could not complete the request): treat as
#      remote-unreachable, not a genuine failure, per the
#      boundary-of-the-boundary discipline: a cloudflared outage is not
#      the same defect class as a broken openbrain-web.
#
# Deliberately does NOT pass -f/--fail to curl: that would make ANY
# 4xx/5xx response exit non-zero, indistinguishable from a genuine
# transport failure. Reachability and HTTP status are checked separately:
# curl_status says whether the request completed at all; http_code says
# what the server answered once it did.
health_check_remote() {
  local url="$1"
  local payload='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2026-06-18","capabilities":{},"clientInfo":{"name":"deploy-system-healthcheck","version":"1"}}}'
  local http_code=""
  local curl_status=0

  http_code="$(curl -sS --max-time 10 -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' -d "$payload" "$url" 2>/dev/null)" || curl_status=$?

  if [[ "$curl_status" -ne 0 ]]; then
    return 2
  fi

  [[ "$http_code" == "200" ]] && return 0
  return 1
}

# run_health_check is the two-tier gate. A local failure is always a
# genuine failure. When local passes, a remote failure is a genuine
# failure, but a remote connection failure is reported as
# HEALTH_REMOTE_UNREACHABLE, not a genuine failure, so a cloudflared
# outage does not trigger a rollback.
#
# Guarded explicitly: invoked as a bare statement in callers under `local
# status=0; run_health_check ... || status=$?`, because both of
# health_check_remote's nonzero returns (1 genuine failure, 2 unreachable)
# are meaningful states this function must branch on, not aborts.
run_health_check() {
  local local_url="$1" remote_url="$2"

  if ! health_check_local "$local_url"; then
    log_error healthcheck "local health check failed: ${local_url}"
    return "$HEALTH_FAIL"
  fi
  log_info "local health check passed: ${local_url}"

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

# ---------------------------------------------------------------------------
# Install delegation (Phase 2 installer) and version tracking
# ---------------------------------------------------------------------------

# current_installed_version queries the primary unit's currently-installed
# binary via its self-identifying --version contract (Phase 1). Returns
# non-zero with no output when there is nothing installed yet to version;
# this is not an error, it just means there is no rollback target. Bounded
# by OPENBRAIN_DS_VERSION_CHECK_TIMEOUT so a hung or misbehaving previous
# binary cannot hang a cutover or rollback indefinitely.
current_installed_version() {
  local install_dir="$1"
  local binary="${install_dir}/${PRIMARY_UNIT}"

  if [[ ! -x "$binary" ]]; then
    return 1
  fi

  local got
  if ! got="$(timeout "$OPENBRAIN_DS_VERSION_CHECK_TIMEOUT" "$binary" --version 2>&1)"; then
    return 1
  fi
  printf '%s' "$got" | tr -d '[:space:]'
  return 0
}

# install_version delegates to the Phase 2 installer
# (scripts/install-release.sh) for the download/checksum-verify/
# version-verify/atomic-install pipeline, rather than reimplementing it.
# Tests override OPENBRAIN_DS_INSTALL_SCRIPT with a fixture stub so this
# file's tests stay scoped to repoint/rollback/health-check behavior, not
# the installer's own (already-covered) download logic.
install_version() {
  local repo="$1" install_dir="$2" version="$3" install_script="$4"
  OPENBRAIN_REPO="$repo" OPENBRAIN_INSTALL_DIR="$install_dir" "$install_script" "$version"
}

# ---------------------------------------------------------------------------
# dry-run
# ---------------------------------------------------------------------------

# dry_run_plan prints exactly what apply/rollback would do: every unit's
# drop-in path and rendered contents, and the exact systemctl/curl command
# sequence, all without writing a file, invoking systemctl/curl, or
# performing a privilege check.
dry_run_plan() {
  local systemd_dir="$1" install_dir="$2" version="$3"
  local unit path content

  log_info "DRY RUN: no file will be written, no systemctl or curl command will be run, no privilege check performed"
  log_info "target version: ${version:-latest (resolved at apply time)}"

  for unit in "${SERVICE_UNITS[@]}"; do
    path="$(drop_in_path "$systemd_dir" "$unit")"
    content="$(drop_in_content "${install_dir}/${unit}")"
    log_info "would write ${path}:"
    printf '%s\n' "$content"
  done

  log_info "would run: ${OPENBRAIN_DS_SYSTEMCTL} daemon-reload"
  log_info "would run: ${OPENBRAIN_DS_SYSTEMCTL} restart ${PRIMARY_UNIT}.service"
  log_info "would run, only for a secondary unit already active (dry-run does not query live state; an inactive unit is left inactive):"
  for unit in "${SECONDARY_UNITS[@]}"; do
    log_info "  ${OPENBRAIN_DS_SYSTEMCTL} restart ${unit}.service"
  done
  log_info "would then run the two-tier health check for ${PRIMARY_UNIT}: local ${OPENBRAIN_DS_HEALTH_LOCAL_URL}, remote ${OPENBRAIN_DS_HEALTH_REMOTE_URL}"
  log_info "on a genuine health-check failure, apply would automatically reinstall and restart whichever version was active before this run, then re-check once more"

  return 0
}

# ---------------------------------------------------------------------------
# Cutover, apply, rollback
# ---------------------------------------------------------------------------

# do_cutover is the write-drop-ins -> daemon-reload -> restart ->
# health-check sequence shared by apply's initial cutover, apply's single
# rollback attempt, and rollback's on-demand cutover. VERSION_FOR_LOG is
# used only in log/error messages so every failure names which version was
# being cut over to. Returns distinct codes per failed stage (4 to 7),
# matching this file's header comment.
do_cutover() {
  local systemd_dir="$1" install_dir="$2" systemctl_bin="$3" sudo_bin="$4" local_url="$5" remote_url="$6" version_for_log="$7"
  local repoint_status=0

  repoint_and_restart "$systemd_dir" "$install_dir" "$systemctl_bin" "$sudo_bin" || repoint_status=$?
  if [[ "$repoint_status" -ne 0 ]]; then
    log_error cutover "cutover FAILED at repoint/restart for version '${version_for_log}' (see the error above for the failing stage)"
    return "$repoint_status"
  fi

  local health_status=0
  run_health_check "$local_url" "$remote_url" || health_status=$?
  if [[ "$health_status" -eq "$HEALTH_OK" || "$health_status" -eq "$HEALTH_REMOTE_UNREACHABLE" ]]; then
    log_info "cutover: version '${version_for_log}' is active and healthy (health_status=${health_status})"
    return 0
  fi

  log_error cutover "cutover FAILED at health check for version '${version_for_log}'"
  return 7
}

# cmd_dry_run prints the plan for VERSION (or latest) and performs no
# privilege check, matching the "writes nothing, runs nothing" contract.
cmd_dry_run() {
  local version="${1:-}"
  dry_run_plan "$OPENBRAIN_DS_SYSTEMD_DIR" "$OPENBRAIN_INSTALL_DIR" "$version"
  return 0
}

# cmd_apply installs VERSION (or latest), repoints/restarts, and
# health-checks. On a genuine health-check failure it automatically rolls
# back to whatever version was installed immediately before this call
# (captured BEFORE install_version overwrites it), then reports a loud,
# actionable error if the rollback itself also fails, rather than looping.
# At most one cutover attempt and at most one automatic rollback attempt.
cmd_apply() {
  local version="${1:-}"

  if ! check_privilege "$OPENBRAIN_DS_SUDO" "$OPENBRAIN_DS_EUID"; then
    return 2
  fi

  local previous_version=""
  previous_version="$(current_installed_version "$OPENBRAIN_INSTALL_DIR")" || previous_version=""
  log_info "apply: installing version '${version:-latest}' (previously installed: '${previous_version:-none}')"

  if ! install_version "$OPENBRAIN_REPO" "$OPENBRAIN_INSTALL_DIR" "$version" "$OPENBRAIN_DS_INSTALL_SCRIPT"; then
    log_error apply "failed to install version '${version:-latest}'; nothing repointed, units left untouched"
    return 3
  fi

  local cutover_status=0
  do_cutover "$OPENBRAIN_DS_SYSTEMD_DIR" "$OPENBRAIN_INSTALL_DIR" "$OPENBRAIN_DS_SYSTEMCTL" "$OPENBRAIN_DS_SUDO" "$OPENBRAIN_DS_HEALTH_LOCAL_URL" "$OPENBRAIN_DS_HEALTH_REMOTE_URL" "${version:-latest}" || cutover_status=$?
  if [[ "$cutover_status" -eq 0 ]]; then
    log_info "apply: version '${version:-latest}' is live and healthy"
    return 0
  fi

  log_error apply "cutover to '${version:-latest}' failed (see the cutover error above for the failing stage); attempting automatic rollback"

  if [[ -z "$previous_version" ]]; then
    log_error rollback "no previously-installed version was recorded; cannot auto-rollback. Manual intervention required: check 'systemctl status ${PRIMARY_UNIT}' and 'journalctl -u ${PRIMARY_UNIT} -n 100'"
    return 7
  fi

  log_info "apply: rolling back to previously-installed version '${previous_version}'"
  if ! install_version "$OPENBRAIN_REPO" "$OPENBRAIN_INSTALL_DIR" "$previous_version" "$OPENBRAIN_DS_INSTALL_SCRIPT"; then
    log_error rollback "automatic rollback's reinstall of '${previous_version}' itself failed; service state is UNKNOWN, manual intervention required"
    return 8
  fi

  local rollback_cutover_status=0
  do_cutover "$OPENBRAIN_DS_SYSTEMD_DIR" "$OPENBRAIN_INSTALL_DIR" "$OPENBRAIN_DS_SYSTEMCTL" "$OPENBRAIN_DS_SUDO" "$OPENBRAIN_DS_HEALTH_LOCAL_URL" "$OPENBRAIN_DS_HEALTH_REMOTE_URL" "$previous_version" || rollback_cutover_status=$?
  if [[ "$rollback_cutover_status" -ne 0 ]]; then
    log_error rollback "reinstalled '${previous_version}' but its cutover still failed (see the cutover error above for the failing stage); manual intervention required"
    return 9
  fi

  log_info "apply: automatic rollback to '${previous_version}' succeeded and the service is healthy again; version '${version:-latest}' did NOT go live"
  # Intentional: apply still reports failure here even though the
  # rollback itself succeeded. The requested version never became
  # healthy; the service being back on the prior version is a successful
  # recovery, not a successful apply of what the caller asked for.
  return 10
}

# cmd_rollback is the on-demand path: revert to an explicit prior version
# regardless of current health. It reuses do_cutover so the
# repoint/restart/health-check sequence is identical to the automatic
# path. VERSION is required: on-demand rollback never guesses.
cmd_rollback() {
  local version="${1:-}"

  if [[ -z "$version" ]]; then
    log_error usage "rollback requires an explicit prior VERSION (e.g. v0.10.0); on-demand rollback never guesses"
    return 1
  fi

  if ! check_privilege "$OPENBRAIN_DS_SUDO" "$OPENBRAIN_DS_EUID"; then
    return 2
  fi

  log_info "rollback: installing requested version '${version}'"
  if ! install_version "$OPENBRAIN_REPO" "$OPENBRAIN_INSTALL_DIR" "$version" "$OPENBRAIN_DS_INSTALL_SCRIPT"; then
    log_error rollback "failed to install requested version '${version}'; units left untouched"
    return 3
  fi

  local cutover_status=0
  do_cutover "$OPENBRAIN_DS_SYSTEMD_DIR" "$OPENBRAIN_INSTALL_DIR" "$OPENBRAIN_DS_SYSTEMCTL" "$OPENBRAIN_DS_SUDO" "$OPENBRAIN_DS_HEALTH_LOCAL_URL" "$OPENBRAIN_DS_HEALTH_REMOTE_URL" "$version" || cutover_status=$?
  if [[ "$cutover_status" -ne 0 ]]; then
    log_error rollback "version '${version}' installed but its cutover failed (see the cutover error above for the failing stage); manual intervention required (no further automatic rollback attempted)"
    return 7
  fi

  log_info "rollback: version '${version}' is live and healthy"
  return 0
}

# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------

usage() {
  cat <<'EOF'
usage: deploy-system.sh dry-run [VERSION]
       deploy-system.sh apply [VERSION]
       deploy-system.sh rollback VERSION

  dry-run   Print every drop-in path, its exact contents, and the exact
            systemctl/curl commands apply would run. Writes nothing, runs
            nothing, requires no privilege.
  apply     Install VERSION (or the latest release), repoint the four
            units' ExecStart via systemd drop-ins, reload/restart, and
            check health (local + remote MCP initialize). Automatically
            rolls back to the previously-installed version on a genuine
            health-check failure.
  rollback  Install the specified prior VERSION and repoint/restart/check
            health. VERSION is required; on-demand rollback never guesses.

Repoints the four openbrain system units' ExecStart at the Phase 2
installer's target via drop-in overrides; the shipped unit files are never
edited in place. See the header comment in this file for the full
contract.
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
      log_error usage "a command is required"
      usage >&2
      return 1
      ;;
    dry-run)
      cmd_dry_run "${2:-}"
      return $?
      ;;
    apply)
      cmd_apply "${2:-}"
      return $?
      ;;
    rollback)
      cmd_rollback "${2:-}"
      return $?
      ;;
    *)
      log_error usage "unrecognized command '${cmd}'"
      usage >&2
      return 1
      ;;
  esac
}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  main "$@"
  exit $?
fi
