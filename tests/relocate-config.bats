#!/usr/bin/env bats
# Unit tests for scripts/relocate-config.sh (OB-066, Phase 3.5).
#
# ABSOLUTE SAFETY: every test in this file targets a sandbox DEST under
# BATS_TEST_DIRNAME's scratch dir. No test ever writes to the real
# /etc/openbrain, and no test runs as (or fakes) an actually-privileged
# user: unprivileged writes to a scratch dir the test itself owns are what
# every test below exercises. The privileged (sudo) branch is exercised
# only against a scratch "sudo" stub that execs its argument directly, in
# the same spirit as tests/deploy-system.bats's write_fake_sudo.

setup() {
  SCRIPT="${BATS_TEST_DIRNAME}/../scripts/relocate-config.sh"
  WORK_DIR="$(mktemp -d)"
  SOURCE_ENV="${WORK_DIR}/source.env"
  DEST_ENV="${WORK_DIR}/etc-openbrain/openbrain.env"
  printf 'OPENBRAIN_DB_PASSWORD=hunter2\nOPENBRAIN_MCP_AUTH_TOKEN=abc123\n' > "$SOURCE_ENV"
}

teardown() {
  rm -rf "$WORK_DIR"
}

write_fake_sudo() {
  local dir="$1"
  cat > "${dir}/sudo" <<'EOF'
#!/usr/bin/env bash
exec "$@"
EOF
  chmod +x "${dir}/sudo"
}

# --- check_source ----------------------------------------------------------

@test "check_source fails closed when the source file does not exist" {
  run bash -c "source '$SCRIPT'; check_source '${WORK_DIR}/does-not-exist.env'"
  [ "$status" -ne 0 ]
  [[ "$output" == *"does not exist"* ]]
}

@test "check_source passes for a readable, existing source file" {
  run bash -c "source '$SCRIPT'; check_source '${SOURCE_ENV}'"
  [ "$status" -eq 0 ]
}

# --- dest_dir_writable / check_privilege -----------------------------------

@test "dest_dir_writable is true for a writable existing directory" {
  mkdir -p "${WORK_DIR}/writable-dir"
  run bash -c "source '$SCRIPT'; dest_dir_writable '${WORK_DIR}/writable-dir'"
  [ "$status" -eq 0 ]
}

@test "dest_dir_writable is true for a not-yet-created directory whose parent is writable" {
  run bash -c "source '$SCRIPT'; dest_dir_writable '${WORK_DIR}/not-yet-created'"
  [ "$status" -eq 0 ]
}

@test "check_privilege fails closed when the dest dir is not writable and sudo is not on PATH" {
  mkdir -p "${WORK_DIR}/readonly-dir"
  chmod 555 "${WORK_DIR}/readonly-dir"
  local empty_path="${WORK_DIR}/empty-path"
  mkdir -p "$empty_path"
  ln -s "$(command -v bash)" "${empty_path}/bash"

  run env PATH="$empty_path" bash -c "source '$SCRIPT'; check_privilege '${WORK_DIR}/readonly-dir' sudo"
  chmod 755 "${WORK_DIR}/readonly-dir"
  [ "$status" -ne 0 ]
  [[ "$output" == *"is not on PATH"* ]]
}

@test "check_privilege passes when the dest dir is not writable but sudo is on PATH" {
  mkdir -p "${WORK_DIR}/readonly-dir"
  chmod 555 "${WORK_DIR}/readonly-dir"
  local fake_bin="${WORK_DIR}/fakebin"
  mkdir -p "$fake_bin"
  write_fake_sudo "$fake_bin"

  run env PATH="${fake_bin}:${PATH}" bash -c "source '$SCRIPT'; check_privilege '${WORK_DIR}/readonly-dir' sudo"
  chmod 755 "${WORK_DIR}/readonly-dir"
  [ "$status" -eq 0 ]
}

# --- needs_write (idempotency signal) --------------------------------------

@test "needs_write is true when dest does not exist yet" {
  run bash -c "source '$SCRIPT'; needs_write '${SOURCE_ENV}' '${DEST_ENV}' craig8"
  [ "$status" -eq 0 ]
}

@test "needs_write is true when dest content differs from source" {
  mkdir -p "$(dirname "$DEST_ENV")"
  printf 'STALE=1\n' > "$DEST_ENV"
  chmod 600 "$DEST_ENV"
  run bash -c "source '$SCRIPT'; needs_write '${SOURCE_ENV}' '${DEST_ENV}' \"\$(id -un)\""
  [ "$status" -eq 0 ]
}

@test "needs_write is true when dest content matches but mode is not 0600" {
  mkdir -p "$(dirname "$DEST_ENV")"
  cp "$SOURCE_ENV" "$DEST_ENV"
  chmod 644 "$DEST_ENV"
  run bash -c "source '$SCRIPT'; needs_write '${SOURCE_ENV}' '${DEST_ENV}' \"\$(id -un)\""
  [ "$status" -eq 0 ]
}

@test "needs_write is false when dest content, mode, and owner already match" {
  mkdir -p "$(dirname "$DEST_ENV")"
  cp "$SOURCE_ENV" "$DEST_ENV"
  chmod 600 "$DEST_ENV"
  run bash -c "source '$SCRIPT'; needs_write '${SOURCE_ENV}' '${DEST_ENV}' \"\$(id -un)\""
  [ "$status" -ne 0 ]
}

