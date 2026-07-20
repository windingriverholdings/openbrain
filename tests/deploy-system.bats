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
  DEPLOY_DIR="${BATS_TEST_DIRNAME}/../deploy"
  WORK_DIR="$(mktemp -d)"
  SYSTEMD_DIR="${WORK_DIR}/systemd-system"
  INSTALL_DIR="${WORK_DIR}/install"
  mkdir -p "$SYSTEMD_DIR" "$INSTALL_DIR"

  # A present EnvironmentFile so the config-precedes-start preflight passes.
  ENV_FILE="${WORK_DIR}/openbrain.env"
  printf 'OPENBRAIN_DB_PASSWORD=hunter2\n' > "$ENV_FILE"

  # An EMPTY --user systemd dir: no --user unit is present to tear down, so
  # cmd_apply/cmd_rollback treat this as a STEADY-STATE run (never touching
  # the operator's real ~/.config/systemd/user). First-cutover tests point
  # this at their own scratch dir with fake --user unit files instead.
  USER_SYSTEMD_DIR="${WORK_DIR}/user-systemd"
  mkdir -p "$USER_SYSTEMD_DIR"

  # A fake loginctl (absolute path, never the host's real loginctl) so no
  # test can read or change the operator's real linger state.
  FAKE_LOGINCTL="${WORK_DIR}/loginctl"
  FAKE_LOGINCTL_LOG="${WORK_DIR}/loginctl.log"
  write_fake_loginctl "$FAKE_LOGINCTL" "$FAKE_LOGINCTL_LOG"

  # The OPENBRAIN_DS_* overrides every cmd_apply/cmd_rollback test needs so
  # nothing reaches real systemd, real loginctl, or the real /etc.
  DS_ENV=(
    OPENBRAIN_DS_ENV_FILE="$ENV_FILE"
    OPENBRAIN_DS_USER_SYSTEMD_DIR="$USER_SYSTEMD_DIR"
    OPENBRAIN_DS_LOGINCTL="$FAKE_LOGINCTL"
    OPENBRAIN_DS_LINGER_USER=testuser
  )
}

teardown() {
  rm -rf "$WORK_DIR"
}

# write_fake_loginctl writes a loginctl stub (absolute path, never on PATH by
# accident) that reports the linger state from FAKE_LINGER_STATE (default
# "no") and logs every invocation. It never touches real linger state.
write_fake_loginctl() {
  local path="$1" log_file="$2"
  cat > "$path" <<EOF
#!/usr/bin/env bash
echo "\$*" >> "${log_file}"
case "\${1:-}" in
  show-user)
    printf 'Linger=%s\n' "\${FAKE_LINGER_STATE:-no}"
    exit 0
    ;;
  disable-linger|enable-linger)
    exit "\${FAKE_LOGINCTL_EXIT:-0}"
    ;;
  *)
    echo "fake loginctl: unhandled args: \$*" >&2
    exit 99
    ;;
esac
EOF
  chmod +x "$path"
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

# write_fake_systemctl writes a systemctl stub that logs every invocation
# (including any leading --user) to log_file and handles both SYSTEM-scope
# operations (restart/enable/disable/daemon-reload/is-active) and the
# --user-scope teardown/restore operations added in OB-068. System is-active
# reads FAKE_ACTIVE_<unit>; --user is-active reads FAKE_USER_ACTIVE_<unit>.
write_fake_systemctl() {
  local dir="$1" log_file="$2"
  cat > "${dir}/systemctl" <<EOF
#!/usr/bin/env bash
orig="\$*"
echo "\$orig" >> "${log_file}"
scope=system
if [[ "\${1:-}" == "--user" ]]; then
  scope=user
  shift
fi
verb="\${1:-}"
last="\${@: -1}"
unitkey="\${last//[-.]/_}"
case "\$verb" in
  daemon-reload)
    exit "\${FAKE_SYSTEMCTL_RELOAD_EXIT:-0}"
    ;;
  restart)
    if [[ "\$scope" == "user" ]]; then
      exit "\${FAKE_SYSTEMCTL_USER_RESTART_EXIT:-0}"
    fi
    varname="FAKE_SYSTEMCTL_RESTART_EXIT_\${unitkey}"
    exit "\${!varname:-\${FAKE_SYSTEMCTL_RESTART_EXIT:-0}}"
    ;;
  enable)
    if [[ "\$scope" == "user" ]]; then
      exit "\${FAKE_SYSTEMCTL_USER_ENABLE_EXIT:-0}"
    fi
    exit "\${FAKE_SYSTEMCTL_ENABLE_EXIT:-0}"
    ;;
  disable)
    if [[ "\$scope" == "user" ]]; then
      exit "\${FAKE_SYSTEMCTL_USER_DISABLE_EXIT:-0}"
    fi
    exit "\${FAKE_SYSTEMCTL_DISABLE_EXIT:-0}"
    ;;
  stop)
    exit 0
    ;;
  is-active)
    if [[ "\$scope" == "user" ]]; then
      varname="FAKE_USER_ACTIVE_\${unitkey}"
    else
      varname="FAKE_ACTIVE_\${unitkey}"
    fi
    state="\${!varname:-inactive}"
    [[ "\$state" == "active" ]] && exit 0
    exit 3
    ;;
  *)
    echo "fake systemctl: unhandled args: \$orig" >&2
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

