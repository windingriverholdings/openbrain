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
#   2. Config-precedes-start preflight: the system openbrain-web has
#      EnvironmentFile=/etc/openbrain/openbrain.env and will not start until
#      that file exists. This is checked BEFORE anything is torn down, so a
#      missing config never leaves the memory backend dead (relocate the
#      config first with scripts/relocate-config.sh).
#   3. Installs VERSION (or latest) via the Phase 2 installer
#      (scripts/install-release.sh), reused as-is: checksum verification,
#      the self-identifying-binary version check, and the atomic install
#      are the installer's job, not reimplemented here. The binary is
#      ALWAYS installed before any unit is repointed at it (install before
#      activate).
#   4. Installs the four shipped base unit files (deploy/openbrain-*.service)
#      to /etc/systemd/system/ (Gap 2). An ExecStart drop-in has nothing to
#      attach to without a base unit; without this step the drop-in plus a
#      system-scope restart fails "unit not found". Idempotent and atomic,
#      same discipline as the drop-in write. Done BEFORE any --user teardown
#      so a failure here leaves the old --user units still serving.
#   5. Old --user teardown (Gap 1): captures which --user units are active
#      (for rollback), stops and disables the four --user units, and disables
#      linger. This is the LAST step before the system unit starts, so there
#      is never a window with two binders on 127.0.0.1:10203. Every part is
#      idempotent: an already-stopped --user unit and already-disabled linger
#      are no-ops, not errors.
#   6. Writes an ExecStart-only systemd drop-in for EACH of the four units
#      (<unit>.service.d/override.conf under /etc/systemd/system/). The
#      shipped unit files are NEVER edited in place. Every other directive
#      (EnvironmentFile, ReadWritePaths, ProtectSystem, User, etc.) is
#      untouched by construction: the drop-in's rendered content is ONLY a
#      bare `ExecStart=` reset (systemd APPENDS ExecStart= across drop-ins
#      unless the list is cleared first) followed by the new `ExecStart=`
#      line.
#   7. `systemctl daemon-reload` once, then restarts openbrain-web (the
#      primary unit the health check depends on) unconditionally, and
#      restarts each of the other three units ONLY if it was already
#      active: an inactive secondary stays inactive, matching today's
#      operational reality (only openbrain-web is normally up).
#   8. Runs the two-tier health check: local http://127.0.0.1:10203/health,
#      then, only if local passes, the remote https://openbrain.wr-s.net/mcp
#      `initialize` POST. A remote connection failure (unreachable, not a
#      genuine HTTP failure) is treated as "cannot confirm, not a rollback
#      trigger" per the boundary-of-the-boundary discipline: a cloudflared
#      outage is not the same defect class as a broken openbrain-web.
#   9. On success, enables the system unit(s) for boot (the counterpart to
#      disabling linger in step 5): the primary always, plus any secondary
#      that was active under --user. Doing this only after a healthy cutover,
#      and only after the --user linger boot mechanism was disabled, means
#      the box never has two boot mechanisms binding the same port.
#  10. On a genuine health-check failure the recovery depends on whether this
#      run tore down a live --user openbrain-web:
#        - FIRST cutover (a --user openbrain-web was active): restores the
#          --user path (stops+disables the system units to free the port,
#          re-enables linger, re-enables and starts the previously-active
#          --user units) so the memory backend keeps serving on the old
#          model. Fail toward "the old thing still serves."
#        - STEADY-STATE repoint (no --user unit was active): reinstalls
#          whichever version was active before this apply and repoints/
#          restarts/re-checks health exactly once more, same as before.
#      At most one cutover attempt and at most one automatic recovery attempt
#      per apply; this tool never restart-loops.
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
#  11  config-precedes-start preflight failed: the EnvironmentFile
#      (/etc/openbrain/openbrain.env) does not exist. Nothing was torn down
#      or changed; relocate the config first (scripts/relocate-config.sh)
#  12  base system unit install failed (apply path): nothing was torn down,
#      the --user units are left serving
#  13  old --user teardown or linger-disable failed (apply path): a
#      best-effort restore of any --user unit stopped this run is attempted
#      before returning
#  14  FIRST-cutover recovery: the system cutover failed AND restoring the
#      --user path also failed. The memory backend may be DOWN; manual
#      intervention required
#  15  FIRST-cutover recovery SUCCEEDED: the system cutover failed, but the
#      previously-active --user path was restored and is serving again. The
#      requested system cutover did NOT go live
#  16  system cutover is live and healthy, but `systemctl enable` for boot
#      persistence failed. The service runs now but will NOT start after a
#      reboot until it is enabled by hand. The memory backend is serving; the
#      failure is boot-persistence only
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
#   OPENBRAIN_DS_ENV_FILE            EnvironmentFile the system units read,
#                                     default /etc/openbrain/openbrain.env
#   OPENBRAIN_DS_DEPLOY_DIR          Source of the four base unit files,
#                                     default deploy/ next to this script
#   OPENBRAIN_DS_USER_SYSTEMD_DIR    Where the old --user unit files live,
#                                     default ~/.config/systemd/user
#   OPENBRAIN_DS_LOGINCTL            loginctl binary, default loginctl
#   OPENBRAIN_DS_LINGER_USER         User whose linger is disabled on cutover
#                                     (re-enabled on rollback), default the
#                                     invoking user
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

