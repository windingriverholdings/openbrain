#!/usr/bin/env bats
# Unit tests for scripts/repoint-unit.sh (OB-063).
#
# ABSOLUTE SAFETY: every test in this file mocks systemctl and curl. No test
# invokes the real systemd user manager or makes a real network call. Fake
# binaries are placed in a per-test scratch PATH; several tests replace PATH
# entirely (not merely prepend to it) specifically to prove no test can fall
# through to a real systemctl/curl on the host running the suite.
#
# Every function call goes through `run bash -c "source '$SCRIPT'; ..."` so
# each test gets a fresh subshell: the script's own `set -euo pipefail` never
# bleeds into bats' own control flow.

setup() {
  SCRIPT="${BATS_TEST_DIRNAME}/../scripts/repoint-unit.sh"
  WORK_DIR="$(mktemp -d)"
  SYSTEMD_DIR="${WORK_DIR}/systemd-user"
  INSTALL_DIR="${WORK_DIR}/install"
  mkdir -p "$SYSTEMD_DIR" "$INSTALL_DIR"
}

teardown() {
  rm -rf "$WORK_DIR"
}

# --- fixture helpers ---------------------------------------------------

# write_fake_binary creates an executable stub at path that prints version
# for `--version` and fails otherwise, matching the Phase-1 self-identifying
# binary contract that current_installed_version relies on.
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

# write_fake_systemctl writes a systemctl stub to dir that logs every
# invocation (one line per call, space-joined args) to log_file, and
# reports each unit's active/inactive state from FAKE_ACTIVE_<unit> env
# vars (unit names with hyphens mapped to underscores for the var name).
write_fake_systemctl() {
  local dir="$1" log_file="$2"
  cat > "${dir}/systemctl" <<EOF
#!/usr/bin/env bash
echo "\$*" >> "${log_file}"
shift  # drop the leading --user
case "\${1:-}" in
  daemon-reload)
    exit "\${FAKE_SYSTEMCTL_RELOAD_EXIT:-0}"
    ;;
  restart)
    unit="\$2"
    varname="FAKE_SYSTEMCTL_RESTART_EXIT_\${unit//-/_}"
    exit "\${!varname:-\${FAKE_SYSTEMCTL_RESTART_EXIT:-0}}"
    ;;
  is-active)
    unit="\${@: -1}"
    varname="FAKE_ACTIVE_\${unit//-/_}"
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

# write_fake_curl writes a curl stub to dir that distinguishes the remote
# MCP check from the local health check by the presence of `-w` (only the
# remote call passes -w '%{http_code}'), and walks a sequence of outcomes
# from a plan file so a test can script "fails on the first call, succeeds
# on the second" (the rollback-recovery scenario). Each call consumes the
# next line of its plan file (local: "ok"/"fail"; remote:
# "200"/"500"/"unreachable"); once the plan runs out, the last line repeats.
#
# The remote branch mirrors REAL curl semantics precisely, including how
# --fail/-f interacts with -w: a completed request's %{http_code} is still
# reported by -w even on a non-2xx response, but --fail makes curl's own
# EXIT code non-zero (22) for that response, identical to a genuine
# transport failure's non-zero exit. This is deliberate, not an
# oversimplification: an earlier version of this mock always exited 0 for
# any non-"unreachable" line regardless of --fail, which hid a real bug in
# health_check_remote (it originally passed -f and branched on curl's exit
# status alone, so a reachable HTTP 500 was indistinguishable from a
# connection failure). Faithfully modeling --fail's exit-code behavior here
# is what makes the reachable-500 test below actually exercise that bug
# class instead of passing by accident.
write_fake_curl() {
  local dir="$1"
  cat > "${dir}/curl" <<'EOF'
#!/usr/bin/env bash
is_remote=0
has_fail_flag=0
for arg in "$@"; do
  [[ "$arg" == "-w" ]] && is_remote=1
  # Real curl accepts --fail as a standalone long option, but -f is a
  # short option that can be clustered with other short options in one
  # token (e.g. -fsS, -sf): a token starting with a single dash (not the
  # double-dash of a long option) whose letters include an "f" is
  # fail-mode, exactly like real curl's getopt-style short-flag parsing.
  # A version of this mock that only matched the standalone "-f"/"--fail"
  # token never flagged the ORIGINAL buggy health_check_remote's
  # `curl -fsS ...` call, so the reachable-500 regression tests passed
  # even against the unfixed code: a false-green, not a real regression
  # guard.
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
      # A 2xx response: -w prints the code and curl exits 0 regardless of
      # --fail.
      printf '%s' "$line"
      exit 0
      ;;
    *)
      # A completed request with a non-2xx status (e.g. 500, 404). Real
      # curl's -w output still reports the actual code even under --fail;
      # --fail only makes curl's own exit code non-zero (22) for that
      # response. See the write_fake_curl comment above for why this
      # distinction matters.
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