# write_user_unit_file drops a minimal fake --user unit file into dir, so
# user_unit_present (and therefore the teardown path) sees it. Content is
# never parsed by the tool under test; only the file's presence matters.
write_user_unit_file() {
  local dir="$1" unit="$2"
  mkdir -p "$dir"
  cat > "${dir}/${unit}.service" <<EOF
[Unit]
Description=fake --user unit for ${unit}
[Service]
ExecStart=/bin/true
EOF
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

@test "repoint_and_restart fails closed (exit 6) with a distinct message when an already-active secondary unit's restart fails" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  local log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$log"

  run env PATH="${fake_bin}:${PATH}" OPENBRAIN_DS_EUID=0 \
    FAKE_ACTIVE_openbrain_telegram_service=active \
    FAKE_SYSTEMCTL_RESTART_EXIT_openbrain_telegram_service=1 \
    bash -c "source '$SCRIPT'; repoint_and_restart '${SYSTEMD_DIR}' '${INSTALL_DIR}' systemctl sudo || echo \"exit=\$?\""
  [[ "$output" == *"exit=6"* ]]
  [[ "$output" == *"restart failed for already-active openbrain-telegram"* ]]

  # Distinct from the primary-unit restart failure above: this fails at a
  # later step, so every drop-in was already written and the primary
  # unit's own restart already succeeded before this failure surfaced.
  for unit in openbrain-web openbrain-telegram openbrain-slack openbrain-watchd; do
    [ -f "${SYSTEMD_DIR}/${unit}.service.d/override.conf" ]
  done
  run cat "$log"
  [[ "$output" == *"restart openbrain-web.service"* ]]
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

@test "current_installed_version is bounded by OPENBRAIN_DS_VERSION_CHECK_TIMEOUT and does not hang on a stuck binary" {
  local hung_bin="${INSTALL_DIR}/openbrain-web"
  cat > "$hung_bin" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == "--version" ]]; then
  sleep 30
  echo "should-never-print"
  exit 0
fi
exit 1
EOF
  chmod +x "$hung_bin"

  local start end elapsed
  start="$(date +%s)"
  run env OPENBRAIN_DS_VERSION_CHECK_TIMEOUT=1 \
    bash -c "source '$SCRIPT'; current_installed_version '${INSTALL_DIR}'"
  end="$(date +%s)"
  elapsed=$((end - start))

  # Bounded well under the binary's 30s sleep: the timeout (1s) plus normal
  # process overhead, never the full hang duration.
  [ "$elapsed" -lt 10 ]
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

  run env "${DS_ENV[@]}" PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0 \
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

  run env "${DS_ENV[@]}" PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0 \
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
    "${DS_ENV[@]}"
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

  run env "${DS_ENV[@]}" PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0 \
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

  run env "${DS_ENV[@]}" PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0 \
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

  run env "${DS_ENV[@]}" PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0 \
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

  run env "${DS_ENV[@]}" PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0 \
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

  # daemon-reload and restart ran twice: once for the failed v0.8.0
  # cutover, once for the rollback cutover that also failed health. Never
  # a restart-loop retrying the same version (matching the exit-10 test's
  # loop-freedom assertion).
  run cat "$systemctl_log"
  [ "$(grep -c '^restart openbrain-web.service$' "$systemctl_log")" -eq 2 ]
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

  run env "${DS_ENV[@]}" PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0 \
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

  run env "${DS_ENV[@]}" PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0 \
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

  run env "${DS_ENV[@]}" PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0 \
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