# The secret-bearing EnvironmentFile the system units read. The system
# openbrain-web must NOT be started until this file exists (Config precedes
# start): a system unit whose EnvironmentFile is absent fails to start, and
# starting it after we have already torn down the --user unit would leave
# the memory backend dead. cmd_apply/cmd_rollback fail closed before any
# teardown when this file is missing. relocate-config.sh is what populates
# it; this tool only checks presence, never reads or writes it.
OPENBRAIN_DS_ENV_FILE="${OPENBRAIN_DS_ENV_FILE:-/etc/openbrain/openbrain.env}"
# Source of the four shipped base unit files installed to the system
# systemd dir (Gap 2: the ExecStart drop-in needs a base unit to attach
# to). Defaults to the repo's deploy/ next to this script.
OPENBRAIN_DS_DEPLOY_DIR="${OPENBRAIN_DS_DEPLOY_DIR:-${SCRIPT_DIR}/../deploy}"
# Where the OLD systemd --user unit files live. Used only to detect whether
# a --user unit is present to tear down (Gap 1): the teardown is a no-op for
# a unit that was never installed as a --user service.
OPENBRAIN_DS_USER_SYSTEMD_DIR="${OPENBRAIN_DS_USER_SYSTEMD_DIR:-${HOME}/.config/systemd/user}"
# loginctl binary and the linger user, for disabling (cutover) and
# re-enabling (rollback) linger. System units start at boot by being
# enabled, so the old --user linger boot mechanism is disabled as part of
# the cutover.
OPENBRAIN_DS_LOGINCTL="${OPENBRAIN_DS_LOGINCTL:-loginctl}"
# Guarded against a restricted PATH: this default is evaluated at SOURCE
# TIME (every test that sources this file pays this cost, not just a
# privileged production run), and under `set -euo pipefail` a bare
# `$(id -un)` with `id` absent from PATH would abort sourcing entirely with
# exit 127, before any function (including check_privilege) ever runs. The
# `|| printf` fallback guarantees a zero exit status for the substitution
# itself regardless of whether `id` is reachable, so a PATH-starved test
# fixture (see tests/deploy-system.bats' "no privilege and no sudo on PATH"
# cases) still sources cleanly.
OPENBRAIN_DS_LINGER_USER="${OPENBRAIN_DS_LINGER_USER:-$(id -un 2>/dev/null || printf '%s' "${USER:-craig8}")}"

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
# Config-precedes-start preflight
# ---------------------------------------------------------------------------