# curl_plan writes a plan file with one outcome per line for write_fake_curl
# to walk through in order.
curl_plan() {
  local path="$1"
  shift
  printf '%s\n' "$@" > "$path"
}

# write_fake_install_release writes a stub standing in for the Phase-2
# installer (scripts/install-release.sh). It logs every invocation (repo,
# install dir, version) to a log file and "installs" the four service
# binaries by writing self-identifying stubs at the requested version, so
# these tests never re-exercise install-release.sh's own (already covered)
# download/checksum logic.
write_fake_install_release() {
  local path="$1" log_file="$2"
  cat > "$path" <<EOF
#!/usr/bin/env bash
version="\$1"
echo "\${OPENBRAIN_REPO}|\${OPENBRAIN_INSTALL_DIR}|\${version}" >> "${log_file}"
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

# --- drop_in_content / drop_in_path --------------------------------------

@test "drop_in_content clears then sets ExecStart to the target binary path" {
  run bash -c "source '$SCRIPT'; drop_in_content /usr/local/bin/openbrain-web"
  [ "$status" -eq 0 ]
  [[ "$output" == *"[Service]"* ]]
  [[ "$output" == *$'ExecStart=\nExecStart=/usr/local/bin/openbrain-web'* ]]
}

@test "drop_in_path builds the <unit>.service.d/override.conf path" {
  run bash -c "source '$SCRIPT'; drop_in_path /home/x/.config/systemd/user openbrain-web"
  [ "$status" -eq 0 ]
  [ "$output" = "/home/x/.config/systemd/user/openbrain-web.service.d/override.conf" ]
}

# --- write_drop_in ---------------------------------------------------------

@test "write_drop_in creates the drop-in directory and file with the right content" {
  run bash -c "source '$SCRIPT'; write_drop_in '${SYSTEMD_DIR}' openbrain-web '${INSTALL_DIR}'"
  [ "$status" -eq 0 ]

  local dropin="${SYSTEMD_DIR}/openbrain-web.service.d/override.conf"
  [ -f "$dropin" ]
  run cat "$dropin"
  [[ "$output" == *"ExecStart=${INSTALL_DIR}/openbrain-web"* ]]
}

@test "write_drop_in does not rewrite the file when content already matches (idempotent)" {
  bash -c "source '$SCRIPT'; write_drop_in '${SYSTEMD_DIR}' openbrain-web '${INSTALL_DIR}'"
  local dropin="${SYSTEMD_DIR}/openbrain-web.service.d/override.conf"
  local before
  before="$(stat -c %Y "$dropin")"
  sleep 1

  run bash -c "source '$SCRIPT'; write_drop_in '${SYSTEMD_DIR}' openbrain-web '${INSTALL_DIR}'"
  [ "$status" -eq 0 ]
  [[ "$output" == *"already matches the target"* ]]

  local after
  after="$(stat -c %Y "$dropin")"
  [ "$before" = "$after" ]
}

@test "write_drop_in rewrites the file when the target binary path changed" {
  bash -c "source '$SCRIPT'; write_drop_in '${SYSTEMD_DIR}' openbrain-web '${INSTALL_DIR}'"
  local other_dir="${WORK_DIR}/install-2"
  mkdir -p "$other_dir"

  run bash -c "source '$SCRIPT'; write_drop_in '${SYSTEMD_DIR}' openbrain-web '${other_dir}'"
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

  run env PATH="${fake_bin}:${PATH}" bash -c "source '$SCRIPT'; repoint_and_restart '${SYSTEMD_DIR}' '${INSTALL_DIR}'"
  [ "$status" -eq 0 ]

  for unit in openbrain-web openbrain-telegram openbrain-slack openbrain-watchd; do
    [ -f "${SYSTEMD_DIR}/${unit}.service.d/override.conf" ]
  done

  run cat "$log"
  [ "$(grep -c '^--user daemon-reload$' "$log")" -eq 1 ]
  [ "$(grep -c '^--user restart openbrain-web$' "$log")" -eq 1 ]

  # daemon-reload happens before the restart in the recorded order.
  local reload_line restart_line
  reload_line="$(grep -n '^--user daemon-reload$' "$log" | head -1 | cut -d: -f1)"
  restart_line="$(grep -n '^--user restart openbrain-web$' "$log" | head -1 | cut -d: -f1)"
  [ "$reload_line" -lt "$restart_line" ]
}

@test "repoint_and_restart restarts an already-active secondary unit but leaves an inactive one alone" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  local log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$log"

  run env PATH="${fake_bin}:${PATH}" FAKE_ACTIVE_openbrain_telegram=active \
    bash -c "source '$SCRIPT'; repoint_and_restart '${SYSTEMD_DIR}' '${INSTALL_DIR}'"
  [ "$status" -eq 0 ]

  run cat "$log"
  [[ "$output" == *"restart openbrain-telegram"* ]]
  [[ "$output" != *"restart openbrain-slack"* ]]
  [[ "$output" != *"restart openbrain-watchd"* ]]
}

