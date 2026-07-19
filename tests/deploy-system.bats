#!/usr/bin/env bats
# Unit tests for scripts/deploy-system.sh (OB-066, Phase 3.5).
#
# ABSOLUTE SAFETY: every test in this file mocks systemctl and curl and
# forces OPENBRAIN_DS_EUID to a non-zero value with a fake `sudo` stub on
# PATH (or omits sudo entirely to exercise the fail-closed path). No test
# invokes the real systemd, makes a real network call, or writes into the
# real /etc/systemd/system or /usr/local/bin: every path used is a
# per-test scratch directory.
#
# Every function call goes through `run bash -c "source '$SCRIPT'; ..."` so
# each test gets a fresh subshell: the script's own `set -euo pipefail`
# never bleeds into bats' own control flow.

setup() {
  SCRIPT="${BATS_TEST_DIRNAME}/../scripts/deploy-system.sh"
  WORK_DIR="$(mktemp -d)"
  SYSTEMD_DIR="${WORK_DIR}/systemd-system"
  INSTALL_DIR="${WORK_DIR}/install"
  mkdir -p "$SYSTEMD_DIR" "$INSTALL_DIR"
}

teardown() {
  rm -rf "$WORK_DIR"
}

# --- fixture helpers ---------------------------------------------------

write_fake_binary() {
  local path="$1" version="$2"
  cat > "$path" <<EOF
#!/usr/bin/env bash
if [[ "\${1:-}" == "--version" ]]; then
  echo "${version}"
  exit 0
fi
echo "unsupported invocation" >&2
exit 1
EOF
  chmod +x "$path"
}

# write_fake_sudo writes a `sudo` stub that just execs its arguments
# directly (no real privilege escalation, and no-op if not found: bats
# tests never run as an actual privileged user).
write_fake_sudo() {
  local dir="$1"
  cat > "${dir}/sudo" <<'EOF'
#!/usr/bin/env bash
exec "$@"
EOF
  chmod +x "${dir}/sudo"
}

# write_fake_systemctl writes a systemctl stub (NO --user prefix: this is
# the system-unit tool) that logs every invocation to log_file and reports
# each unit's active/inactive state from FAKE_ACTIVE_<unit> env vars.
write_fake_systemctl() {
  local dir="$1" log_file="$2"
  cat > "${dir}/systemctl" <<EOF
#!/usr/bin/env bash
echo "\$*" >> "${log_file}"
case "\${1:-}" in
  daemon-reload)
    exit "\${FAKE_SYSTEMCTL_RELOAD_EXIT:-0}"
    ;;
  restart)
    unit="\$2"
    varname="FAKE_SYSTEMCTL_RESTART_EXIT_\${unit//[-.]/_}"
    exit "\${!varname:-\${FAKE_SYSTEMCTL_RESTART_EXIT:-0}}"
    ;;
  is-active)
    unit="\${@: -1}"
    varname="FAKE_ACTIVE_\${unit//[-.]/_}"
    state="\${!varname:-inactive}"
    [[ "\$state" == "active" ]] && exit 0
    exit 3
    ;;
  *)
    echo "fake systemctl: unhandled args: \$*" >&2
    exit 99
    ;;
esac
EOF
  chmod +x "${dir}/systemctl"
}

# write_fake_curl: same two-tier mock as OB-063's tests/repoint-unit.bats
# (local vs remote distinguished by presence of -w; walks a plan file of
# outcomes per call).
write_fake_curl() {
  local dir="$1"
  cat > "${dir}/curl" <<'EOF'
#!/usr/bin/env bash
is_remote=0
has_fail_flag=0
for arg in "$@"; do
  [[ "$arg" == "-w" ]] && is_remote=1
  if [[ "$arg" == "--fail" ]]; then
    has_fail_flag=1
  elif [[ "$arg" == -* && "$arg" != --* && "$arg" == *f* ]]; then
    has_fail_flag=1
  fi
done

if [[ "$is_remote" == "1" ]]; then
  plan_file="${FAKE_CURL_REMOTE_PLAN}"
  counter_file="${FAKE_CURL_REMOTE_COUNTER}"
else
  plan_file="${FAKE_CURL_LOCAL_PLAN}"
  counter_file="${FAKE_CURL_LOCAL_COUNTER}"
fi

idx=0
[[ -f "$counter_file" ]] && idx="$(cat "$counter_file")"
line="$(sed -n "$((idx + 1))p" "$plan_file")"
if [[ -z "$line" ]]; then
  line="$(tail -n1 "$plan_file")"
fi
echo "$((idx + 1))" > "$counter_file"

if [[ "$is_remote" == "1" ]]; then
  case "$line" in
    unreachable)
      echo "curl: (7) fake connection refused" >&2
      exit 7
      ;;
    2??)
      printf '%s' "$line"
      exit 0
      ;;
    *)
      printf '%s' "$line"
      if [[ "$has_fail_flag" == "1" ]]; then
        exit 22
      fi
      exit 0
      ;;
  esac