# check_env_file_present fails closed when the system units' EnvironmentFile
# does not exist. It is checked BEFORE any --user teardown so a missing
# config never leaves the memory backend dead: a system openbrain-web whose
# EnvironmentFile is absent will not start, and starting it after the --user
# unit has already been stopped would strand the backend. This tool only
# checks presence; relocate-config.sh is what actually populates the file.
check_env_file_present() {
  local env_file="$1"
  if [[ ! -f "$env_file" ]]; then
    log_error preflight "the system units' EnvironmentFile does not exist: ${env_file}. Relocate the config first (scripts/relocate-config.sh), then re-run. Nothing was changed."
    return 1
  fi
  return 0
}

# ---------------------------------------------------------------------------
# Base system unit install (Gap 2)
# ---------------------------------------------------------------------------

# install_base_unit copies deploy/<unit>.service into the system systemd dir
# so the ExecStart drop-in has a base unit to attach to. Idempotent (a
# byte-identical target is left untouched, no mtime change) and atomic (temp
# file in the destination dir, then a single rename), same discipline as
# write_drop_in. Guarded explicitly throughout: invoked as
# `install_base_unit ... || return 1` from install_base_units, which suspends
# errexit for its whole body, so an unguarded copy/rename failure here would
# otherwise be swallowed and the caller would proceed as if the base unit
# were in place when it is not.
install_base_unit() {
  local systemd_dir="$1" deploy_dir="$2" unit="$3" use_sudo_flag="$4" sudo_bin="$5"
  local -a priv=()
  [[ "$use_sudo_flag" == "1" ]] && priv=("$sudo_bin")
  local src="${deploy_dir}/${unit}.service"
  local dst="${systemd_dir}/${unit}.service"

  if [[ ! -f "$src" ]]; then
    log_error baseunit "shipped unit file not found: ${src}"
    return 1
  fi

  if [[ -f "$dst" ]] && cmp -s "$src" "$dst"; then
    log_info "base unit for ${unit} already matches the shipped file (idempotent, no write): ${dst}"
    return 0
  fi

  if ! "${priv[@]}" mkdir -p "$systemd_dir" 2>&1; then
    log_error baseunit "failed to create ${systemd_dir} for ${unit}"
    return 1
  fi

  local tmp_path
  if ! tmp_path="$("${priv[@]}" mktemp "${systemd_dir}/.${unit}.service.XXXXXX" 2>&1)"; then
    log_error baseunit "failed to create a temp file in ${systemd_dir} for ${unit}"
    return 1
  fi

  if ! "${priv[@]}" tee "$tmp_path" < "$src" >/dev/null 2>&1; then
    log_error baseunit "failed to stage ${src} into ${tmp_path} for ${unit}"
    "${priv[@]}" rm -f "$tmp_path" 2>/dev/null || true
    return 1
  fi

  if ! "${priv[@]}" chmod 0644 "$tmp_path" 2>&1; then
    log_error baseunit "failed to set mode 0644 on ${tmp_path} for ${unit}"
    "${priv[@]}" rm -f "$tmp_path" 2>/dev/null || true
    return 1
  fi

  if ! "${priv[@]}" mv -f -- "$tmp_path" "$dst" 2>&1; then
    log_error baseunit "atomic rename of ${tmp_path} to ${dst} failed for ${unit}"
    "${priv[@]}" rm -f "$tmp_path" 2>/dev/null || true
    return 1
  fi

  log_info "installed base unit for ${unit}: ${dst}"
  return 0
}

# install_base_units installs all four base unit files. Returns non-zero on
# the first failure; the caller maps that to exit code 12 and, crucially,
# has torn nothing down yet, so the --user units are left serving.
install_base_units() {
  local systemd_dir="$1" deploy_dir="$2" use_sudo_flag="$3" sudo_bin="$4"
  local unit
  for unit in "${SERVICE_UNITS[@]}"; do
    if ! install_base_unit "$systemd_dir" "$deploy_dir" "$unit" "$use_sudo_flag" "$sudo_bin"; then
      return 1
    fi
  done
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
        # A distinct message from the primary-unit restart failure above:
        # restart_unit already logged the bare "restart failed for
        # ${unit}"; this adds the "already-active" context so the two
        # failure sites are distinguishable in the output, matching
        # OB-063's retired repoint-unit.sh convention.
        log_error dropin "restart failed for already-active ${unit}"
        return 6
      fi
    fi
  done

  return 0
}