@test "repoint_and_restart fails closed and stops when daemon-reload fails" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  local log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$log"

  run env PATH="${fake_bin}:${PATH}" FAKE_SYSTEMCTL_RELOAD_EXIT=1 \
    bash -c "source '$SCRIPT'; repoint_and_restart '${SYSTEMD_DIR}' '${INSTALL_DIR}'"
  [ "$status" -ne 0 ]
  [[ "$output" == *"daemon-reload failed"* ]]

  run cat "$log"
  [[ "$output" != *"restart"* ]]
}

@test "repoint_and_restart fails closed when the primary unit restart fails" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  local log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$log"

  run env PATH="${fake_bin}:${PATH}" FAKE_SYSTEMCTL_RESTART_EXIT_openbrain_web=1 \
    bash -c "source '$SCRIPT'; repoint_and_restart '${SYSTEMD_DIR}' '${INSTALL_DIR}'"
  [ "$status" -ne 0 ]
  [[ "$output" == *"restart failed for openbrain-web"* ]]
}

@test "repoint_and_restart fails closed with a distinct message when an already-active secondary unit's restart fails" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  local log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$log"

  run env PATH="${fake_bin}:${PATH}" \
    FAKE_ACTIVE_openbrain_telegram=active \
    FAKE_SYSTEMCTL_RESTART_EXIT_openbrain_telegram=1 \
    bash -c "source '$SCRIPT'; repoint_and_restart '${SYSTEMD_DIR}' '${INSTALL_DIR}'"
  [ "$status" -ne 0 ]
  [[ "$output" == *"restart failed for already-active openbrain-telegram"* ]]

  # Distinct from the primary-unit restart failure above: this fails at a
  # later step, so every drop-in was already written and the primary unit's
  # own restart already succeeded before this failure surfaced.
  for unit in openbrain-web openbrain-telegram openbrain-slack openbrain-watchd; do
    [ -f "${SYSTEMD_DIR}/${unit}.service.d/override.conf" ]
  done
  run cat "$log"
  [[ "$output" == *"restart openbrain-web"* ]]
}

@test "repoint_and_restart fails closed with a distinct message when a drop-in write fails" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  local log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$log"

  chmod 555 "$SYSTEMD_DIR"

  run env PATH="${fake_bin}:${PATH}" \
    bash -c "source '$SCRIPT'; repoint_and_restart '${SYSTEMD_DIR}' '${INSTALL_DIR}'"
  chmod 755 "$SYSTEMD_DIR"

  [ "$status" -ne 0 ]
  [[ "$output" == *"failed to create"* ]]

  # Distinct from the daemon-reload/restart failure messages: no systemctl
  # command was ever reached because the drop-in write failed first.
  [ ! -f "$log" ]
}

# --- health_check_local / health_check_remote / run_health_check ---------

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

@test "health_check_local fails when curl fails" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  write_fake_curl "$fake_bin"
  curl_plan "${WORK_DIR}/local.plan" fail

  run env PATH="${fake_bin}:${PATH}" \
    FAKE_CURL_LOCAL_PLAN="${WORK_DIR}/local.plan" FAKE_CURL_LOCAL_COUNTER="${WORK_DIR}/local.count" \
    bash -c "source '$SCRIPT'; health_check_local http://127.0.0.1:10203/health"
  [ "$status" -ne 0 ]
}

@test "health_check_remote returns 0 on HTTP 200" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  write_fake_curl "$fake_bin"
  curl_plan "${WORK_DIR}/remote.plan" 200

  run env PATH="${fake_bin}:${PATH}" \
    FAKE_CURL_REMOTE_PLAN="${WORK_DIR}/remote.plan" FAKE_CURL_REMOTE_COUNTER="${WORK_DIR}/remote.count" \
    bash -c "source '$SCRIPT'; health_check_remote https://openbrain.wr-s.net/mcp"
  [ "$status" -eq 0 ]
}

@test "health_check_remote returns 1 on a reachable non-200 status" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  write_fake_curl "$fake_bin"
  curl_plan "${WORK_DIR}/remote.plan" 500

  run env PATH="${fake_bin}:${PATH}" \
    FAKE_CURL_REMOTE_PLAN="${WORK_DIR}/remote.plan" FAKE_CURL_REMOTE_COUNTER="${WORK_DIR}/remote.count" \
    bash -c "source '$SCRIPT'; health_check_remote https://openbrain.wr-s.net/mcp"
  [ "$status" -eq 1 ]
}