# ===========================================================================
# OB-068: config-precedes-start preflight, base system unit install (Gap 2),
# old --user teardown + linger disable (Gap 1), boot-enable, and the two
# recovery paths (first-cutover restore vs. steady-state rollback).
# ===========================================================================

# --- check_env_file_present --------------------------------------------

@test "check_env_file_present passes when the file exists" {
  run bash -c "source '$SCRIPT'; check_env_file_present '${ENV_FILE}'"
  [ "$status" -eq 0 ]
}

@test "check_env_file_present fails closed with an actionable message when the file is missing" {
  run bash -c "source '$SCRIPT'; check_env_file_present '${WORK_DIR}/nope.env'"
  [ "$status" -ne 0 ]
  [[ "$output" == *"does not exist"* ]]
  [[ "$output" == *"relocate-config.sh"* ]]
}

# --- install_base_unit / install_base_units (Gap 2) ---------------------

@test "install_base_unit installs the shipped unit file at mode 0644" {
  run bash -c "source '$SCRIPT'; install_base_unit '${SYSTEMD_DIR}' '${DEPLOY_DIR}' openbrain-web 0 sudo"
  [ "$status" -eq 0 ]
  [ -f "${SYSTEMD_DIR}/openbrain-web.service" ]
  run stat -c '%a' "${SYSTEMD_DIR}/openbrain-web.service"
  [ "$output" = "644" ]
  run cmp "${DEPLOY_DIR}/openbrain-web.service" "${SYSTEMD_DIR}/openbrain-web.service"
  [ "$status" -eq 0 ]
}

@test "install_base_unit is idempotent: a second run makes no mtime change" {
  bash -c "source '$SCRIPT'; install_base_unit '${SYSTEMD_DIR}' '${DEPLOY_DIR}' openbrain-web 0 sudo"
  local before
  before="$(stat -c %Y "${SYSTEMD_DIR}/openbrain-web.service")"
  sleep 1

  run bash -c "source '$SCRIPT'; install_base_unit '${SYSTEMD_DIR}' '${DEPLOY_DIR}' openbrain-web 0 sudo"
  [ "$status" -eq 0 ]
  [[ "$output" == *"already matches the shipped file"* ]]
  local after
  after="$(stat -c %Y "${SYSTEMD_DIR}/openbrain-web.service")"
  [ "$before" = "$after" ]
}

@test "install_base_unit fails closed when the shipped unit file is missing" {
  local empty_deploy="${WORK_DIR}/empty-deploy"
  mkdir -p "$empty_deploy"
  run bash -c "source '$SCRIPT'; install_base_unit '${SYSTEMD_DIR}' '${empty_deploy}' openbrain-web 0 sudo"
  [ "$status" -ne 0 ]
  [[ "$output" == *"not found"* ]]
  [ ! -f "${SYSTEMD_DIR}/openbrain-web.service" ]
}

@test "install_base_units installs all four base units" {
  run bash -c "source '$SCRIPT'; install_base_units '${SYSTEMD_DIR}' '${DEPLOY_DIR}' 0 sudo"
  [ "$status" -eq 0 ]
  for unit in openbrain-web openbrain-telegram openbrain-slack openbrain-watchd; do
    [ -f "${SYSTEMD_DIR}/${unit}.service" ]
  done
}

@test "install_base_units stops at the first failure and does not install the remaining units" {
  local partial_deploy="${WORK_DIR}/partial-deploy"
  mkdir -p "$partial_deploy"
  cp "${DEPLOY_DIR}/openbrain-web.service" "$partial_deploy/"
  # openbrain-telegram.service deliberately absent from partial_deploy.

  run bash -c "source '$SCRIPT'; install_base_units '${SYSTEMD_DIR}' '${partial_deploy}' 0 sudo"
  [ "$status" -ne 0 ]
  [ -f "${SYSTEMD_DIR}/openbrain-web.service" ]
  [ ! -f "${SYSTEMD_DIR}/openbrain-slack.service" ]
}

# --- user_unit_present / user_unit_is_active -----------------------------