else
  case "$line" in
    ok)
      printf 'ok'
      exit 0
      ;;
    *)
      echo "curl: (22) fake http failure" >&2
      exit 22
      ;;
  esac
fi
EOF
  chmod +x "${dir}/curl"
}

curl_plan() {
  local path="$1"
  shift
  printf '%s\n' "$@" > "$path"
}

# write_fake_install_release stands in for scripts/install-release.sh:
# logs every invocation and "installs" the four service binaries as
# self-identifying stubs at the requested version. When
# FAKE_INSTALL_RELEASE_FAIL_VERSIONS_FILE is set and exists, any requested
# version listed in it (one per line) fails instead of installing, so a
# test can make ONE specific version's (re)install fail (e.g. the
# auto-rollback's reinstall of a previous version) without also failing
# the initial, unrelated install call in the same test.
write_fake_install_release() {
  local path="$1" log_file="$2"
  cat > "$path" <<EOF
#!/usr/bin/env bash
version="\$1"
echo "\${OPENBRAIN_REPO}|\${OPENBRAIN_INSTALL_DIR}|\${version}" >> "${log_file}"
if [[ -n "\${FAKE_INSTALL_RELEASE_FAIL_VERSIONS_FILE:-}" && -f "\${FAKE_INSTALL_RELEASE_FAIL_VERSIONS_FILE}" ]]; then
  if grep -qxF "\$version" "\${FAKE_INSTALL_RELEASE_FAIL_VERSIONS_FILE}"; then
    echo "fake install-release: simulated failure for \$version" >&2
    exit 1
  fi
fi
for name in openbrain-web openbrain-telegram openbrain-slack openbrain-watchd; do
  cat > "\${OPENBRAIN_INSTALL_DIR}/\${name}" <<INNER
#!/usr/bin/env bash
if [[ "\\\${1:-}" == "--version" ]]; then
  echo "\${version}"
  exit 0
fi
exit 1
INNER
  chmod +x "\${OPENBRAIN_INSTALL_DIR}/\${name}"
done
exit "\${FAKE_INSTALL_RELEASE_EXIT:-0}"
EOF
  chmod +x "$path"
}

# --- drop_in_content / drop_in_dir / drop_in_path ------------------------

@test "drop_in_content clears then sets ExecStart to the target binary path" {
  run bash -c "source '$SCRIPT'; drop_in_content /usr/local/bin/openbrain-web"
  [ "$status" -eq 0 ]
  [[ "$output" == *"[Service]"* ]]
  [[ "$output" == *$'ExecStart=\nExecStart=/usr/local/bin/openbrain-web'* ]]
}

@test "drop_in_path builds the system <unit>.service.d/override.conf path" {
  run bash -c "source '$SCRIPT'; drop_in_path /etc/systemd/system openbrain-web"
  [ "$status" -eq 0 ]
  [ "$output" = "/etc/systemd/system/openbrain-web.service.d/override.conf" ]
}

# --- check_privilege / use_sudo ------------------------------------------

@test "check_privilege passes when euid is 0 (root), no sudo needed" {
  run bash -c "source '$SCRIPT'; check_privilege sudo 0"
  [ "$status" -eq 0 ]
}

@test "check_privilege passes when euid is non-zero and sudo is on PATH" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  write_fake_sudo "$fake_bin"

  run env PATH="${fake_bin}:${PATH}" bash -c "source '$SCRIPT'; check_privilege sudo 1000"
  [ "$status" -eq 0 ]
}

@test "check_privilege fails closed when euid is non-zero and sudo is not on PATH" {
  local empty_path="${WORK_DIR}/empty-path"
  mkdir -p "$empty_path"
  ln -s "$(command -v bash)" "${empty_path}/bash"

  run env PATH="$empty_path" bash -c "source '$SCRIPT'; check_privilege sudo 1000"
  [ "$status" -ne 0 ]
  [[ "$output" == *"require root or sudo"* ]]
}

@test "use_sudo prints 0 for root and 1 for a non-root euid" {
  run bash -c "source '$SCRIPT'; OPENBRAIN_DS_EUID=0; use_sudo"
  [ "$output" = "0" ]

  run bash -c "source '$SCRIPT'; OPENBRAIN_DS_EUID=1000; use_sudo"
  [ "$output" = "1" ]
}

# --- write_drop_in ---------------------------------------------------------

@test "write_drop_in creates the drop-in directory and file with the right content (unprivileged path)" {
  run bash -c "source '$SCRIPT'; write_drop_in '${SYSTEMD_DIR}' openbrain-web '${INSTALL_DIR}' 0 sudo"
  [ "$status" -eq 0 ]

  local dropin="${SYSTEMD_DIR}/openbrain-web.service.d/override.conf"
  [ -f "$dropin" ]
  run cat "$dropin"
  [[ "$output" == *"ExecStart=${INSTALL_DIR}/openbrain-web"* ]]
}