@test "health_check_remote returns 2 when unreachable (curl connect failure)" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  write_fake_curl "$fake_bin"
  curl_plan "${WORK_DIR}/remote.plan" unreachable

  run env PATH="${fake_bin}:${PATH}" \
    FAKE_CURL_REMOTE_PLAN="${WORK_DIR}/remote.plan" FAKE_CURL_REMOTE_COUNTER="${WORK_DIR}/remote.count" \
    bash -c "source '$SCRIPT'; health_check_remote https://openbrain.wr-s.net/mcp"
  [ "$status" -eq 2 ]
}

@test "health_check_remote distinguishes a reachable HTTP 500 (genuine failure, status 1) from an unreachable transport failure (status 2)" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  write_fake_curl "$fake_bin"
  curl_plan "${WORK_DIR}/remote.plan" 500

  run env PATH="${fake_bin}:${PATH}" \
    FAKE_CURL_REMOTE_PLAN="${WORK_DIR}/remote.plan" FAKE_CURL_REMOTE_COUNTER="${WORK_DIR}/remote.count" \
    bash -c "source '$SCRIPT'; health_check_remote https://openbrain.wr-s.net/mcp"

  # A reachable, completed request that answered with a real HTTP error is
  # NOT the same defect class as curl failing to connect at all: status 1
  # (genuine failure), never status 2 (remote-unreachable). This is the
  # exact distinction health_check_remote's docstring promises; a version
  # that passed -f to curl and branched on curl's own exit status conflated
  # the two, since curl -f's exit code for a 500 is indistinguishable from
  # its exit code for a connection refusal.
  [ "$status" -eq 1 ]
}

@test "run_health_check returns HEALTH_OK when both tiers pass" {
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
}

@test "run_health_check distinguishes local-healthy-remote-unreachable from a genuine failure (no rollback trigger)" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  write_fake_curl "$fake_bin"
  curl_plan "${WORK_DIR}/local.plan" ok
  curl_plan "${WORK_DIR}/remote.plan" unreachable

  run env PATH="${fake_bin}:${PATH}" \
    FAKE_CURL_LOCAL_PLAN="${WORK_DIR}/local.plan" FAKE_CURL_LOCAL_COUNTER="${WORK_DIR}/local.count" \
    FAKE_CURL_REMOTE_PLAN="${WORK_DIR}/remote.plan" FAKE_CURL_REMOTE_COUNTER="${WORK_DIR}/remote.count" \
    bash -c "source '$SCRIPT'; run_health_check http://127.0.0.1:10203/health https://openbrain.wr-s.net/mcp"
  [ "$status" -eq 2 ]
  [[ "$output" == *"not a genuine failure"* ]]
}

@test "run_health_check treats a local failure as genuine regardless of remote" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  write_fake_curl "$fake_bin"
  curl_plan "${WORK_DIR}/local.plan" fail
  curl_plan "${WORK_DIR}/remote.plan" 200

  run env PATH="${fake_bin}:${PATH}" \
    FAKE_CURL_LOCAL_PLAN="${WORK_DIR}/local.plan" FAKE_CURL_LOCAL_COUNTER="${WORK_DIR}/local.count" \
    FAKE_CURL_REMOTE_PLAN="${WORK_DIR}/remote.plan" FAKE_CURL_REMOTE_COUNTER="${WORK_DIR}/remote.count" \
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

# --- dry-run: writes nothing, invokes neither systemctl nor curl ---------

@test "dry_run_plan prints the drop-in contents and command sequence without any PATH tool available" {
  local empty_path="${WORK_DIR}/empty-path"
  mkdir -p "$empty_path"
  ln -s "$(command -v bash)" "${empty_path}/bash"
  ln -s "$(command -v cat)" "${empty_path}/cat"

  run env PATH="$empty_path" bash -c "source '$SCRIPT'; dry_run_plan '${SYSTEMD_DIR}' '${INSTALL_DIR}' v0.8.0"
  [ "$status" -eq 0 ]
  [[ "$output" == *"DRY RUN"* ]]
  [[ "$output" == *"target version: v0.8.0"* ]]
  [[ "$output" == *"ExecStart=${INSTALL_DIR}/openbrain-web"* ]]
  [[ "$output" == *"systemctl --user daemon-reload"* ]]
  [[ "$output" == *"systemctl --user restart openbrain-web"* ]]

  # Nothing was written: dry-run must not create the drop-in files.
  [ ! -e "${SYSTEMD_DIR}/openbrain-web.service.d/override.conf" ]
}

@test "cmd_apply --dry-run reports the plan and touches nothing, with no systemctl/curl on PATH at all" {
  local empty_path="${WORK_DIR}/empty-path"
  mkdir -p "$empty_path"
  ln -s "$(command -v bash)" "${empty_path}/bash"
  ln -s "$(command -v cat)" "${empty_path}/cat"

  run env PATH="$empty_path" bash -c "source '$SCRIPT'; cmd_apply 1 v0.8.0"
  [ "$status" -eq 0 ]
  [[ "$output" == *"DRY RUN"* ]]
  [ ! -e "${SYSTEMD_DIR}/openbrain-web.service.d/override.conf" ]
  [ ! -e "${INSTALL_DIR}/openbrain-web" ]
}