@test "user_unit_present is true only when the --user unit file exists" {
  write_user_unit_file "$USER_SYSTEMD_DIR" openbrain-web
  run bash -c "source '$SCRIPT'; user_unit_present '${USER_SYSTEMD_DIR}' openbrain-web"
  [ "$status" -eq 0 ]

  run bash -c "source '$SCRIPT'; user_unit_present '${USER_SYSTEMD_DIR}' openbrain-telegram"
  [ "$status" -ne 0 ]
}

@test "user_unit_is_active reads the --user systemctl is-active state" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  local log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$log"

  run env PATH="${fake_bin}:${PATH}" FAKE_USER_ACTIVE_openbrain_web_service=active \
    bash -c "source '$SCRIPT'; user_unit_is_active systemctl openbrain-web"
  [ "$status" -eq 0 ]

  run env PATH="${fake_bin}:${PATH}" \
    bash -c "source '$SCRIPT'; user_unit_is_active systemctl openbrain-telegram"
  [ "$status" -ne 0 ]

  run cat "$log"
  [[ "$output" == *"--user is-active --quiet openbrain-web.service"* ]]
}

# --- teardown_user_unit / teardown_user_units (Gap 1) --------------------

@test "teardown_user_unit is a no-op when no --user unit file is present" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  local log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$log"

  run env PATH="${fake_bin}:${PATH}" \
    bash -c "source '$SCRIPT'; teardown_user_unit systemctl '${USER_SYSTEMD_DIR}' openbrain-web"
  [ "$status" -eq 0 ]
  [ ! -f "$log" ]
}

@test "teardown_user_unit disables an existing --user unit even if currently inactive" {
  write_user_unit_file "$USER_SYSTEMD_DIR" openbrain-watchd
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  local log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$log"

  run env PATH="${fake_bin}:${PATH}" \
    bash -c "source '$SCRIPT'; teardown_user_unit systemctl '${USER_SYSTEMD_DIR}' openbrain-watchd"
  [ "$status" -eq 0 ]
  run cat "$log"
  [[ "$output" == *"--user disable --now openbrain-watchd.service"* ]]
}

@test "teardown_user_unit fails closed when systemctl --user disable fails" {
  write_user_unit_file "$USER_SYSTEMD_DIR" openbrain-web
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  local log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$log"

  run env PATH="${fake_bin}:${PATH}" FAKE_SYSTEMCTL_USER_DISABLE_EXIT=1 \
    bash -c "source '$SCRIPT'; teardown_user_unit systemctl '${USER_SYSTEMD_DIR}' openbrain-web"
  [ "$status" -ne 0 ]
  [[ "$output" == *"failed to stop and disable"* ]]
}

@test "teardown_user_units tears down every --user unit that is present, skipping the absent ones" {
  write_user_unit_file "$USER_SYSTEMD_DIR" openbrain-web
  write_user_unit_file "$USER_SYSTEMD_DIR" openbrain-watchd
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  local log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$log"

  run env PATH="${fake_bin}:${PATH}" \
    bash -c "source '$SCRIPT'; teardown_user_units systemctl '${USER_SYSTEMD_DIR}'"
  [ "$status" -eq 0 ]
  run cat "$log"
  [[ "$output" == *"--user disable --now openbrain-web.service"* ]]
  [[ "$output" == *"--user disable --now openbrain-watchd.service"* ]]
  [[ "$output" != *"openbrain-telegram"* ]]
  [[ "$output" != *"openbrain-slack"* ]]
}

# --- linger_enabled / disable_linger / enable_linger ---------------------

@test "linger_enabled is true only when loginctl reports Linger=yes" {
  run env FAKE_LINGER_STATE=yes \
    bash -c "source '$SCRIPT'; linger_enabled '${FAKE_LOGINCTL}' testuser"
  [ "$status" -eq 0 ]

  run env FAKE_LINGER_STATE=no \
    bash -c "source '$SCRIPT'; linger_enabled '${FAKE_LOGINCTL}' testuser"
  [ "$status" -ne 0 ]
}

@test "disable_linger is idempotent (no loginctl call) when linger is already off" {
  run env FAKE_LINGER_STATE=no \
    bash -c "source '$SCRIPT'; disable_linger '${FAKE_LOGINCTL}' testuser 0 sudo"
  [ "$status" -eq 0 ]
  [[ "$output" == *"not enabled (idempotent, no change)"* ]]
  run cat "$FAKE_LOGINCTL_LOG"
  [[ "$output" != *"disable-linger"* ]]
}