@test "write_drop_in does not rewrite the file when content already matches (idempotent)" {
  bash -c "source '$SCRIPT'; write_drop_in '${SYSTEMD_DIR}' openbrain-web '${INSTALL_DIR}' 0 sudo"
  local dropin="${SYSTEMD_DIR}/openbrain-web.service.d/override.conf"
  local before
  before="$(stat -c %Y "$dropin")"
  sleep 1

  run bash -c "source '$SCRIPT'; write_drop_in '${SYSTEMD_DIR}' openbrain-web '${INSTALL_DIR}' 0 sudo"
  [ "$status" -eq 0 ]
  [[ "$output" == *"already matches the target"* ]]

  local after
  after="$(stat -c %Y "$dropin")"
  [ "$before" = "$after" ]
}

@test "write_drop_in rewrites the file when the target binary path changed" {
  bash -c "source '$SCRIPT'; write_drop_in '${SYSTEMD_DIR}' openbrain-web '${INSTALL_DIR}' 0 sudo"
  local other_dir="${WORK_DIR}/install-2"
  mkdir -p "$other_dir"

  run bash -c "source '$SCRIPT'; write_drop_in '${SYSTEMD_DIR}' openbrain-web '${other_dir}' 0 sudo"
  [ "$status" -eq 0 ]
  [[ "$output" == *"wrote drop-in"* ]]

  run cat "${SYSTEMD_DIR}/openbrain-web.service.d/override.conf"
  [[ "$output" == *"ExecStart=${other_dir}/openbrain-web"* ]]
}

# --- repoint_and_restart: command sequence + secondary-unit state --------

@test "repoint_and_restart writes all four drop-ins, reloads once, always restarts the primary unit" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  local log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$log"

  run env PATH="${fake_bin}:${PATH}" OPENBRAIN_DS_EUID=0 \
    bash -c "source '$SCRIPT'; repoint_and_restart '${SYSTEMD_DIR}' '${INSTALL_DIR}' systemctl sudo"
  [ "$status" -eq 0 ]

  for unit in openbrain-web openbrain-telegram openbrain-slack openbrain-watchd; do
    [ -f "${SYSTEMD_DIR}/${unit}.service.d/override.conf" ]
  done

  run cat "$log"
  [ "$(grep -c '^daemon-reload$' "$log")" -eq 1 ]
  [ "$(grep -c '^restart openbrain-web.service$' "$log")" -eq 1 ]

  local reload_line restart_line
  reload_line="$(grep -n '^daemon-reload$' "$log" | head -1 | cut -d: -f1)"
  restart_line="$(grep -n '^restart openbrain-web.service$' "$log" | head -1 | cut -d: -f1)"
  [ "$reload_line" -lt "$restart_line" ]
}

@test "repoint_and_restart restarts an already-active secondary unit but leaves an inactive one alone" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  local log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$log"

  run env PATH="${fake_bin}:${PATH}" OPENBRAIN_DS_EUID=0 FAKE_ACTIVE_openbrain_telegram_service=active \
    bash -c "source '$SCRIPT'; repoint_and_restart '${SYSTEMD_DIR}' '${INSTALL_DIR}' systemctl sudo"
  [ "$status" -eq 0 ]

  run cat "$log"
  [[ "$output" == *"restart openbrain-telegram.service"* ]]
  [[ "$output" != *"restart openbrain-slack.service"* ]]
  [[ "$output" != *"restart openbrain-watchd.service"* ]]
}

@test "repoint_and_restart fails closed (exit 5) when daemon-reload fails" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  local log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$log"

  run env PATH="${fake_bin}:${PATH}" OPENBRAIN_DS_EUID=0 FAKE_SYSTEMCTL_RELOAD_EXIT=1 \
    bash -c "source '$SCRIPT'; repoint_and_restart '${SYSTEMD_DIR}' '${INSTALL_DIR}' systemctl sudo || echo \"exit=\$?\""
  [[ "$output" == *"exit=5"* ]]
  [[ "$output" == *"daemon-reload failed"* ]]

  run cat "$log"
  [[ "$output" != *"restart"* ]]
}

@test "repoint_and_restart fails closed (exit 6) when the primary unit restart fails" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  local log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$log"

  run env PATH="${fake_bin}:${PATH}" OPENBRAIN_DS_EUID=0 FAKE_SYSTEMCTL_RESTART_EXIT_openbrain_web_service=1 \
    bash -c "source '$SCRIPT'; repoint_and_restart '${SYSTEMD_DIR}' '${INSTALL_DIR}' systemctl sudo || echo \"exit=\$?\""
  [[ "$output" == *"exit=6"* ]]
  [[ "$output" == *"restart failed for openbrain-web"* ]]
}

@test "repoint_and_restart fails closed (exit 4) when a drop-in write fails" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  local log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$log"

  chmod 555 "$SYSTEMD_DIR"

  run env PATH="${fake_bin}:${PATH}" OPENBRAIN_DS_EUID=0 \
    bash -c "source '$SCRIPT'; repoint_and_restart '${SYSTEMD_DIR}' '${INSTALL_DIR}' systemctl sudo || echo \"exit=\$?\""
  chmod 755 "$SYSTEMD_DIR"

  [[ "$output" == *"exit=4"* ]]
  [[ "$output" == *"failed to create"* ]]
  [ ! -f "$log" ]
}