# ---------------------------------------------------------------------------
# Old --user teardown, linger, and system-unit enable/disable (Gap 1)
# ---------------------------------------------------------------------------
#
# The --user operations run as the invoking user against that user's own
# systemd bus and are therefore NEVER prefixed with sudo (sudo would target
# root's user bus, not the operator's). This matches the migration sequence:
# the operator runs this tool as the run-as user (craig8), and the tool
# self-escalates with sudo only for the SYSTEM-scope operations. loginctl
# linger changes DO need sudo (they touch /var/lib/systemd/linger).

# user_unit_present reports whether a --user unit file exists to tear down.
# The teardown is a no-op for a unit that was never installed as a --user
# service, which is what keeps the whole step idempotent.
user_unit_present() {
  local user_systemd_dir="$1" unit="$2"
  [[ -f "${user_systemd_dir}/${unit}.service" ]]
}

# user_unit_is_active reports the --user unit's active state via exit code.
# A nonzero from `systemctl --user is-active` is expected state information,
# not a failure to guard against; it is only ever called as an if-condition.
user_unit_is_active() {
  local systemctl_bin="$1" unit="$2"
  "$systemctl_bin" --user is-active --quiet "${unit}.service"
}

# teardown_user_unit stops and disables one --user unit, but only if its
# --user unit file is present (idempotent: nothing to tear down otherwise).
# `disable --now` on an already-inactive, already-disabled unit is a no-op
# success, so a second run converges. Guarded explicitly: called under
# `teardown_user_units ... || return 13`, which suspends errexit.
teardown_user_unit() {
  local systemctl_bin="$1" user_systemd_dir="$2" unit="$3"

  if ! user_unit_present "$user_systemd_dir" "$unit"; then
    return 0
  fi

  if ! "$systemctl_bin" --user disable --now "${unit}.service" 2>&1; then
    log_error teardown "failed to stop and disable the --user ${unit}"
    return 1
  fi
  log_info "stopped and disabled the --user ${unit}"
  return 0
}

# teardown_user_units tears down all four --user units. Returns non-zero on
# the first failure (caller maps to exit 13).
teardown_user_units() {
  local systemctl_bin="$1" user_systemd_dir="$2"
  local unit
  for unit in "${SERVICE_UNITS[@]}"; do
    if ! teardown_user_unit "$systemctl_bin" "$user_systemd_dir" "$unit"; then
      return 1
    fi
  done
  return 0
}

# linger_enabled reports (via exit code) whether the user currently has
# linger enabled. A failing `loginctl show-user` (no such user record, or
# loginctl unavailable) is read as "not lingering", so disable_linger
# no-ops rather than erroring in that case.
linger_enabled() {
  local loginctl_bin="$1" linger_user="$2"
  "$loginctl_bin" show-user "$linger_user" 2>/dev/null | grep -q '^Linger=yes'
}

# disable_linger disables linger for the user, but only if it is currently
# enabled (idempotent). Uses sudo for the system-scope loginctl change.
# Guarded explicitly (called under `... || return 13`).
disable_linger() {
  local loginctl_bin="$1" linger_user="$2" use_sudo_flag="$3" sudo_bin="$4"
  local -a priv=()
  [[ "$use_sudo_flag" == "1" ]] && priv=("$sudo_bin")

  if ! linger_enabled "$loginctl_bin" "$linger_user"; then
    log_info "linger for ${linger_user} is not enabled (idempotent, no change)"
    return 0
  fi

  if ! "${priv[@]}" "$loginctl_bin" disable-linger "$linger_user" 2>&1; then
    log_error teardown "failed to disable linger for ${linger_user}"
    return 1
  fi
  log_info "disabled linger for ${linger_user}"
  return 0
}