@test "disable_linger disables when currently enabled" {
  run env FAKE_LINGER_STATE=yes \
    bash -c "source '$SCRIPT'; disable_linger '${FAKE_LOGINCTL}' testuser 0 sudo"
  [ "$status" -eq 0 ]
  run cat "$FAKE_LOGINCTL_LOG"
  [[ "$output" == *"disable-linger testuser"* ]]
}

@test "disable_linger fails closed when loginctl disable-linger fails" {
  run env FAKE_LINGER_STATE=yes FAKE_LOGINCTL_EXIT=1 \
    bash -c "source '$SCRIPT'; disable_linger '${FAKE_LOGINCTL}' testuser 0 sudo"
  [ "$status" -ne 0 ]
  [[ "$output" == *"failed to disable linger"* ]]
}

@test "enable_linger re-enables linger (used by first-cutover recovery)" {
  run bash -c "source '$SCRIPT'; enable_linger '${FAKE_LOGINCTL}' testuser 0 sudo"
  [ "$status" -eq 0 ]
  run cat "$FAKE_LOGINCTL_LOG"
  [[ "$output" == *"enable-linger testuser"* ]]
}

# --- enable_system_unit / enable_system_boot ------------------------------

@test "enable_system_boot enables the primary unit and any active secondary, never the primary twice" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  local log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$log"

  run env PATH="${fake_bin}:${PATH}" \
    bash -c "source '$SCRIPT'; enable_system_boot systemctl 0 sudo openbrain-web openbrain-telegram"
  [ "$status" -eq 0 ]
  run cat "$log"
  [ "$(grep -c '^enable openbrain-web.service$' "$log")" -eq 1 ]
  [[ "$output" == *"enable openbrain-telegram.service"* ]]
}

@test "enable_system_boot fails closed when enabling the primary unit fails" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  local log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$log"

  run env PATH="${fake_bin}:${PATH}" FAKE_SYSTEMCTL_ENABLE_EXIT=1 \
    bash -c "source '$SCRIPT'; enable_system_boot systemctl 0 sudo"
  [ "$status" -ne 0 ]
}

# --- config-precedes-start preflight: cmd_apply / cmd_rollback -----------

@test "cmd_apply fails closed (exit 11) when the EnvironmentFile is missing; nothing installed or touched" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  write_fake_sudo "$fake_bin"
  local systemctl_log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$systemctl_log"
  local installer="${WORK_DIR}/fake-install-release.sh"
  local installer_log="${WORK_DIR}/installer.log"
  write_fake_install_release "$installer" "$installer_log"
  write_user_unit_file "$USER_SYSTEMD_DIR" openbrain-web

  run env "${DS_ENV[@]}" PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0 \
    OPENBRAIN_DS_ENV_FILE="${WORK_DIR}/nope.env" \
    OPENBRAIN_DS_INSTALL_SCRIPT="$installer" \
    bash -c "
      source '$SCRIPT'
      OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
      OPENBRAIN_DS_SYSTEMD_DIR='${SYSTEMD_DIR}'
      cmd_apply v0.8.0
    "
  [ "$status" -eq 11 ]
  [[ "$output" == *"does not exist"* ]]
  [ ! -f "$installer_log" ]
  [ ! -f "$systemctl_log" ]
}

@test "cmd_rollback fails closed (exit 11) when the EnvironmentFile is missing" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  write_fake_sudo "$fake_bin"
  local installer="${WORK_DIR}/fake-install-release.sh"
  local installer_log="${WORK_DIR}/installer.log"
  write_fake_install_release "$installer" "$installer_log"

  run env "${DS_ENV[@]}" PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0 \
    OPENBRAIN_DS_ENV_FILE="${WORK_DIR}/nope.env" \
    OPENBRAIN_DS_INSTALL_SCRIPT="$installer" \
    bash -c "
      source '$SCRIPT'
      OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
      OPENBRAIN_DS_SYSTEMD_DIR='${SYSTEMD_DIR}'
      cmd_rollback v0.6.0
    "
  [ "$status" -eq 11 ]
  [ ! -f "$installer_log" ]
}

# --- base-unit install gate: cmd_apply / cmd_rollback (exit 12) ----------