@test "cmd_rollback requires an explicit version" {
  run bash -c "source '$SCRIPT'; cmd_rollback 0 ''"
  [ "$status" -ne 0 ]
  [[ "$output" == *"requires an explicit VERSION"* ]]
}

@test "cmd_rollback --dry-run reports the plan and touches nothing, with no systemctl/curl on PATH at all" {
  local empty_path="${WORK_DIR}/empty-path"
  mkdir -p "$empty_path"
  ln -s "$(command -v bash)" "${empty_path}/bash"
  ln -s "$(command -v cat)" "${empty_path}/cat"

  run env PATH="$empty_path" bash -c "
    source '$SCRIPT'
    OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
    OPENBRAIN_SYSTEMD_USER_DIR='${SYSTEMD_DIR}'
    cmd_rollback 1 v0.6.0
  "
  [ "$status" -eq 0 ]
  [[ "$output" == *"DRY RUN"* ]]
  [[ "$output" == *"target version: v0.6.0"* ]]
  [ ! -e "${SYSTEMD_DIR}/openbrain-web.service.d/override.conf" ]
  [ ! -e "${INSTALL_DIR}/openbrain-web" ]
}

# --- cmd_apply: happy path, idempotency, and rollback-on-failure ---------

@test "cmd_apply aborts before any unit change when the initial install fails" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  local systemctl_log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$systemctl_log"
  write_fake_curl "$fake_bin"
  local installer="${WORK_DIR}/fake-install-release.sh"
  local installer_log="${WORK_DIR}/installer.log"
  write_fake_install_release "$installer" "$installer_log"

  run env PATH="$fake_bin:$PATH" \
    OPENBRAIN_INSTALL_RELEASE_SCRIPT="$installer" \
    FAKE_INSTALL_RELEASE_EXIT=1 \
    bash -c "
      source '$SCRIPT'
      OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
      OPENBRAIN_SYSTEMD_USER_DIR='${SYSTEMD_DIR}'
      cmd_apply 0 v0.8.0
    "

  [ "$status" -ne 0 ]
  [[ "$output" == *"failed to install version v0.8.0; aborting before any unit change"* ]]

  # No unit change happened: no drop-in was ever written, and systemctl was
  # never invoked (the log file is only created once systemctl is called).
  [ ! -e "${SYSTEMD_DIR}/openbrain-web.service.d/override.conf" ]
  [ ! -f "$systemctl_log" ]
}

@test "cmd_apply happy path: installs, repoints, restarts, and passes health" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  local systemctl_log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$systemctl_log"
  write_fake_curl "$fake_bin"
  local installer="${WORK_DIR}/fake-install-release.sh"
  local installer_log="${WORK_DIR}/installer.log"
  write_fake_install_release "$installer" "$installer_log"
  curl_plan "${WORK_DIR}/local.plan" ok
  curl_plan "${WORK_DIR}/remote.plan" 200

  run env PATH="$fake_bin:$PATH" \
    OPENBRAIN_INSTALL_RELEASE_SCRIPT="$installer" \
    FAKE_CURL_LOCAL_PLAN="${WORK_DIR}/local.plan" FAKE_CURL_LOCAL_COUNTER="${WORK_DIR}/local.count" \
    FAKE_CURL_REMOTE_PLAN="${WORK_DIR}/remote.plan" FAKE_CURL_REMOTE_COUNTER="${WORK_DIR}/remote.count" \
    bash -c "
      source '$SCRIPT'
      OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
      OPENBRAIN_SYSTEMD_USER_DIR='${SYSTEMD_DIR}'
      cmd_apply 0 v0.8.0
    "
  [ "$status" -eq 0 ]
  [[ "$output" == *"apply of v0.8.0 succeeded"* ]]

  run cat "$installer_log"
  [ "$output" = "windingriverholdings/openbrain|${INSTALL_DIR}|v0.8.0" ]

  run cat "${SYSTEMD_DIR}/openbrain-web.service.d/override.conf"
  [[ "$output" == *"ExecStart=${INSTALL_DIR}/openbrain-web"* ]]

  run cat "$systemctl_log"
  [[ "$output" == *"daemon-reload"* ]]
  [[ "$output" == *"restart openbrain-web"* ]]
}