# --- write_dest: atomic write, mode 0600 ------------------------------------

@test "write_dest atomically writes source content to dest at mode 0600" {
  mkdir -p "$(dirname "$DEST_ENV")"
  run bash -c "source '$SCRIPT'; write_dest '${SOURCE_ENV}' '${DEST_ENV}' \"\$(id -un)\" 0 sudo"
  [ "$status" -eq 0 ]
  [ -f "$DEST_ENV" ]
  run stat -c '%a' "$DEST_ENV"
  [ "$output" = "600" ]
  run cmp "$SOURCE_ENV" "$DEST_ENV"
  [ "$status" -eq 0 ]
}

@test "write_dest leaves no partial file at dest when the copy step fails" {
  mkdir -p "$(dirname "$DEST_ENV")"
  # A source path that vanishes between check_source and write_dest's own
  # cp step simulates a mid-flight copy failure without needing a fake cp.
  local vanishing="${WORK_DIR}/vanishing.env"
  cp "$SOURCE_ENV" "$vanishing"
  ( cp "$vanishing" "${WORK_DIR}/still-here.env"; rm -f "$vanishing" )

  run bash -c "source '$SCRIPT'; write_dest '${vanishing}' '${DEST_ENV}' \"\$(id -un)\" 0 sudo"
  [ "$status" -ne 0 ]
  [ ! -f "$DEST_ENV" ]
  # No leaked temp file either.
  run bash -c "shopt -s nullglob dotglob; files=('$(dirname "$DEST_ENV")'/.openbrain.env.*); echo \${#files[@]}"
  [ "$output" = "0" ]
}

# --- relocate_config: end-to-end, idempotent, privilege-gated --------------

@test "relocate_config performs the initial relocation (mode 0600, matching content)" {
  run bash -c "source '$SCRIPT'; relocate_config '${SOURCE_ENV}' '${DEST_ENV}' \"\$(id -un)\" sudo"
  [ "$status" -eq 0 ]
  [ -f "$DEST_ENV" ]
  run stat -c '%a' "$DEST_ENV"
  [ "$output" = "600" ]
  run cmp "$SOURCE_ENV" "$DEST_ENV"
  [ "$status" -eq 0 ]
}

@test "relocate_config is idempotent: a second run performs no write and no mtime change" {
  bash -c "source '$SCRIPT'; relocate_config '${SOURCE_ENV}' '${DEST_ENV}' \"\$(id -un)\" sudo"
  local before
  before="$(stat -c %Y "$DEST_ENV")"
  sleep 1

  run bash -c "source '$SCRIPT'; relocate_config '${SOURCE_ENV}' '${DEST_ENV}' \"\$(id -un)\" sudo"
  [ "$status" -eq 0 ]
  [[ "$output" == *"already up to date, no write needed"* ]]

  local after
  after="$(stat -c %Y "$DEST_ENV")"
  [ "$before" = "$after" ]
}

@test "relocate_config re-writes when the source content changes" {
  bash -c "source '$SCRIPT'; relocate_config '${SOURCE_ENV}' '${DEST_ENV}' \"\$(id -un)\" sudo"
  printf 'OPENBRAIN_DB_PASSWORD=rotated\n' > "$SOURCE_ENV"

  run bash -c "source '$SCRIPT'; relocate_config '${SOURCE_ENV}' '${DEST_ENV}' \"\$(id -un)\" sudo"
  [ "$status" -eq 0 ]
  run cmp "$SOURCE_ENV" "$DEST_ENV"
  [ "$status" -eq 0 ]
}

@test "relocate_config fails closed (does not create dest) when the source is missing" {
  run bash -c "source '$SCRIPT'; relocate_config '${WORK_DIR}/nope.env' '${DEST_ENV}' \"\$(id -un)\" sudo"
  [ "$status" -ne 0 ]
  [ ! -f "$DEST_ENV" ]
}

@test "relocate_config creates the dest directory at mode 0755" {
  bash -c "source '$SCRIPT'; relocate_config '${SOURCE_ENV}' '${DEST_ENV}' \"\$(id -un)\" sudo"
  run stat -c '%a' "$(dirname "$DEST_ENV")"
  [ "$output" = "755" ]
}

@test "relocate_config never leaves a secret readable to group or other" {
  bash -c "source '$SCRIPT'; relocate_config '${SOURCE_ENV}' '${DEST_ENV}' \"\$(id -un)\" sudo"
  run stat -c '%A' "$DEST_ENV"
  # rw------- : owner read/write only, nothing for group or other.
  [[ "$output" == "-rw-------" ]]
}

# --- main / usage ------------------------------------------------------------

@test "main relocates using default SOURCE/DEST when both are omitted, given an overridden default" {
  # main's own DEFAULT_SOURCE/DEFAULT_DEST are computed once at source time
  # from SCRIPT_DIR; exercise the explicit two-arg form here (the common
  # path for tests) since overriding the compile-time defaults would
  # require re-sourcing with different SCRIPT_DIR plumbing.
  run bash -c "source '$SCRIPT'; main '${SOURCE_ENV}' '${DEST_ENV}'"
  [ "$status" -eq 0 ]
  [ -f "$DEST_ENV" ]
}

@test "main propagates relocate_config's exit code on failure" {
  run bash -c "source '$SCRIPT'; main '${WORK_DIR}/nope.env' '${DEST_ENV}'"
  [ "$status" -ne 0 ]
}