# --- health_check_local / health_check_remote / run_health_check ---------
# (Same two-tier contract as OB-063's repoint-unit.sh; unchanged logic.)

@test "health_check_local passes on a reachable 'ok' body" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  write_fake_curl "$fake_bin"
  curl_plan "${WORK_DIR}/local.plan" ok

  run env PATH="${fake_bin}:${PATH}" \
    FAKE_CURL_LOCAL_PLAN="${WORK_DIR}/local.plan" FAKE_CURL_LOCAL_COUNTER="${WORK_DIR}/local.count" \
    bash -c "source '$SCRIPT'; health_check_local http://127.0.0.1:10203/health"
  [ "$status" -eq 0 ]
}

@test "health_check_remote returns 0 on HTTP 200, 1 on reachable non-200, 2 when unreachable" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  write_fake_curl "$fake_bin"

  curl_plan "${WORK_DIR}/remote.plan" 200
  run env PATH="${fake_bin}:${PATH}" \
    FAKE_CURL_REMOTE_PLAN="${WORK_DIR}/remote.plan" FAKE_CURL_REMOTE_COUNTER="${WORK_DIR}/remote.count" \
    bash -c "source '$SCRIPT'; health_check_remote https://openbrain.wr-s.net/mcp"
  [ "$status" -eq 0 ]

  curl_plan "${WORK_DIR}/remote2.plan" 500
  run env PATH="${fake_bin}:${PATH}" \
    FAKE_CURL_REMOTE_PLAN="${WORK_DIR}/remote2.plan" FAKE_CURL_REMOTE_COUNTER="${WORK_DIR}/remote2.count" \
    bash -c "source '$SCRIPT'; health_check_remote https://openbrain.wr-s.net/mcp"
  [ "$status" -eq 1 ]

  curl_plan "${WORK_DIR}/remote3.plan" unreachable
  run env PATH="${fake_bin}:${PATH}" \
    FAKE_CURL_REMOTE_PLAN="${WORK_DIR}/remote3.plan" FAKE_CURL_REMOTE_COUNTER="${WORK_DIR}/remote3.count" \
    bash -c "source '$SCRIPT'; health_check_remote https://openbrain.wr-s.net/mcp"
  [ "$status" -eq 2 ]
}

@test "run_health_check distinguishes local-healthy-remote-unreachable (2) from a genuine failure (1) and full pass (0)" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  write_fake_curl "$fake_bin"

  curl_plan "${WORK_DIR}/local.plan" ok
  curl_plan "${WORK_DIR}/remote.plan" 200
  run env PATH="${fake_bin}:${PATH}" \
    FAKE_CURL_LOCAL_PLAN="${WORK_DIR}/local.plan" FAKE_CURL_LOCAL_COUNTER="${WORK_DIR}/local.count" \
    FAKE_CURL_REMOTE_PLAN="${WORK_DIR}/remote.plan" FAKE_CURL_REMOTE_COUNTER="${WORK_DIR}/remote.count" \
    bash -c "source '$SCRIPT'; run_health_check http://127.0.0.1:10203/health https://openbrain.wr-s.net/mcp"
  [ "$status" -eq 0 ]

  curl_plan "${WORK_DIR}/local2.plan" ok
  curl_plan "${WORK_DIR}/remote2.plan" unreachable
  run env PATH="${fake_bin}:${PATH}" \
    FAKE_CURL_LOCAL_PLAN="${WORK_DIR}/local2.plan" FAKE_CURL_LOCAL_COUNTER="${WORK_DIR}/local2.count" \
    FAKE_CURL_REMOTE_PLAN="${WORK_DIR}/remote2.plan" FAKE_CURL_REMOTE_COUNTER="${WORK_DIR}/remote2.count" \
    bash -c "source '$SCRIPT'; run_health_check http://127.0.0.1:10203/health https://openbrain.wr-s.net/mcp"
  [ "$status" -eq 2 ]
  [[ "$output" == *"not a genuine failure"* ]]

  curl_plan "${WORK_DIR}/local3.plan" fail
  curl_plan "${WORK_DIR}/remote3.plan" 200
  run env PATH="${fake_bin}:${PATH}" \
    FAKE_CURL_LOCAL_PLAN="${WORK_DIR}/local3.plan" FAKE_CURL_LOCAL_COUNTER="${WORK_DIR}/local3.count" \
    FAKE_CURL_REMOTE_PLAN="${WORK_DIR}/remote3.plan" FAKE_CURL_REMOTE_COUNTER="${WORK_DIR}/remote3.count" \
    bash -c "source '$SCRIPT'; run_health_check http://127.0.0.1:10203/health https://openbrain.wr-s.net/mcp"
  [ "$status" -eq 1 ]
}