# enable_system_unit enables one system unit for boot (idempotent: enabling
# an already-enabled unit is a no-op success). This is the boot-start
# counterpart to disabling --user linger and runs only after a healthy
# cutover, so the box never has both boot mechanisms binding the port.
# Guarded explicitly (called under `enable_system_boot ... || ...`).
enable_system_unit() {
  local systemctl_bin="$1" unit="$2" use_sudo_flag="$3" sudo_bin="$4"
  local -a priv=()
  [[ "$use_sudo_flag" == "1" ]] && priv=("$sudo_bin")

  if ! "${priv[@]}" "$systemctl_bin" enable "${unit}.service" 2>&1; then
    log_error enable "failed to enable ${unit} for boot"
    return 1
  fi
  return 0
}

# enable_system_boot enables the primary unit plus each secondary named in
# the extra arguments (the units that were active under --user). Returns
# non-zero if any enable fails.
enable_system_boot() {
  local systemctl_bin="$1" use_sudo_flag="$2" sudo_bin="$3"
  shift 3
  local -a extra_units=("$@")
  local unit

  if ! enable_system_unit "$systemctl_bin" "$PRIMARY_UNIT" "$use_sudo_flag" "$sudo_bin"; then
    return 1
  fi
  for unit in "${extra_units[@]}"; do
    [[ "$unit" == "$PRIMARY_UNIT" ]] && continue
    if ! enable_system_unit "$systemctl_bin" "$unit" "$use_sudo_flag" "$sudo_bin"; then
      return 1
    fi
  done
  return 0
}

# stop_disable_system_unit stops and disables one system unit, used on
# first-cutover recovery to free 127.0.0.1:10203 before the --user unit is
# restarted. `disable --now` on an inactive/not-enabled unit is a no-op
# success. Best-effort: a failure is logged but recovery continues, since
# the whole point is to give the old path a chance to bind the port.
stop_disable_system_unit() {
  local systemctl_bin="$1" unit="$2" use_sudo_flag="$3" sudo_bin="$4"
  local -a priv=()
  [[ "$use_sudo_flag" == "1" ]] && priv=("$sudo_bin")

  if ! "${priv[@]}" "$systemctl_bin" disable --now "${unit}.service" 2>&1; then
    log_error recovery "could not stop/disable the system ${unit} while freeing the port (continuing recovery)"
    return 1
  fi
  return 0
}

# enable_linger re-enables linger for the user (first-cutover recovery), so
# the restored --user units start at boot again. Guarded explicitly.
enable_linger() {
  local loginctl_bin="$1" linger_user="$2" use_sudo_flag="$3" sudo_bin="$4"
  local -a priv=()
  [[ "$use_sudo_flag" == "1" ]] && priv=("$sudo_bin")

  if ! "${priv[@]}" "$loginctl_bin" enable-linger "$linger_user" 2>&1; then
    log_error recovery "could not re-enable linger for ${linger_user} during --user restore"
    return 1
  fi
  return 0
}