@test "cmd_apply fails closed (exit 12) when the base unit install fails; --user units left untouched" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  write_fake_sudo "$fake_bin"
  local systemctl_log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$systemctl_log"
  write_fake_curl "$fake_bin"
  local installer="${WORK_DIR}/fake-install-release.sh"
  local installer_log="${WORK_DIR}/installer.log"
  write_fake_install_release "$installer" "$installer_log"
  local empty_deploy="${WORK_DIR}/empty-deploy"
  mkdir -p "$empty_deploy"
  write_user_unit_file "$USER_SYSTEMD_DIR" openbrain-web

  run env "${DS_ENV[@]}" PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0 \
    OPENBRAIN_DS_DEPLOY_DIR="$empty_deploy" \
    OPENBRAIN_DS_INSTALL_SCRIPT="$installer" \
    bash -c "
      source '$SCRIPT'
      OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
      OPENBRAIN_DS_SYSTEMD_DIR='${SYSTEMD_DIR}'
      cmd_apply v0.8.0
    "
  [ "$status" -eq 12 ]
  [[ "$output" == *"nothing torn down, the --user units are left serving"* ]]
  # The install itself succeeded (nonzero happens only at base-unit install),
  # but no teardown or linger call was ever made.
  if [ -f "$systemctl_log" ]; then
    run cat "$systemctl_log"
    [[ "$output" != *"--user disable"* ]]
  fi
  if [ -f "$FAKE_LOGINCTL_LOG" ]; then
    run cat "$FAKE_LOGINCTL_LOG"
    [ -z "$output" ]
  fi
}

@test "cmd_rollback fails closed (exit 12) when the base unit install fails" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  write_fake_sudo "$fake_bin"
  local systemctl_log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$systemctl_log"
  write_fake_curl "$fake_bin"
  local installer="${WORK_DIR}/fake-install-release.sh"
  local installer_log="${WORK_DIR}/installer.log"
  write_fake_install_release "$installer" "$installer_log"
  local empty_deploy="${WORK_DIR}/empty-deploy"
  mkdir -p "$empty_deploy"

  run env "${DS_ENV[@]}" PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0 \
    OPENBRAIN_DS_DEPLOY_DIR="$empty_deploy" \
    OPENBRAIN_DS_INSTALL_SCRIPT="$installer" \
    bash -c "
      source '$SCRIPT'
      OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
      OPENBRAIN_DS_SYSTEMD_DIR='${SYSTEMD_DIR}'
      cmd_rollback v0.6.0
    "
  [ "$status" -eq 12 ]
  [[ "$output" == *"failed to install the base system units"* ]]
}

# --- full first-cutover integration: teardown, linger, cutover, boot -----

@test "cmd_apply first cutover: tears down all four present --user units, disables linger, cuts over, and enables boot" {
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

  for unit in openbrain-web openbrain-telegram openbrain-slack openbrain-watchd; do
    write_user_unit_file "$USER_SYSTEMD_DIR" "$unit"
  done

  run env "${DS_ENV[@]}" PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0 \
    FAKE_LINGER_STATE=yes FAKE_USER_ACTIVE_openbrain_web_service=active \
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

  # Base units installed before anything torn down.
  for unit in openbrain-web openbrain-telegram openbrain-slack openbrain-watchd; do
    [ -f "${SYSTEMD_DIR}/${unit}.service" ]
  done

  run cat "$systemctl_log"
  for unit in openbrain-web openbrain-telegram openbrain-slack openbrain-watchd; do
    [[ "$output" == *"--user disable --now ${unit}.service"* ]]
  done
  [[ "$output" == *"enable openbrain-web.service"* ]]

  run cat "$FAKE_LOGINCTL_LOG"
  [[ "$output" == *"disable-linger testuser"* ]]

  # Teardown happens before the system daemon-reload/restart: never two
  # binders on the port at once.
  run cat "$systemctl_log"
  local teardown_line reload_line
  teardown_line="$(grep -n '\-\-user disable --now openbrain-web.service' "$systemctl_log" | head -1 | cut -d: -f1)"
  reload_line="$(grep -n '^daemon-reload$' "$systemctl_log" | head -1 | cut -d: -f1)"
  [ "$teardown_line" -lt "$reload_line" ]
}