@test "cmd_apply is idempotent: a second run at the same version writes no new drop-in diff" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  local systemctl_log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$systemctl_log"
  write_fake_curl "$fake_bin"
  local installer="${WORK_DIR}/fake-install-release.sh"
  local installer_log="${WORK_DIR}/installer.log"
  write_fake_install_release "$installer" "$installer_log"
  curl_plan "${WORK_DIR}/local.plan" ok ok
  curl_plan "${WORK_DIR}/remote.plan" 200 200

  local common_env=(
    PATH="$fake_bin:$PATH"
    OPENBRAIN_INSTALL_RELEASE_SCRIPT="$installer"
    FAKE_CURL_LOCAL_PLAN="${WORK_DIR}/local.plan" FAKE_CURL_LOCAL_COUNTER="${WORK_DIR}/local.count"
    FAKE_CURL_REMOTE_PLAN="${WORK_DIR}/remote.plan" FAKE_CURL_REMOTE_COUNTER="${WORK_DIR}/remote.count"
  )
  local apply_cmd="
    source '$SCRIPT'
    OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
    OPENBRAIN_SYSTEMD_USER_DIR='${SYSTEMD_DIR}'
    cmd_apply 0 v0.8.0
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

@test "cmd_apply automatically rolls back to the previous version on a genuine health-check failure and recovers" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  local systemctl_log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$systemctl_log"
  write_fake_curl "$fake_bin"
  local installer="${WORK_DIR}/fake-install-release.sh"
  local installer_log="${WORK_DIR}/installer.log"
  write_fake_install_release "$installer" "$installer_log"

  # Seed a previously-installed version so cmd_apply has a rollback target.
  OPENBRAIN_INSTALL_DIR="$INSTALL_DIR" bash -c "
    source '$SCRIPT'
    install_version windingriverholdings/openbrain '${INSTALL_DIR}' v0.7.0
  " 2>/dev/null || true
  env OPENBRAIN_REPO=windingriverholdings/openbrain OPENBRAIN_INSTALL_DIR="$INSTALL_DIR" "$installer" v0.7.0

  # First health check (for the new v0.8.0) fails locally; the second health
  # check (after rollback to v0.7.0) passes. health_check_remote is never
  # reached on the first pass because a local failure is genuine regardless
  # of remote, so only one remote-plan line is needed.
  curl_plan "${WORK_DIR}/local.plan" fail ok
  curl_plan "${WORK_DIR}/remote.plan" 200 200

  run env PATH="$fake_bin:$PATH" \
    OPENBRAIN_INSTALL_RELEASE_SCRIPT="$installer" \
    FAKE_CURL_LOCAL_PLAN="${WORK_DIR}/local.plan" FAKE_CURL_LOCAL_COUNTER="${WORK_DIR}/local.count" \
    FAKE_CURL_REMOTE_PLAN="${WORK_DIR}/remote.plan" FAKE_CURL_REMOTE_COUNTER="${WORK_DIR}/remote.count" \
    bash -c "
      source '$SCRIPT'
      OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
      OPENBRAIN_SYSTEMD_USER_DIR='${SYSTEMD_DIR}'
      cmd_apply 0 v0.8.0
    "

  # apply itself still reports failure: v0.8.0 never became healthy, even
  # though the automatic rollback to v0.7.0 succeeded.
  [ "$status" -ne 0 ]
  [[ "$output" == *"genuine health-check failure at v0.8.0"* ]]
  [[ "$output" == *"attempting automatic rollback"* ]]
  [[ "$output" == *"automatic rollback to v0.7.0 succeeded"* ]]

  run cat "$installer_log"
  [[ "$output" == *"|v0.7.0"* ]]
  [[ "$output" == *"|v0.8.0"* ]]
  # v0.7.0 (seed) then v0.8.0 (apply target) then v0.7.0 again (the rollback
  # reinstall): three install_version invocations in that order.
  [ "$(echo "$output" | wc -l)" -eq 3 ]
  [ "$(echo "$output" | sed -n '3p' | awk -F'|' '{print $3}')" = "v0.7.0" ]
}