# restore_user_unit re-enables and starts one --user unit (first-cutover
# recovery). No sudo (user bus). Guarded explicitly.
restore_user_unit() {
  local systemctl_bin="$1" unit="$2"
  if ! "$systemctl_bin" --user enable --now "${unit}.service" 2>&1; then
    log_error recovery "could not re-enable and start the --user ${unit} during restore"
    return 1
  fi
  log_info "restored the --user ${unit}"
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

  log_info "DRY RUN: no file will be written, no systemctl, loginctl, or curl command will be run, no privilege check performed"
  log_info "target version: ${version:-latest (resolved at apply time)}"

  log_info "would first verify the EnvironmentFile exists (config precedes start): ${OPENBRAIN_DS_ENV_FILE}"
  log_info "would then install the base system units from ${OPENBRAIN_DS_DEPLOY_DIR} into ${systemd_dir} (Gap 2: the drop-in needs a base unit to attach to):"
  for unit in "${SERVICE_UNITS[@]}"; do
    log_info "  ${OPENBRAIN_DS_DEPLOY_DIR}/${unit}.service -> ${systemd_dir}/${unit}.service (mode 0644, idempotent)"
  done

  log_info "would then tear down the old --user units (Gap 1), for each --user unit present in ${OPENBRAIN_DS_USER_SYSTEMD_DIR} (dry-run does not query live state):"
  for unit in "${SERVICE_UNITS[@]}"; do
    log_info "  ${OPENBRAIN_DS_SYSTEMCTL} --user disable --now ${unit}.service"
  done
  log_info "would then disable linger, only if currently enabled: ${OPENBRAIN_DS_LOGINCTL} disable-linger ${OPENBRAIN_DS_LINGER_USER}"

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
  log_info "on success, would enable the system unit(s) for boot (counterpart to disabling linger): ${OPENBRAIN_DS_SYSTEMCTL} enable ${PRIMARY_UNIT}.service (plus any secondary that was active under --user)"
  log_info "on a genuine health-check failure after a FIRST cutover (a --user openbrain-web was active), apply would restore the --user path (stop/disable the system units, re-enable linger, re-enable and start the previously-active --user units) so the memory backend keeps serving"
  log_info "on a genuine health-check failure during a STEADY-STATE repoint (no --user unit was active), apply would automatically reinstall and restart whichever version was active before this run, then re-check once more"

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

# restore_user_path is the FIRST-cutover recovery: the system cutover failed,
# and this run had torn down a live --user openbrain-web, so nothing is
# serving the memory backend. It frees 127.0.0.1:10203 by stopping and
# disabling the system units, re-enables linger, re-enables and starts the
# --user units that were active before teardown, and confirms the local
# health endpoint answers again. Returns 0 when the --user path is serving
# again, non-zero when it is not (the memory backend may be down).
#
# The system-unit free-the-port step is best-effort (a failure there is
# logged but does not abort recovery): the goal is to give the old --user
# path a chance to bind the port, and the health check at the end is the
# authoritative signal of whether the backend recovered.
restore_user_path() {
  local systemctl_bin="$1" sudo_bin="$2" use_sudo_flag="$3" loginctl_bin="$4" linger_user="$5" local_url="$6"
  shift 6
  local -a active_user_units=("$@")
  local unit

  for unit in "${SERVICE_UNITS[@]}"; do
    stop_disable_system_unit "$systemctl_bin" "$unit" "$use_sudo_flag" "$sudo_bin" || true
  done

  if ! enable_linger "$loginctl_bin" "$linger_user" "$use_sudo_flag" "$sudo_bin"; then
    log_error recovery "re-enabling linger failed during --user restore (continuing to restart the --user units regardless)"
  fi

  for unit in "${active_user_units[@]}"; do
    restore_user_unit "$systemctl_bin" "$unit" || true
  done

  if health_check_local "$local_url"; then
    log_info "recovery: the --user path is serving again (local health check passed): ${local_url}"
    return 0
  fi

  log_error recovery "the --user path did NOT come back healthy after restore: ${local_url}"
  return 1
}

# cmd_apply is the cutover: config preflight, install the binary, install the
# base system units (Gap 2), tear down the old --user units and disable linger
# (Gap 1), repoint/restart, health-check, and enable for boot on success. On a
# genuine health-check failure the recovery branch depends on whether this run
# tore down a live --user openbrain-web (FIRST cutover, restore the --user
# path) or not (STEADY-STATE repoint, roll back the binary version). At most
# one cutover attempt and at most one automatic recovery attempt.
cmd_apply() {
  local version="${1:-}"

  if ! check_privilege "$OPENBRAIN_DS_SUDO" "$OPENBRAIN_DS_EUID"; then
    return 2
  fi

  local use_sudo_flag
  use_sudo_flag="$(use_sudo)"

  # Config precedes start: refuse before anything is torn down.
  if ! check_env_file_present "$OPENBRAIN_DS_ENV_FILE"; then
    return 11
  fi

  local previous_version=""
  previous_version="$(current_installed_version "$OPENBRAIN_INSTALL_DIR")" || previous_version=""
  log_info "apply: installing version '${version:-latest}' (previously installed: '${previous_version:-none}')"

  if ! install_version "$OPENBRAIN_REPO" "$OPENBRAIN_INSTALL_DIR" "$version" "$OPENBRAIN_DS_INSTALL_SCRIPT"; then
    log_error apply "failed to install version '${version:-latest}'; nothing repointed, units left untouched"
    return 3
  fi

  # Gap 2: the ExecStart drop-in needs a base unit at
  # /etc/systemd/system/openbrain-<svc>.service to attach to. Done BEFORE any
  # teardown so a failure here leaves the --user units still serving.
  if ! install_base_units "$OPENBRAIN_DS_SYSTEMD_DIR" "$OPENBRAIN_DS_DEPLOY_DIR" "$use_sudo_flag" "$OPENBRAIN_DS_SUDO"; then
    log_error apply "failed to install the base system units; nothing torn down, the --user units are left serving"
    return 12
  fi

  # Gap 1: capture which --user units are active (for a possible restore),
  # then tear them down and disable linger. This is the last step before the
  # system unit starts, so there is never a window with two binders on the
  # port.
  local -a active_user_units=()
  local user_web_was_active=0
  local u
  for u in "${SERVICE_UNITS[@]}"; do
    if user_unit_present "$OPENBRAIN_DS_USER_SYSTEMD_DIR" "$u" \
      && user_unit_is_active "$OPENBRAIN_DS_SYSTEMCTL" "$u"; then
      active_user_units+=("$u")
      [[ "$u" == "$PRIMARY_UNIT" ]] && user_web_was_active=1
    fi
  done

  if ! teardown_user_units "$OPENBRAIN_DS_SYSTEMCTL" "$OPENBRAIN_DS_USER_SYSTEMD_DIR"; then
    log_error apply "tearing down the old --user units failed; attempting to restart any --user unit stopped this run so the backend keeps serving"
    for u in "${active_user_units[@]}"; do
      restore_user_unit "$OPENBRAIN_DS_SYSTEMCTL" "$u" || true
    done
    return 13
  fi

  if ! disable_linger "$OPENBRAIN_DS_LOGINCTL" "$OPENBRAIN_DS_LINGER_USER" "$use_sudo_flag" "$OPENBRAIN_DS_SUDO"; then
    log_error apply "disabling linger failed after tearing down the --user units; restoring the --user path so the backend keeps serving"
    restore_user_path "$OPENBRAIN_DS_SYSTEMCTL" "$OPENBRAIN_DS_SUDO" "$use_sudo_flag" "$OPENBRAIN_DS_LOGINCTL" "$OPENBRAIN_DS_LINGER_USER" "$OPENBRAIN_DS_HEALTH_LOCAL_URL" "${active_user_units[@]}" || true
    return 13
  fi

  local cutover_status=0
  do_cutover "$OPENBRAIN_DS_SYSTEMD_DIR" "$OPENBRAIN_INSTALL_DIR" "$OPENBRAIN_DS_SYSTEMCTL" "$OPENBRAIN_DS_SUDO" "$OPENBRAIN_DS_HEALTH_LOCAL_URL" "$OPENBRAIN_DS_HEALTH_REMOTE_URL" "${version:-latest}" || cutover_status=$?
  if [[ "$cutover_status" -eq 0 ]]; then
    log_info "apply: version '${version:-latest}' is live and healthy"
    # Enable for boot: the counterpart to disabling linger. Only after a
    # healthy cutover and only after linger was disabled, so the box never
    # has two boot mechanisms binding the same port. A failure here leaves a
    # RUNNING, healthy service that will not survive a reboot until enabled by
    # hand: surfaced distinctly (16), never treated as a silent success.
    if ! enable_system_boot "$OPENBRAIN_DS_SYSTEMCTL" "$use_sudo_flag" "$OPENBRAIN_DS_SUDO" "${active_user_units[@]}"; then
      log_error apply "version '${version:-latest}' is live and healthy, but enabling it for boot failed; it will NOT start after a reboot until you run: sudo systemctl enable ${PRIMARY_UNIT}.service"
      return 16
    fi
    return 0
  fi

  # Recovery. FIRST cutover (a live --user openbrain-web was torn down):
  # restore the --user path so the memory backend keeps serving. STEADY-STATE
  # repoint (no --user unit was active): roll back the binary version.
  if [[ "$user_web_was_active" -eq 1 ]]; then
    log_error apply "cutover to '${version:-latest}' failed after tearing down the live --user openbrain-web; restoring the --user path so the memory backend keeps serving"
    if restore_user_path "$OPENBRAIN_DS_SYSTEMCTL" "$OPENBRAIN_DS_SUDO" "$use_sudo_flag" "$OPENBRAIN_DS_LOGINCTL" "$OPENBRAIN_DS_LINGER_USER" "$OPENBRAIN_DS_HEALTH_LOCAL_URL" "${active_user_units[@]}"; then
      log_info "apply: restored the --user path; the memory backend is serving again on the old model; the system cutover to '${version:-latest}' did NOT go live"
      return 15
    fi
    log_error apply "the system cutover failed AND restoring the --user path also failed; the memory backend may be DOWN. Manual intervention required: check 'systemctl --user status ${PRIMARY_UNIT}' and 'systemctl status ${PRIMARY_UNIT}'"
    return 14
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
# regardless of current health. This is a STEADY-STATE, system-model
# operation (the --user units are already gone once the cutover has run), so
# it does NOT tear down --user units or touch linger; it only reverts the
# installed version. It still enforces config-precedes-start and ensures the
# base units are present, because do_cutover writes a drop-in that must
# attach to a base unit and restarts a unit that must have its EnvironmentFile.
# VERSION is required: on-demand rollback never guesses.
cmd_rollback() {
  local version="${1:-}"

  if [[ -z "$version" ]]; then
    log_error usage "rollback requires an explicit prior VERSION (e.g. v0.10.0); on-demand rollback never guesses"
    return 1
  fi

  if ! check_privilege "$OPENBRAIN_DS_SUDO" "$OPENBRAIN_DS_EUID"; then
    return 2
  fi

  local use_sudo_flag
  use_sudo_flag="$(use_sudo)"

  if ! check_env_file_present "$OPENBRAIN_DS_ENV_FILE"; then
    return 11
  fi

  log_info "rollback: installing requested version '${version}'"
  if ! install_version "$OPENBRAIN_REPO" "$OPENBRAIN_INSTALL_DIR" "$version" "$OPENBRAIN_DS_INSTALL_SCRIPT"; then
    log_error rollback "failed to install requested version '${version}'; units left untouched"
    return 3
  fi

  if ! install_base_units "$OPENBRAIN_DS_SYSTEMD_DIR" "$OPENBRAIN_DS_DEPLOY_DIR" "$use_sudo_flag" "$OPENBRAIN_DS_SUDO"; then
    log_error rollback "failed to install the base system units; units left untouched"
    return 12
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

  dry-run   Print the config preflight, the base-unit install, the --user
            teardown and linger-disable, every drop-in path and its exact
            contents, and the exact systemctl/loginctl/curl commands apply
            would run. Writes nothing, runs nothing, requires no privilege.
  apply     Verify the EnvironmentFile exists (config precedes start),
            install VERSION (or the latest release), install the four base
            system units, tear down the old --user units and disable linger,
            repoint each unit's ExecStart via a systemd drop-in,
            reload/restart, check health (local + remote MCP initialize), and
            enable for boot. On a genuine health-check failure it restores the
            old --user path (first cutover) or rolls back the binary version
            (steady-state repoint).
  rollback  Install the specified prior VERSION and repoint/restart/check
            health on the system model. VERSION is required; on-demand
            rollback never guesses.

Converts the four openbrain services from systemd --user units to system
units, relocating the ExecStart at the Phase 2 installer's target via drop-in
overrides; the shipped unit files are installed as the base units and never
edited in place. See the header comment in this file for the full contract.
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