@test "cmd_apply first cutover: also enables a secondary system unit for boot when it was active under --user" {
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

  write_user_unit_file "$USER_SYSTEMD_DIR" openbrain-web
  write_user_unit_file "$USER_SYSTEMD_DIR" openbrain-telegram

  run env "${DS_ENV[@]}" PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0 \
    FAKE_USER_ACTIVE_openbrain_web_service=active FAKE_USER_ACTIVE_openbrain_telegram_service=active \
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
  run cat "$systemctl_log"
  [[ "$output" == *"enable openbrain-web.service"* ]]
  [[ "$output" == *"enable openbrain-telegram.service"* ]]
}

@test "cmd_apply steady state: no --user unit present, tears down nothing, disable-linger is a no-op" {
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
  # USER_SYSTEMD_DIR (from setup) is empty: no --user unit files at all.

  run env "${DS_ENV[@]}" PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0 \
    FAKE_LINGER_STATE=no \
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
  if [ -f "$systemctl_log" ]; then
    run cat "$systemctl_log"
    [[ "$output" != *"--user disable"* ]]
  fi
  run cat "$FAKE_LOGINCTL_LOG"
  [[ "$output" != *"disable-linger"* ]]
}

# --- exit 16: healthy cutover, boot-enable failure ------------------------

@test "cmd_apply exit 16: cutover succeeds and is healthy, but enabling for boot fails" {
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

  run env "${DS_ENV[@]}" PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0 \
    FAKE_SYSTEMCTL_ENABLE_EXIT=1 \
    OPENBRAIN_DS_INSTALL_SCRIPT="$installer" \
    FAKE_CURL_LOCAL_PLAN="${WORK_DIR}/local.plan" FAKE_CURL_LOCAL_COUNTER="${WORK_DIR}/local.count" \
    FAKE_CURL_REMOTE_PLAN="${WORK_DIR}/remote.plan" FAKE_CURL_REMOTE_COUNTER="${WORK_DIR}/remote.count" \
    bash -c "
      source '$SCRIPT'
      OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
      OPENBRAIN_DS_SYSTEMD_DIR='${SYSTEMD_DIR}'
      cmd_apply v0.8.0
    "
  [ "$status" -eq 16 ]
  [[ "$output" == *"is live and healthy, but enabling it for boot failed"* ]]
  [[ "$output" == *"will NOT start after a reboot"* ]]
}

# --- first-cutover recovery: restore the --user path on health failure ---

@test "cmd_apply first-cutover recovery: system health check fails, restores the --user path (exit 15)" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  write_fake_sudo "$fake_bin"
  local systemctl_log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$systemctl_log"
  write_fake_curl "$fake_bin"
  local installer="${WORK_DIR}/fake-install-release.sh"
  local installer_log="${WORK_DIR}/installer.log"
  write_fake_install_release "$installer" "$installer_log"
  write_user_unit_file "$USER_SYSTEMD_DIR" openbrain-web

  # First local check (system cutover) fails; the second (restore_user_path's
  # own confirmation probe) passes. No remote calls happen: run_health_check
  # never queries remote after a local failure.
  curl_plan "${WORK_DIR}/local.plan" fail ok

  run env "${DS_ENV[@]}" PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0 \
    FAKE_LINGER_STATE=yes FAKE_USER_ACTIVE_openbrain_web_service=active \
    OPENBRAIN_DS_INSTALL_SCRIPT="$installer" \
    FAKE_CURL_LOCAL_PLAN="${WORK_DIR}/local.plan" FAKE_CURL_LOCAL_COUNTER="${WORK_DIR}/local.count" \
    bash -c "
      source '$SCRIPT'
      OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
      OPENBRAIN_DS_SYSTEMD_DIR='${SYSTEMD_DIR}'
      cmd_apply v0.8.0
    "
  [ "$status" -eq 15 ]
  [[ "$output" == *"restoring the --user path"* ]]
  [[ "$output" == *"restored the --user path"* ]]
  [[ "$output" == *"did NOT go live"* ]]

  run cat "$systemctl_log"
  # The system unit is freed (stopped/disabled) before the --user unit is
  # restarted, and the --user unit is re-enabled/started.
  [[ "$output" == *"disable --now openbrain-web.service"* ]]
  [[ "$output" == *"--user enable --now openbrain-web.service"* ]]

  run cat "$FAKE_LOGINCTL_LOG"
  [[ "$output" == *"enable-linger testuser"* ]]
}