# --- current_installed_version --------------------------------------------

@test "current_installed_version reads the primary unit's --version" {
  write_fake_binary "${INSTALL_DIR}/openbrain-web" v0.7.0

  run bash -c "source '$SCRIPT'; current_installed_version '${INSTALL_DIR}'"
  [ "$status" -eq 0 ]
  [ "$output" = "v0.7.0" ]
}

@test "current_installed_version fails closed with no output when nothing is installed yet" {
  run bash -c "source '$SCRIPT'; current_installed_version '${INSTALL_DIR}'"
  [ "$status" -ne 0 ]
  [ -z "$output" ]
}

# --- dry-run: writes nothing, invokes nothing, no privilege check --------

@test "cmd_dry_run prints unit contents and command sequence, with no PATH tool available at all" {
  local empty_path="${WORK_DIR}/empty-path"
  mkdir -p "$empty_path"
  ln -s "$(command -v bash)" "${empty_path}/bash"
  ln -s "$(command -v cat)" "${empty_path}/cat"

  run env PATH="$empty_path" bash -c "
    source '$SCRIPT'
    OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
    OPENBRAIN_DS_SYSTEMD_DIR='${SYSTEMD_DIR}'
    cmd_dry_run v0.8.0
  "
  [ "$status" -eq 0 ]
  [[ "$output" == *"DRY RUN"* ]]
  [[ "$output" == *"no privilege check performed"* ]]
  [[ "$output" == *"target version: v0.8.0"* ]]
  [[ "$output" == *"ExecStart=${INSTALL_DIR}/openbrain-web"* ]]
  [[ "$output" == *"systemctl daemon-reload"* ]]
  [[ "$output" == *"systemctl restart openbrain-web.service"* ]]

  for unit in openbrain-web openbrain-telegram openbrain-slack openbrain-watchd; do
    [ ! -e "${SYSTEMD_DIR}/${unit}.service.d/override.conf" ]
  done
}

@test "main dry-run dispatches to cmd_dry_run and exits 0" {
  local empty_path="${WORK_DIR}/empty-path"
  mkdir -p "$empty_path"
  ln -s "$(command -v bash)" "${empty_path}/bash"
  ln -s "$(command -v cat)" "${empty_path}/cat"

  run env PATH="$empty_path" bash -c "
    source '$SCRIPT'
    OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
    OPENBRAIN_DS_SYSTEMD_DIR='${SYSTEMD_DIR}'
    main dry-run v0.8.0
  "
  [ "$status" -eq 0 ]
  [[ "$output" == *"DRY RUN"* ]]
}

@test "main with no command is a usage error (exit 1)" {
  run bash -c "source '$SCRIPT'; main"
  [ "$status" -eq 1 ]
  [[ "$output" == *"a command is required"* ]]
}

@test "main with an unrecognized command is a usage error (exit 1)" {
  run bash -c "source '$SCRIPT'; main bogus"
  [ "$status" -eq 1 ]
  [[ "$output" == *"unrecognized command"* ]]
}

# --- cmd_rollback: usage and privilege gates ------------------------------

@test "cmd_rollback requires an explicit version (exit 1)" {
  run bash -c "source '$SCRIPT'; cmd_rollback ''"
  [ "$status" -eq 1 ]
  [[ "$output" == *"requires an explicit prior VERSION"* ]]
}

@test "cmd_rollback fails closed (exit 2) with no privilege and no sudo on PATH" {
  local empty_path="${WORK_DIR}/empty-path"
  mkdir -p "$empty_path"
  ln -s "$(command -v bash)" "${empty_path}/bash"

  run env PATH="$empty_path" OPENBRAIN_DS_EUID=1000 bash -c "source '$SCRIPT'; cmd_rollback v0.6.0"
  [ "$status" -eq 2 ]
  [[ "$output" == *"require root or sudo"* ]]
}

@test "cmd_apply fails closed (exit 2) with no privilege and no sudo on PATH" {
  local empty_path="${WORK_DIR}/empty-path"
  mkdir -p "$empty_path"
  ln -s "$(command -v bash)" "${empty_path}/bash"

  run env PATH="$empty_path" OPENBRAIN_DS_EUID=1000 bash -c "source '$SCRIPT'; cmd_apply v0.8.0"
  [ "$status" -eq 2 ]
  [[ "$output" == *"require root or sudo"* ]]
}

# --- cmd_apply: install failure, happy path, idempotency, auto-rollback --

@test "cmd_apply aborts before any unit change when the initial install fails (exit 3)" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  write_fake_sudo "$fake_bin"
  local systemctl_log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$systemctl_log"
  write_fake_curl "$fake_bin"
  local installer="${WORK_DIR}/fake-install-release.sh"
  local installer_log="${WORK_DIR}/installer.log"
  write_fake_install_release "$installer" "$installer_log"

  run env PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0 \
    OPENBRAIN_DS_INSTALL_SCRIPT="$installer" \
    FAKE_INSTALL_RELEASE_EXIT=1 \
    bash -c "
      source '$SCRIPT'
      OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
      OPENBRAIN_DS_SYSTEMD_DIR='${SYSTEMD_DIR}'
      cmd_apply v0.8.0
    "
  [ "$status" -eq 3 ]
  [[ "$output" == *"failed to install version 'v0.8.0'; nothing repointed"* ]]
  [ ! -e "${SYSTEMD_DIR}/openbrain-web.service.d/override.conf" ]
  [ ! -f "$systemctl_log" ]
}

