#!/usr/bin/env bats
# Syntax/correctness check for the four Phase 3.5 (OB-066) system unit
# files under deploy/*.service, using `systemd-analyze verify`.
#
# ABSOLUTE SAFETY: this NEVER runs against the shipped deploy/*.service
# files directly, and NEVER installs, loads, or registers anything with
# the live systemd instance on this host. `systemd-analyze verify` only
# parses and statically checks a unit file; it does not daemon-reload,
# enable, or start anything. Even so, verifying the shipped files as-is
# would fail for a reason that has nothing to do with unit-file
# correctness: ExecStart points at /usr/local/bin/openbrain-<name>, which
# does not exist on a dev/CI host (by design: these are the Phase 2
# installer's target, populated only on a real deploy). To isolate pure
# syntax/schema validity from "is the release binary installed on this
# machine", every test here verifies a TEMP COPY of the unit file with
# ExecStart repointed at /bin/true (a binary that exists on every Linux
# host) and EnvironmentFile made optional (a leading `-`, since
# /etc/openbrain/openbrain.env is not expected to exist on a dev/CI
# host either). The original files under deploy/ are never modified.
#
# Skips entirely (not a failure) when systemd-analyze is not on PATH, so
# this suite does not block a non-systemd CI runner.

setup() {
  DEPLOY_DIR="${BATS_TEST_DIRNAME}/../deploy"
  WORK_DIR="$(mktemp -d)"

  if ! command -v systemd-analyze >/dev/null 2>&1; then
    skip "systemd-analyze not available on this host"
  fi
}

teardown() {
  rm -rf "$WORK_DIR"
}

# verifiable_copy renders a syntax-checkable copy of deploy/<unit>.service
# into WORK_DIR: ExecStart repointed at /bin/true (a real, universally
# present binary, standing in for the not-yet-installed release binary)
# and EnvironmentFile made optional. No other directive is touched, so
# this still catches a malformed [Unit]/[Service]/[Install] section, an
# invalid directive name, an invalid boolean, a bad ReadWritePaths value,
# or a bad StateDirectory value in the ACTUAL shipped content.
verifiable_copy() {
  local unit="$1"
  local src="${DEPLOY_DIR}/${unit}.service"
  local dst="${WORK_DIR}/${unit}.service"

  sed \
    -e "s#ExecStart=/usr/local/bin/${unit}#ExecStart=/bin/true#" \
    -e "s#EnvironmentFile=/etc/openbrain/openbrain.env#EnvironmentFile=-/etc/openbrain/openbrain.env#" \
    "$src" > "$dst"
  printf '%s\n' "$dst"
}

@test "openbrain-web.service passes systemd-analyze verify" {
  local copy
  copy="$(verifiable_copy openbrain-web)"
  run systemd-analyze verify "$copy"
  [ "$status" -eq 0 ]
}

@test "openbrain-telegram.service passes systemd-analyze verify" {
  local copy
  copy="$(verifiable_copy openbrain-telegram)"
  run systemd-analyze verify "$copy"
  [ "$status" -eq 0 ]
}

@test "openbrain-slack.service passes systemd-analyze verify" {
  local copy
  copy="$(verifiable_copy openbrain-slack)"
  run systemd-analyze verify "$copy"
  [ "$status" -eq 0 ]
}

@test "openbrain-watchd.service passes systemd-analyze verify" {
  # watchd's PartOf=openbrain-web.service references a sibling unit, so
  # its verifiable copy needs openbrain-web's own copy present alongside
  # it in the same directory to resolve cleanly.
  verifiable_copy openbrain-web >/dev/null
  local copy
  copy="$(verifiable_copy openbrain-watchd)"
  run systemd-analyze verify "$copy"
  [ "$status" -eq 0 ]
}

@test "all four unit files pass systemd-analyze verify together, as a set" {
  local unit
  for unit in openbrain-web openbrain-telegram openbrain-slack openbrain-watchd; do
    verifiable_copy "$unit" >/dev/null
  done

  run systemd-analyze verify \
    "${WORK_DIR}/openbrain-web.service" \
    "${WORK_DIR}/openbrain-telegram.service" \
    "${WORK_DIR}/openbrain-slack.service" \
    "${WORK_DIR}/openbrain-watchd.service"
  [ "$status" -eq 0 ]
}