@test "cmd_apply first-cutover recovery FAILS: system cutover fails and the --user restore does not come back healthy (exit 14)" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  write_fake_sudo "$fake_bin"
  local systemctl_log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$systemctl_log"
  write_fake_curl "$fake_bin"
  local installer="${WORK_DIR}/fake-install-release.sh"
  local installer_log="${WORK_DIR}/installer.log"
  write_fake_install_release "$installer" "$installer_log"
  write_user_unit_file "$USER_SYSTEMD_DIR" openbrain-web

  # Both the system cutover's health check AND the post-restore confirmation
  # probe fail: neither model is serving.
  curl_plan "${WORK_DIR}/local.plan" fail fail

  run env "${DS_ENV[@]}" PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0 \
    FAKE_USER_ACTIVE_openbrain_web_service=active \
    OPENBRAIN_DS_INSTALL_SCRIPT="$installer" \
    FAKE_CURL_LOCAL_PLAN="${WORK_DIR}/local.plan" FAKE_CURL_LOCAL_COUNTER="${WORK_DIR}/local.count" \
    bash -c "
      source '$SCRIPT'
      OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
      OPENBRAIN_DS_SYSTEMD_DIR='${SYSTEMD_DIR}'
      cmd_apply v0.8.0
    "
  [ "$status" -eq 14 ]
  [[ "$output" == *"may be DOWN"* ]]
  [[ "$output" == *"Manual intervention required"* ]]
}

@test "cmd_apply steady-state repoint still rolls back the binary version on health failure (exit 10), unaffected by the new --user recovery branch" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  write_fake_sudo "$fake_bin"
  local systemctl_log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$systemctl_log"
  write_fake_curl "$fake_bin"
  local installer="${WORK_DIR}/fake-install-release.sh"
  local installer_log="${WORK_DIR}/installer.log"
  write_fake_install_release "$installer" "$installer_log"
  # No --user unit files present: steady-state.

  env OPENBRAIN_REPO=windingriverholdings/openbrain OPENBRAIN_INSTALL_DIR="$INSTALL_DIR" "$installer" v0.7.0
  curl_plan "${WORK_DIR}/local.plan" fail ok
  curl_plan "${WORK_DIR}/remote.plan" 200 200

  run env "${DS_ENV[@]}" PATH="$fake_bin:$PATH" OPENBRAIN_DS_EUID=0 \
    OPENBRAIN_DS_INSTALL_SCRIPT="$installer" \
    FAKE_CURL_LOCAL_PLAN="${WORK_DIR}/local.plan" FAKE_CURL_LOCAL_COUNTER="${WORK_DIR}/local.count" \
    FAKE_CURL_REMOTE_PLAN="${WORK_DIR}/remote.plan" FAKE_CURL_REMOTE_COUNTER="${WORK_DIR}/remote.count" \
    bash -c "
      source '$SCRIPT'
      OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
      OPENBRAIN_DS_SYSTEMD_DIR='${SYSTEMD_DIR}'
      cmd_apply v0.8.0
    "
  [ "$status" -eq 10 ]
  [[ "$output" == *"automatic rollback to 'v0.7.0' succeeded"* ]]
}

# --- dry-run mentions the new steps (Gap 1 / Gap 2 / config preflight) ----

@test "cmd_dry_run mentions the config preflight, base-unit install, --user teardown, and linger-disable steps" {
  local empty_path="${WORK_DIR}/empty-path"
  mkdir -p "$empty_path"
  ln -s "$(command -v bash)" "${empty_path}/bash"
  ln -s "$(command -v cat)" "${empty_path}/cat"

  run env PATH="$empty_path" "${DS_ENV[@]}" bash -c "
    source '$SCRIPT'
    OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
    OPENBRAIN_DS_SYSTEMD_DIR='${SYSTEMD_DIR}'
    OPENBRAIN_DS_DEPLOY_DIR='${DEPLOY_DIR}'
    cmd_dry_run v0.8.0
  "
  [ "$status" -eq 0 ]
  [[ "$output" == *"EnvironmentFile exists"* ]]
  [[ "$output" == *"${ENV_FILE}"* ]]
  [[ "$output" == *"base system units"* ]]
  [[ "$output" == *"--user disable --now"* ]]
  [[ "$output" == *"disable-linger"* ]]
  [[ "$output" == *"restore the --user path"* ]]
  [[ "$output" == *"enable"* ]]
}