@test "cmd_apply treats a reachable remote HTTP 500 as a genuine failure and triggers automatic rollback (not a benign remote-unreachable no-op)" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  local systemctl_log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$systemctl_log"
  write_fake_curl "$fake_bin"
  local installer="${WORK_DIR}/fake-install-release.sh"
  local installer_log="${WORK_DIR}/installer.log"
  write_fake_install_release "$installer" "$installer_log"

  env OPENBRAIN_REPO=windingriverholdings/openbrain OPENBRAIN_INSTALL_DIR="$INSTALL_DIR" "$installer" v0.7.0

  # Local passes both times; the remote check is what fails: a reachable
  # HTTP 500 on the first attempt (genuine failure), then a healthy 200
  # after the automatic rollback reinstalls v0.7.0. This is the exact
  # scenario Wren's finding covers: a --fail-based remote check would have
  # misread the 500 as HEALTH_REMOTE_UNREACHABLE and never rolled back.
  curl_plan "${WORK_DIR}/local.plan" ok ok
  curl_plan "${WORK_DIR}/remote.plan" 500 200

  run env PATH="$fake_bin:$PATH" \
    OPENBRAIN_INSTALL_RELEASE_SCRIPT="$installer" \
    FAKE_CURL_LOCAL_PLAN="${WORK_DIR}/local.plan" FAKE_CURL_LOCAL_COUNTER="${WORK_DIR}/local.count" \
    FAKE_CURL_REMOTE_PLAN="${WORK_DIR}/remote.plan" FAKE_CURL_REMOTE_COUNTER="${WORK_DIR}/remote.count" \
    bash -c "
      source '$SCRIPT'
      OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
      OPENBRAIN_SYSTEMD_USER_DIR='${SYSTEMD_DIR}'
      cmd_apply 0 v0.8.0
    "

  [ "$status" -ne 0 ]
  [[ "$output" == *"genuine health-check failure at v0.8.0"* ]]
  [[ "$output" == *"attempting automatic rollback"* ]]
  [[ "$output" == *"automatic rollback to v0.7.0 succeeded"* ]]

  run cat "$installer_log"
  # v0.7.0 (seed) then v0.8.0 (apply target) then v0.7.0 again (the rollback
  # reinstall triggered by the 500): rollback genuinely ran, it was not
  # skipped as a benign remote-unreachable case.
  [ "$(echo "$output" | wc -l)" -eq 3 ]
  [ "$(echo "$output" | sed -n '3p' | awk -F'|' '{print $3}')" = "v0.7.0" ]
}

@test "cmd_apply surfaces a loud actionable error when the automatic rollback itself also fails" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  local systemctl_log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$systemctl_log"
  write_fake_curl "$fake_bin"
  local installer="${WORK_DIR}/fake-install-release.sh"
  local installer_log="${WORK_DIR}/installer.log"
  write_fake_install_release "$installer" "$installer_log"

  env OPENBRAIN_REPO=windingriverholdings/openbrain OPENBRAIN_INSTALL_DIR="$INSTALL_DIR" "$installer" v0.7.0

  # Both the initial apply AND the post-rollback recheck fail locally: the
  # tool must report a distinct rollback-failed error, not loop.
  curl_plan "${WORK_DIR}/local.plan" fail fail
  curl_plan "${WORK_DIR}/remote.plan" 200 200

  run env PATH="$fake_bin:$PATH" \
    OPENBRAIN_INSTALL_RELEASE_SCRIPT="$installer" \
    FAKE_CURL_LOCAL_PLAN="${WORK_DIR}/local.plan" FAKE_CURL_LOCAL_COUNTER="${WORK_DIR}/local.count" \
    FAKE_CURL_REMOTE_PLAN="${WORK_DIR}/remote.plan" FAKE_CURL_REMOTE_COUNTER="${WORK_DIR}/remote.count" \
    bash -c "
      source '$SCRIPT'
      OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
      OPENBRAIN_SYSTEMD_USER_DIR='${SYSTEMD_DIR}'
      cmd_apply 0 v0.8.0
    "

  [ "$status" -ne 0 ]
  [[ "$output" == *"automatic rollback to v0.7.0 FAILED"* ]]
  [[ "$output" == *"manual intervention required"* ]]
  # Exactly one rollback attempt: no retry loop.
  [ "$(echo "$output" | grep -c "attempting automatic rollback")" -eq 1 ]
}

@test "cmd_apply refuses to auto-rollback with a clear error when there is no previously-installed version" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  local systemctl_log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$systemctl_log"
  write_fake_curl "$fake_bin"
  local installer="${WORK_DIR}/fake-install-release.sh"
  local installer_log="${WORK_DIR}/installer.log"
  write_fake_install_release "$installer" "$installer_log"

  curl_plan "${WORK_DIR}/local.plan" fail
  curl_plan "${WORK_DIR}/remote.plan" 200

  run env PATH="$fake_bin:$PATH" \
    OPENBRAIN_INSTALL_RELEASE_SCRIPT="$installer" \
    FAKE_CURL_LOCAL_PLAN="${WORK_DIR}/local.plan" FAKE_CURL_LOCAL_COUNTER="${WORK_DIR}/local.count" \
    FAKE_CURL_REMOTE_PLAN="${WORK_DIR}/remote.plan" FAKE_CURL_REMOTE_COUNTER="${WORK_DIR}/remote.count" \
    bash -c "
      source '$SCRIPT'
      OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
      OPENBRAIN_SYSTEMD_USER_DIR='${SYSTEMD_DIR}'
      cmd_apply 0 v0.8.0
    "

  [ "$status" -ne 0 ]
  [[ "$output" == *"no previously-installed version was recorded"* ]]
  # No actual rollback reinstall was attempted: install_version is only
  # ever logged once (the original apply target), never a second time for
  # a rollback that had nowhere to go.
  [ "$(grep -c '|v0.8.0$' "$installer_log")" -eq 1 ]
  [ "$(wc -l < "$installer_log")" -eq 1 ]
}