@test "cmd_apply happy path: installs, repoints all four units, restarts, and passes health (exit 0)" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  write_fake_sudo "$fake_bin"
  local systemctl_log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$systemctl_log"
  write_fake_curl "$fake_bin"
  local installer="${WORK_DIR}/fake-install-release.sh"
  local installer_log="${WORK_DIR}/installer.log"
  write_fake_install_release "$installer" "$installer_log"
  curl_plan "${WORK_DIR}/local.plan" ok
  curl_plan "${WORK_DIR}/remote.plan" 200

  run env PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0 \
    OPENBRAIN_DS_INSTALL_SCRIPT="$installer" \
    FAKE_CURL_LOCAL_PLAN="${WORK_DIR}/local.plan" FAKE_CURL_LOCAL_COUNTER="${WORK_DIR}/local.count" \
    FAKE_CURL_REMOTE_PLAN="${WORK_DIR}/remote.plan" FAKE_CURL_REMOTE_COUNTER="${WORK_DIR}/remote.count" \
    bash -c "
      source '$SCRIPT'
      OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
      OPENBRAIN_DS_SYSTEMD_DIR='${SYSTEMD_DIR}'
      cmd_apply v0.8.0
    "
  [ "$status" -eq 0 ]
  [[ "$output" == *"version 'v0.8.0' is live and healthy"* ]]

  run cat "$installer_log"
  [ "$output" = "windingriverholdings/openbrain|${INSTALL_DIR}|v0.8.0" ]

  for unit in openbrain-web openbrain-telegram openbrain-slack openbrain-watchd; do
    run cat "${SYSTEMD_DIR}/${unit}.service.d/override.conf"
    [[ "$output" == *"ExecStart=${INSTALL_DIR}/${unit}"* ]]
  done

  run cat "$systemctl_log"
  [[ "$output" == *"daemon-reload"* ]]
  [[ "$output" == *"restart openbrain-web.service"* ]]
}

@test "cmd_apply is idempotent: a second run at the same version writes no new drop-in diff" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  write_fake_sudo "$fake_bin"
  local systemctl_log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$systemctl_log"
  write_fake_curl "$fake_bin"
  local installer="${WORK_DIR}/fake-install-release.sh"
  local installer_log="${WORK_DIR}/installer.log"
  write_fake_install_release "$installer" "$installer_log"
  curl_plan "${WORK_DIR}/local.plan" ok ok
  curl_plan "${WORK_DIR}/remote.plan" 200 200

  local common_env=(
    PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0
    OPENBRAIN_DS_INSTALL_SCRIPT="$installer"
    FAKE_CURL_LOCAL_PLAN="${WORK_DIR}/local.plan" FAKE_CURL_LOCAL_COUNTER="${WORK_DIR}/local.count"
    FAKE_CURL_REMOTE_PLAN="${WORK_DIR}/remote.plan" FAKE_CURL_REMOTE_COUNTER="${WORK_DIR}/remote.count"
  )
  local apply_cmd="
    source '$SCRIPT'
    OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
    OPENBRAIN_DS_SYSTEMD_DIR='${SYSTEMD_DIR}'
    cmd_apply v0.8.0
  "

  run env "${common_env[@]}" bash -c "$apply_cmd"
  [ "$status" -eq 0 ]
  local dropin="${SYSTEMD_DIR}/openbrain-web.service.d/override.conf"
  local mtime_before
  mtime_before="$(stat -c %Y "$dropin")"
  sleep 1

  run env "${common_env[@]}" bash -c "$apply_cmd"
  [ "$status" -eq 0 ]
  [[ "$output" == *"already matches the target"* ]]

  local mtime_after
  mtime_after="$(stat -c %Y "$dropin")"
  [ "$mtime_before" = "$mtime_after" ]
}

@test "cmd_apply automatically rolls back to the previous version on a genuine health-check failure and recovers (exit 10)" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  write_fake_sudo "$fake_bin"
  local systemctl_log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$systemctl_log"
  write_fake_curl "$fake_bin"
  local installer="${WORK_DIR}/fake-install-release.sh"
  local installer_log="${WORK_DIR}/installer.log"
  write_fake_install_release "$installer" "$installer_log"

  # Seed a previously-installed version so cmd_apply has a rollback target.
  env OPENBRAIN_REPO=windingriverholdings/openbrain OPENBRAIN_INSTALL_DIR="$INSTALL_DIR" "$installer" v0.7.0

  # First health check (for v0.8.0) fails locally; the second (after
  # rollback to v0.7.0) passes.
  curl_plan "${WORK_DIR}/local.plan" fail ok
  curl_plan "${WORK_DIR}/remote.plan" 200 200

  run env PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0 \
    OPENBRAIN_DS_INSTALL_SCRIPT="$installer" \
    FAKE_CURL_LOCAL_PLAN="${WORK_DIR}/local.plan" FAKE_CURL_LOCAL_COUNTER="${WORK_DIR}/local.count" \
    FAKE_CURL_REMOTE_PLAN="${WORK_DIR}/remote.plan" FAKE_CURL_REMOTE_COUNTER="${WORK_DIR}/remote.count" \
    bash -c "
      source '$SCRIPT'
      OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
      OPENBRAIN_DS_SYSTEMD_DIR='${SYSTEMD_DIR}'
      cmd_apply v0.8.0 || echo \"exit=\$?\"
    "
  [[ "$output" == *"exit=10"* ]]
  [[ "$output" == *"attempting automatic rollback"* ]]
  [[ "$output" == *"automatic rollback to 'v0.7.0' succeeded"* ]]
  [[ "$output" == *"did NOT go live"* ]]

  run cat "$installer_log"
  [[ "$output" == *"|v0.7.0"* ]]
  [[ "$output" == *"|v0.8.0"* ]]
  [ "$(echo "$output" | wc -l)" -eq 3 ]
  [ "$(echo "$output" | sed -n '3p' | awk -F'|' '{print $3}')" = "v0.7.0" ]

  # daemon-reload and restart ran twice: once for the failed v0.8.0
  # cutover, once for the rollback cutover. Never a restart-loop retrying
  # the same version.
  run cat "$systemctl_log"
  [ "$(grep -c '^restart openbrain-web.service$' "$systemctl_log")" -eq 2 ]
}

@test "cmd_apply auto-rollback has no previous version to fall back to: loud actionable error (exit 7)" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  write_fake_sudo "$fake_bin"
  local systemctl_log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$systemctl_log"
  write_fake_curl "$fake_bin"
  local installer="${WORK_DIR}/fake-install-release.sh"
  local installer_log="${WORK_DIR}/installer.log"
  write_fake_install_release "$installer" "$installer_log"
  curl_plan "${WORK_DIR}/local.plan" fail
  # No pre-existing binary at INSTALL_DIR/openbrain-web: nothing to roll
  # back to.

  run env PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0 \
    OPENBRAIN_DS_INSTALL_SCRIPT="$installer" \
    FAKE_CURL_LOCAL_PLAN="${WORK_DIR}/local.plan" FAKE_CURL_LOCAL_COUNTER="${WORK_DIR}/local.count" \
    bash -c "
      source '$SCRIPT'
      OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
      OPENBRAIN_DS_SYSTEMD_DIR='${SYSTEMD_DIR}'
      cmd_apply v1.0.0
    "
  [ "$status" -eq 7 ]
  [[ "$output" == *"no previously-installed version was recorded"* ]]
}

@test "cmd_apply exit 8: automatic rollback's reinstall of the previous version itself fails" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  write_fake_sudo "$fake_bin"
  local systemctl_log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$systemctl_log"
  write_fake_curl "$fake_bin"
  local installer="${WORK_DIR}/fake-install-release.sh"
  local installer_log="${WORK_DIR}/installer.log"
  write_fake_install_release "$installer" "$installer_log"

  # Seed a previous version so it is recorded and resolvable at seed time.
  env OPENBRAIN_REPO=windingriverholdings/openbrain OPENBRAIN_INSTALL_DIR="$INSTALL_DIR" "$installer" v0.7.0

  # v0.8.0's own install (the first call cmd_apply makes) must succeed so
  # the cutover is reached and fails health; only the SUBSEQUENT reinstall
  # of v0.7.0 (the auto-rollback's own install call) is made to fail, via
  # the per-version fail-list rather than a blanket exit code.
  local fail_versions="${WORK_DIR}/fail-versions.txt"
  printf 'v0.7.0\n' > "$fail_versions"
  curl_plan "${WORK_DIR}/local.plan" fail

  run env PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0 \
    OPENBRAIN_DS_INSTALL_SCRIPT="$installer" \
    FAKE_CURL_LOCAL_PLAN="${WORK_DIR}/local.plan" FAKE_CURL_LOCAL_COUNTER="${WORK_DIR}/local.count" \
    FAKE_INSTALL_RELEASE_FAIL_VERSIONS_FILE="$fail_versions" \
    bash -c "
      source '$SCRIPT'
      OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
      OPENBRAIN_DS_SYSTEMD_DIR='${SYSTEMD_DIR}'
      cmd_apply v0.8.0
    "
  [ "$status" -eq 8 ]
  [[ "$output" == *"automatic rollback's reinstall of 'v0.7.0' itself failed"* ]]
  [[ "$output" == *"service state is UNKNOWN"* ]]
}