# --- rollback_to_version: isolated failure branches -----------------------

@test "rollback_to_version fails loud when it cannot even reinstall the prior version" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  local systemctl_log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$systemctl_log"
  write_fake_curl "$fake_bin"
  local installer="${WORK_DIR}/fake-install-release.sh"
  local installer_log="${WORK_DIR}/installer.log"
  write_fake_install_release "$installer" "$installer_log"

  run env PATH="$fake_bin:$PATH" \
    OPENBRAIN_INSTALL_RELEASE_SCRIPT="$installer" \
    FAKE_INSTALL_RELEASE_EXIT=1 \
    bash -c "source '$SCRIPT'; rollback_to_version windingriverholdings/openbrain '${INSTALL_DIR}' '${SYSTEMD_DIR}' http://127.0.0.1:10203/health https://openbrain.wr-s.net/mcp v0.6.0"

  [ "$status" -ne 0 ]
  [[ "$output" == *"failed to reinstall prior version v0.6.0"* ]]

  # The reinstall failed before any unit change was attempted at all.
  [ ! -f "$systemctl_log" ]
}

@test "rollback_to_version fails loud when the reinstall succeeds but repoint/restart fails" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  local systemctl_log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$systemctl_log"
  write_fake_curl "$fake_bin"
  local installer="${WORK_DIR}/fake-install-release.sh"
  local installer_log="${WORK_DIR}/installer.log"
  write_fake_install_release "$installer" "$installer_log"

  run env PATH="$fake_bin:$PATH" \
    OPENBRAIN_INSTALL_RELEASE_SCRIPT="$installer" \
    FAKE_SYSTEMCTL_RELOAD_EXIT=1 \
    bash -c "source '$SCRIPT'; rollback_to_version windingriverholdings/openbrain '${INSTALL_DIR}' '${SYSTEMD_DIR}' http://127.0.0.1:10203/health https://openbrain.wr-s.net/mcp v0.6.0"

  [ "$status" -ne 0 ]
  [[ "$output" == *"failed to repoint/restart during rollback to v0.6.0"* ]]

  # Distinct from the "cannot even reinstall" branch above: the reinstall
  # itself DID run here; it is the unit-change step that failed.
  run cat "$installer_log"
  [ "$output" = "windingriverholdings/openbrain|${INSTALL_DIR}|v0.6.0" ]
}

# --- cmd_rollback: on-demand -----------------------------------------------

@test "cmd_rollback reinstalls the named version, repoints, restarts, and rechecks health" {
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  local systemctl_log="${WORK_DIR}/systemctl.log"
  write_fake_systemctl "$fake_bin" "$systemctl_log"
  write_fake_curl "$fake_bin"
  local installer="${WORK_DIR}/fake-install-release.sh"
  local installer_log="${WORK_DIR}/installer.log"
  write_fake_install_release "$installer" "$installer_log"

  curl_plan "${WORK_DIR}/local.plan" ok
  curl_plan "${WORK_DIR}/remote.plan" 200

  run env PATH="$fake_bin:$PATH" \
    OPENBRAIN_INSTALL_RELEASE_SCRIPT="$installer" \
    FAKE_CURL_LOCAL_PLAN="${WORK_DIR}/local.plan" FAKE_CURL_LOCAL_COUNTER="${WORK_DIR}/local.count" \
    FAKE_CURL_REMOTE_PLAN="${WORK_DIR}/remote.plan" FAKE_CURL_REMOTE_COUNTER="${WORK_DIR}/remote.count" \
    bash -c "
      source '$SCRIPT'
      OPENBRAIN_INSTALL_DIR='${INSTALL_DIR}'
      OPENBRAIN_SYSTEMD_USER_DIR='${SYSTEMD_DIR}'
      cmd_rollback 0 v0.6.5
    "
  [ "$status" -eq 0 ]

  run cat "$installer_log"
  [ "$output" = "windingriverholdings/openbrain|${INSTALL_DIR}|v0.6.5" ]

  run cat "$systemctl_log"
  [[ "$output" == *"daemon-reload"* ]]
  [[ "$output" == *"restart openbrain-web"* ]]
}

# --- main(): argument parsing ------------------------------------------

@test "main rejects an unknown subcommand with usage on stderr and a non-zero exit" {
  run bash "$SCRIPT" bogus v1.0.0
  [ "$status" -ne 0 ]
  [[ "$output" == *"Usage:"* ]]
}

@test "main requires apply's VERSION argument" {
  run bash "$SCRIPT" apply
  [ "$status" -ne 0 ]
  [[ "$output" == *"requires an explicit VERSION"* ]]
}