@test "cmd_apply exit 9: automatic rollback reinstalls the previous version but it still fails health" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  write_fake_sudo "$fake_bin"
  local systemctl_log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$systemctl_log"
  write_fake_curl "$fake_bin"
  local installer="${WORK_DIR}/fake-install-release.sh"
  local installer_log="${WORK_DIR}/installer.log"
  write_fake_install_release "$installer" "$installer_log"

  env OPENBRAIN_REPO=windingriverholdings/openbrain OPENBRAIN_INSTALL_DIR="$INSTALL_DIR" "$installer" v0.7.0

  # Both the initial cutover AND the rollback cutover fail health: local
  # fails every time.
  curl_plan "${WORK_DIR}/local.plan" fail fail

  run env PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0 \
    OPENBRAIN_DS_INSTALL_SCRIPT="$installer" \
    FAKE_CURL_LOCAL_PLAN="${WORK_DIR}/local.plan" FAKE_CURL_LOCAL_COUNTER="${WORK_DIR}/local.count" \
    bash -c "
      source '$SCRIPT'
      OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
      OPENBRAIN_DS_SYSTEMD_DIR='${SYSTEMD_DIR}'
      cmd_apply v0.8.0
    "
  [ "$status" -eq 9 ]
  [[ "$output" == *"reinstalled 'v0.7.0' but its cutover still failed"* ]]
  [[ "$output" == *"manual intervention required"* ]]
}

# --- cmd_rollback: on-demand path -----------------------------------------

@test "cmd_rollback happy path: installs the requested version and confirms health (exit 0)" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  write_fake_sudo "$fake_bin"
  local systemctl_log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$systemctl_log"
  write_fake_curl "$fake_bin"
  local installer="${WORK_DIR}/fake-install-release.sh"
  local installer_log="${WORK_DIR}/installer.log"
  write_fake_install_release "$installer" "$installer_log"
  curl_plan "${WORK_DIR}/local.plan" ok
  curl_plan "${WORK_DIR}/remote.plan" 200

  run env PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0 \
    OPENBRAIN_DS_INSTALL_SCRIPT="$installer" \
    FAKE_CURL_LOCAL_PLAN="${WORK_DIR}/local.plan" FAKE_CURL_LOCAL_COUNTER="${WORK_DIR}/local.count" \
    FAKE_CURL_REMOTE_PLAN="${WORK_DIR}/remote.plan" FAKE_CURL_REMOTE_COUNTER="${WORK_DIR}/remote.count" \
    bash -c "
      source '$SCRIPT'
      OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
      OPENBRAIN_DS_SYSTEMD_DIR='${SYSTEMD_DIR}'
      cmd_rollback v0.6.0
    "
  [ "$status" -eq 0 ]
  [[ "$output" == *"version 'v0.6.0' is live and healthy"* ]]
  run cat "$installer_log"
  [ "$output" = "windingriverholdings/openbrain|${INSTALL_DIR}|v0.6.0" ]
}

@test "cmd_rollback aborts before any unit change when install of the requested version fails (exit 3)" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  write_fake_sudo "$fake_bin"
  local systemctl_log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$systemctl_log"
  write_fake_curl "$fake_bin"
  local installer="${WORK_DIR}/fake-install-release.sh"
  local installer_log="${WORK_DIR}/installer.log"
  write_fake_install_release "$installer" "$installer_log"

  run env PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0 \
    OPENBRAIN_DS_INSTALL_SCRIPT="$installer" \
    FAKE_INSTALL_RELEASE_EXIT=1 \
    bash -c "
      source '$SCRIPT'
      OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
      OPENBRAIN_DS_SYSTEMD_DIR='${SYSTEMD_DIR}'
      cmd_rollback v0.6.0
    "
  [ "$status" -eq 3 ]
  [ ! -f "$systemctl_log" ]
}

@test "cmd_rollback fails closed (exit 7) with no further automatic rollback attempted when the cutover fails" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  write_fake_sudo "$fake_bin"
  local systemctl_log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$systemctl_log"
  write_fake_curl "$fake_bin"
  local installer="${WORK_DIR}/fake-install-release.sh"
  local installer_log="${WORK_DIR}/installer.log"
  write_fake_install_release "$installer" "$installer_log"
  curl_plan "${WORK_DIR}/local.plan" fail

  run env PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0 \
    OPENBRAIN_DS_INSTALL_SCRIPT="$installer" \
    FAKE_CURL_LOCAL_PLAN="${WORK_DIR}/local.plan" FAKE_CURL_LOCAL_COUNTER="${WORK_DIR}/local.count" \
    bash -c "
      source '$SCRIPT'
      OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
      OPENBRAIN_DS_SYSTEMD_DIR='${SYSTEMD_DIR}'
      cmd_rollback v0.6.0
    "
  [ "$status" -eq 7 ]
  [[ "$output" == *"no further automatic rollback attempted"* ]]

  # Exactly one restart attempt: rollback never restart-loops on its own.
  run cat "$systemctl_log"
  [ "$(grep -c '^restart openbrain-web.service$' "$systemctl_log")" -eq 1 ]
}
