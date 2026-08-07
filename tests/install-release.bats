#!/usr/bin/env bats
# Unit tests for scripts/install-release.sh (OB-062).
#
# These tests exercise the checksum-verify and version-verify fail-closed
# paths, plus the atomic-install happy path and its partial-failure
# behavior, entirely against LOCAL fixtures (a fake binary, a hand-built
# SHA256SUMS, and a fake --version stub). No network call and no real
# GitHub release are involved.
#
# Every function call goes through `run bash -c "source '$SCRIPT'; ..."` so
# each test gets a fresh subshell: the script's own `set -euo pipefail` never
# bleeds into bats' own control flow, and a function that calls `exit`
# (there are none at this layer; only main() exits) would only end that
# subshell, not the test runner.

setup() {
  SCRIPT="${BATS_TEST_DIRNAME}/../scripts/install-release.sh"
  WORK_DIR="$(mktemp -d)"
}

teardown() {
  rm -rf "$WORK_DIR"
}

# write_fake_binary creates a non-executable script at path that prints
# version when invoked as `path --version`, and fails any other invocation.
# Non-executable on purpose: verify_versions is responsible for chmod +x
# before running it, and this proves that responsibility is exercised.
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
}

# write_sums (re)computes real SHA256 hashes for the given filenames inside
# dir and writes a SHA256SUMS file there, matching the Makefile dist target's
# own "cd dist && sha256sum ... > SHA256SUMS" convention.
write_sums() {
  local dir="$1"
  shift
  ( cd "$dir" && sha256sum "$@" > SHA256SUMS )
}

# make_fixture_set builds a valid four-binary + SHA256SUMS scratch directory
# for the given version, all reporting that version via --version.
make_fixture_set() {
  local dir="$1" version="$2"
  local -a names=(openbrain-web openbrain-telegram openbrain-slack openbrain-watchd)
  local -a assets=()
  local name
  for name in "${names[@]}"; do
    local asset="${name}-${version}-linux-amd64"
    write_fake_binary "${dir}/${asset}" "$version"
    assets+=("$asset")
  done
  write_sums "$dir" "${assets[@]}"
}

# write_fake_gh writes a stub `gh` at dir/gh handling exactly the two call
# shapes resolve_version uses: `gh auth status`, `gh release view <tag>
# --repo <repo>` (existence check), and `gh release view --repo <repo>
# --json tagName -q .tagName` (latest resolution). Behavior is controlled by
# env vars so one stub covers every resolve_version scenario:
#   FAKE_GH_AUTH_OK     1 (default) = authenticated, 0 = not authenticated
#   FAKE_GH_TAG_EXISTS  1 (default) = the requested tag exists, 0 = it does not
#   FAKE_GH_LATEST_TAG  the tag printed for the no-args "latest" resolution
write_fake_gh() {
  local dir="$1"
  cat > "${dir}/gh" <<'EOF'
#!/usr/bin/env bash
if [[ "$1" == "auth" && "$2" == "status" ]]; then
  [[ "${FAKE_GH_AUTH_OK:-1}" == "1" ]] && exit 0
  exit 1
fi
if [[ "$1" == "release" && "$2" == "view" ]]; then
  shift 2
  if [[ "$1" != --* ]]; then
    [[ "${FAKE_GH_TAG_EXISTS:-1}" == "1" ]] && exit 0
    exit 1
  fi
  if [[ -n "${FAKE_GH_LATEST_TAG:-}" ]]; then
    printf '%s\n' "${FAKE_GH_LATEST_TAG}"
    exit 0
  fi
  exit 1
fi
echo "fake gh: unhandled args: $*" >&2
exit 99
EOF
  chmod +x "${dir}/gh"
}

# --- asset_filename -----------------------------------------------------

@test "asset_filename builds the binary-version-platform name" {
  run bash -c "source '$SCRIPT'; asset_filename openbrain-web v0.7.1"
  [ "$status" -eq 0 ]
  [ "$output" = "openbrain-web-v0.7.1-linux-amd64" ]
}

# --- expected_checksum ----------------------------------------------------

@test "expected_checksum returns the hash for a listed filename" {
  echo hello > "${WORK_DIR}/thing"
  write_sums "$WORK_DIR" thing
  local want
  want="$(sha256sum "${WORK_DIR}/thing" | awk '{print $1}')"

  run bash -c "source '$SCRIPT'; expected_checksum '${WORK_DIR}/SHA256SUMS' thing"
  [ "$status" -eq 0 ]
  [ "$output" = "$want" ]
}

@test "expected_checksum fails closed when the filename is not listed" {
  echo hello > "${WORK_DIR}/thing"
  write_sums "$WORK_DIR" thing

  run bash -c "source '$SCRIPT'; expected_checksum '${WORK_DIR}/SHA256SUMS' missing-thing"
  [ "$status" -ne 0 ]
}

# --- verify_checksums -----------------------------------------------------

@test "verify_checksums succeeds against a valid local fixture (happy path)" {
  make_fixture_set "$WORK_DIR" v9.9.9

  run bash -c "source '$SCRIPT'; verify_checksums '${WORK_DIR}' SHA256SUMS \
    openbrain-web-v9.9.9-linux-amd64 openbrain-telegram-v9.9.9-linux-amd64 \
    openbrain-slack-v9.9.9-linux-amd64 openbrain-watchd-v9.9.9-linux-amd64"
  [ "$status" -eq 0 ]
}

@test "verify_checksums fails closed and names the asset on a single corrupted binary" {
  make_fixture_set "$WORK_DIR" v9.9.9
  echo "corrupted bytes" >> "${WORK_DIR}/openbrain-slack-v9.9.9-linux-amd64"

  run bash -c "source '$SCRIPT'; verify_checksums '${WORK_DIR}' SHA256SUMS \
    openbrain-web-v9.9.9-linux-amd64 openbrain-telegram-v9.9.9-linux-amd64 \
    openbrain-slack-v9.9.9-linux-amd64 openbrain-watchd-v9.9.9-linux-amd64"
  [ "$status" -ne 0 ]
  [[ "$output" == *"checksum"* ]]
  [[ "$output" == *"openbrain-slack-v9.9.9-linux-amd64"* ]]
}

@test "verify_checksums fails closed when SHA256SUMS itself is missing" {
  write_fake_binary "${WORK_DIR}/openbrain-web-v9.9.9-linux-amd64" v9.9.9

  run bash -c "source '$SCRIPT'; verify_checksums '${WORK_DIR}' SHA256SUMS openbrain-web-v9.9.9-linux-amd64"
  [ "$status" -ne 0 ]
}

@test "verify_checksums fails closed when a listed asset was never downloaded" {
  make_fixture_set "$WORK_DIR" v9.9.9
  rm -f "${WORK_DIR}/openbrain-watchd-v9.9.9-linux-amd64"

  run bash -c "source '$SCRIPT'; verify_checksums '${WORK_DIR}' SHA256SUMS \
    openbrain-web-v9.9.9-linux-amd64 openbrain-telegram-v9.9.9-linux-amd64 \
    openbrain-slack-v9.9.9-linux-amd64 openbrain-watchd-v9.9.9-linux-amd64"
  [ "$status" -ne 0 ]
  [[ "$output" == *"openbrain-watchd-v9.9.9-linux-amd64"* ]]
}

# --- verify_versions -------------------------------------------------------

@test "verify_versions succeeds when every binary reports the expected version (happy path)" {
  make_fixture_set "$WORK_DIR" v9.9.9

  run bash -c "source '$SCRIPT'; verify_versions '${WORK_DIR}' v9.9.9 \
    openbrain-web openbrain-telegram openbrain-slack openbrain-watchd"
  [ "$status" -eq 0 ]
}

@test "verify_versions fails closed and names the binary on a version mismatch" {
  local dir="$WORK_DIR"
  write_fake_binary "${dir}/openbrain-web-v9.9.9-linux-amd64" v9.9.9
  write_fake_binary "${dir}/openbrain-telegram-v9.9.9-linux-amd64" v0.0.1
  write_fake_binary "${dir}/openbrain-slack-v9.9.9-linux-amd64" v9.9.9
  write_fake_binary "${dir}/openbrain-watchd-v9.9.9-linux-amd64" v9.9.9

  run bash -c "source '$SCRIPT'; verify_versions '${dir}' v9.9.9 \
    openbrain-web openbrain-telegram openbrain-slack openbrain-watchd"
  [ "$status" -ne 0 ]
  [[ "$output" == *"openbrain-telegram"* ]]
  [[ "$output" == *"v0.0.1"* ]]
}

@test "verify_versions fails closed when a binary cannot be executed" {
  write_fake_binary "${WORK_DIR}/openbrain-web-v9.9.9-linux-amd64" v9.9.9
  # Overwrite with a stub that exits non-zero even for --version.
  cat > "${WORK_DIR}/openbrain-web-v9.9.9-linux-amd64" <<'EOF'
#!/usr/bin/env bash
exit 7
EOF

  run bash -c "source '$SCRIPT'; verify_versions '${WORK_DIR}' v9.9.9 openbrain-web"
  [ "$status" -ne 0 ]
}

# --- atomic_install ---------------------------------------------------------

@test "atomic_install places all four binaries atomically with mode 0755 (happy path)" {
  make_fixture_set "$WORK_DIR" v9.9.9
  local install_dir="${WORK_DIR}/install"
  mkdir -p "$install_dir"

  run bash -c "source '$SCRIPT'; atomic_install '${WORK_DIR}' '${install_dir}' v9.9.9 \
    openbrain-web openbrain-telegram openbrain-slack openbrain-watchd"
  [ "$status" -eq 0 ]

  for name in openbrain-web openbrain-telegram openbrain-slack openbrain-watchd; do
    [ -f "${install_dir}/${name}" ]
    perm="$(stat -c '%a' "${install_dir}/${name}")"
    [ "$perm" = "755" ]
    "${install_dir}/${name}" --version | grep -q v9.9.9
  done

  # No leftover temp dotfiles from the write-to-temp-then-rename step.
  run bash -c "ls -A '${install_dir}'"
  [[ "$output" != *".openbrain"* ]]
}

@test "atomic_install is idempotent: re-running the same version succeeds with no error" {
  make_fixture_set "$WORK_DIR" v9.9.9
  local install_dir="${WORK_DIR}/install"
  mkdir -p "$install_dir"

  run bash -c "source '$SCRIPT'; atomic_install '${WORK_DIR}' '${install_dir}' v9.9.9 \
    openbrain-web openbrain-telegram openbrain-slack openbrain-watchd"
  [ "$status" -eq 0 ]

  run bash -c "source '$SCRIPT'; atomic_install '${WORK_DIR}' '${install_dir}' v9.9.9 \
    openbrain-web openbrain-telegram openbrain-slack openbrain-watchd"
  [ "$status" -eq 0 ]
  [ -f "${install_dir}/openbrain-web" ]
}

@test "atomic_install leaves not-yet-processed prior binaries intact when a later source is missing" {
  make_fixture_set "$WORK_DIR" v9.9.9
  local install_dir="${WORK_DIR}/install"
  mkdir -p "$install_dir"

  for name in openbrain-web openbrain-telegram openbrain-slack openbrain-watchd; do
    echo "OLD-${name}" > "${install_dir}/${name}"
    chmod 755 "${install_dir}/${name}"
  done

  # Simulate a mid-run failure: the third binary's verified source vanished.
  rm -f "${WORK_DIR}/openbrain-slack-v9.9.9-linux-amd64"

  run bash -c "source '$SCRIPT'; atomic_install '${WORK_DIR}' '${install_dir}' v9.9.9 \
    openbrain-web openbrain-telegram openbrain-slack openbrain-watchd"
  [ "$status" -ne 0 ]

  # Binaries processed before the failure were updated.
  run cat "${install_dir}/openbrain-web"
  [[ "$output" == v9.9.9* ]] || "${install_dir}/openbrain-web" --version | grep -q v9.9.9

  # The binary that failed to stage, and any not yet reached, keep their
  # prior content untouched: no corrupted/half-written file appears.
  run cat "${install_dir}/openbrain-slack"
  [ "$output" = "OLD-openbrain-slack" ]
  run cat "${install_dir}/openbrain-watchd"
  [ "$output" = "OLD-openbrain-watchd" ]

  # No leftover temp dotfiles.
  run bash -c "ls -A '${install_dir}'"
  [[ "$output" != *".openbrain-slack."* ]]
}

# --- check_prereqs -----------------------------------------------------------

@test "check_prereqs fails when the install directory does not exist" {
  run bash -c "source '$SCRIPT'; check_prereqs '${WORK_DIR}/does-not-exist'"
  [ "$status" -ne 0 ]
}

@test "check_prereqs succeeds when the install directory is writable and a download path exists" {
  local install_dir="${WORK_DIR}/install"
  mkdir -p "$install_dir"
  run bash -c "source '$SCRIPT'; check_prereqs '${install_dir}'"
  [ "$status" -eq 0 ]
}

@test "check_prereqs fails closed when neither gh nor curl is on PATH" {
  local install_dir="${WORK_DIR}/install"
  mkdir -p "$install_dir"

  # A PATH containing only bash itself: `command -v gh` and `command -v curl`
  # both fail to resolve, exercising check_prereqs' own no-download-path
  # branch rather than failing for the unrelated reason of bash being
  # unfindable.
  local minimal_path_dir="${WORK_DIR}/minimal-path"
  mkdir -p "$minimal_path_dir"
  ln -s "$(command -v bash)" "${minimal_path_dir}/bash"

  run env PATH="$minimal_path_dir" bash -c "source '$SCRIPT'; check_prereqs '${install_dir}'"
  [ "$status" -ne 0 ]
  [[ "$output" == *"gh is not installed"* ]]
}

# --- end-to-end local pipeline (no network) ---------------------------------

@test "verify_checksums, verify_versions, and atomic_install compose to a full local install" {
  make_fixture_set "$WORK_DIR" v9.9.9
  local install_dir="${WORK_DIR}/install"
  mkdir -p "$install_dir"

  run bash -c "
    source '$SCRIPT'
    set -e
    verify_checksums '${WORK_DIR}' SHA256SUMS \
      openbrain-web-v9.9.9-linux-amd64 openbrain-telegram-v9.9.9-linux-amd64 \
      openbrain-slack-v9.9.9-linux-amd64 openbrain-watchd-v9.9.9-linux-amd64
    verify_versions '${WORK_DIR}' v9.9.9 \
      openbrain-web openbrain-telegram openbrain-slack openbrain-watchd
    atomic_install '${WORK_DIR}' '${install_dir}' v9.9.9 \
      openbrain-web openbrain-telegram openbrain-slack openbrain-watchd
  "
  [ "$status" -eq 0 ]

  for name in openbrain-web openbrain-telegram openbrain-slack openbrain-watchd; do
    "${install_dir}/${name}" --version | grep -q v9.9.9
  done
}

# --- CRITICAL (Wren): atomic_install must fail closed on a chmod failure ---

@test "atomic_install fails closed when chmod fails, installs nothing, and logs no success" {
  make_fixture_set "$WORK_DIR" v9.9.9
  local install_dir="${WORK_DIR}/install"
  mkdir -p "$install_dir"

  # A fake chmod that always fails, prepended to PATH so it shadows the real
  # one while mktemp/cp/mv/sha256sum still resolve normally.
  local fake_bin="${WORK_DIR}/fakebin-chmod"
  mkdir -p "$fake_bin"
  cat > "${fake_bin}/chmod" <<'EOF'
#!/usr/bin/env bash
echo "chmod: simulated failure" >&2
exit 1
EOF
  chmod +x "${fake_bin}/chmod"

  run env PATH="${fake_bin}:${PATH}" bash -c "source '$SCRIPT'; atomic_install '${WORK_DIR}' '${install_dir}' v9.9.9 openbrain-web"
  [ "$status" -ne 0 ]
  [[ "$output" == *"install"* ]]
  # No success message is ever logged for a run that aborted mid-loop.
  [[ "$output" != *"installed:"* ]]
  # Nothing is visible under the real name: the rename never happened.
  [ ! -f "${install_dir}/openbrain-web" ]
  # No leftover temp dotfile: the failed chmod's temp file was cleaned up.
  run bash -c "ls -A '${install_dir}'"
  [[ "$output" != *".openbrain-web."* ]]
}

# --- HIGH (Tess): resolve_version ------------------------------------------

@test "resolve_version aborts and names the tag when the requested tag does not exist" {
  local fake_bin="${WORK_DIR}/fakebin-gh"
  mkdir -p "$fake_bin"
  write_fake_gh "$fake_bin"

  run env PATH="${fake_bin}:${PATH}" FAKE_GH_AUTH_OK=1 FAKE_GH_TAG_EXISTS=0 \
    bash -c "source '$SCRIPT'; resolve_version windingriverholdings/openbrain v0.0.0-missing"
  [ "$status" -ne 0 ]
  [[ "$output" == *"v0.0.0-missing"* ]]
}

@test "resolve_version accepts a requested tag that exists via gh" {
  local fake_bin="${WORK_DIR}/fakebin-gh"
  mkdir -p "$fake_bin"
  write_fake_gh "$fake_bin"

  run env PATH="${fake_bin}:${PATH}" FAKE_GH_AUTH_OK=1 FAKE_GH_TAG_EXISTS=1 \
    bash -c "source '$SCRIPT'; resolve_version windingriverholdings/openbrain v1.0.0"
  [ "$status" -eq 0 ]
  [ "$output" = "v1.0.0" ]
}

@test "resolve_version resolves the latest release via gh when no tag is requested" {
  local fake_bin="${WORK_DIR}/fakebin-gh"
  mkdir -p "$fake_bin"
  write_fake_gh "$fake_bin"

  run env PATH="${fake_bin}:${PATH}" FAKE_GH_AUTH_OK=1 FAKE_GH_LATEST_TAG=v1.2.3 \
    bash -c "source '$SCRIPT'; resolve_version windingriverholdings/openbrain ''"
  [ "$status" -eq 0 ]
  [ "$output" = "v1.2.3" ]
}

@test "resolve_version falls back to curl/jq for latest release when gh is unavailable" {
  # PATH is replaced entirely (not prepended): this proves the curl/jq
  # fallback works with no gh reachable at all, not merely with gh failing.
  local fake_bin="${WORK_DIR}/fakebin-nogh"
  mkdir -p "$fake_bin"
  ln -s "$(command -v bash)" "${fake_bin}/bash"

  cat > "${fake_bin}/curl" <<'EOF'
#!/usr/bin/env bash
echo '{"tag_name":"v2.0.0"}'
EOF
  chmod +x "${fake_bin}/curl"

  # Minimal stand-in for `jq -r '.tag_name'` against the fixed curl stub
  # output, using only bash builtins so no further PATH entries are needed.
  cat > "${fake_bin}/jq" <<'EOF'
#!/usr/bin/env bash
read -r line
line="${line#*\"tag_name\":\"}"
line="${line%%\"*}"
printf '%s\n' "$line"
EOF
  chmod +x "${fake_bin}/jq"

  run env PATH="$fake_bin" bash -c "source '$SCRIPT'; resolve_version windingriverholdings/openbrain ''"
  [ "$status" -eq 0 ]
  [ "$output" = "v2.0.0" ]
}

# --- MEDIUM (Tess) ----------------------------------------------------------

@test "check_prereqs fails when the install directory is not writable and sudo is unavailable" {
  local install_dir="${WORK_DIR}/install"
  mkdir -p "$install_dir"
  chmod 555 "$install_dir"

  # A PATH with curl present (so the download-path branch passes) but no
  # sudo, isolating this test to the write-permission branch specifically.
  local minimal_path_dir="${WORK_DIR}/minimal-path-nosudo"
  mkdir -p "$minimal_path_dir"
  ln -s "$(command -v bash)" "${minimal_path_dir}/bash"
  ln -s "$(command -v curl)" "${minimal_path_dir}/curl"

  run env PATH="$minimal_path_dir" bash -c "source '$SCRIPT'; check_prereqs '${install_dir}'"
  chmod 755 "$install_dir"
  [ "$status" -ne 0 ]
  [[ "$output" == *"sudo"* ]]
}

@test "atomic_install invokes sudo only when the install directory is not directly writable" {
  make_fixture_set "$WORK_DIR" v9.9.9
  local install_dir="${WORK_DIR}/install"
  mkdir -p "$install_dir"
  chmod 555 "$install_dir"

  # A passthrough fake sudo that records it was called, then execs the real
  # command. It cannot truly elevate (this test has no root), so the
  # underlying operation may still fail on the real 555 permission; the
  # point of this test is only to prove atomic_install's sudo_cmd branch is
  # taken when install_dir is not writable, not to prove real elevation
  # (that needs a live host, out of scope for a unit test).
  local fake_bin="${WORK_DIR}/fakebin-sudo"
  mkdir -p "$fake_bin"
  local marker="${WORK_DIR}/sudo-called"
  cat > "${fake_bin}/sudo" <<EOF
#!/usr/bin/env bash
echo called >> "${marker}"
exec "\$@"
EOF
  chmod +x "${fake_bin}/sudo"

  run env PATH="${fake_bin}:${PATH}" bash -c "source '$SCRIPT'; atomic_install '${WORK_DIR}' '${install_dir}' v9.9.9 openbrain-web"
  chmod 755 "$install_dir"

  [ -f "$marker" ]
}

# --- OB-084: RETURN trap scoping in download_via_curl's curl fallback -----
#
# download_via_curl sets a RETURN trap to clean up a temp Authorization
# header file. A RETURN trap registered inside a function is not scoped to
# that function: once armed, it stays active and fires again on every later
# function return, including the CALLER's. Because the trap body references
# the local variable header_file by name (not a value baked in at
# registration time), firing again after download_via_curl has already
# returned finds header_file out of scope, and set -u aborts the whole
# script with "header_file: unbound variable". This reproduces the failure
# through download_release (the real caller), the code path
# scripts/install-release.sh actually exercises when gh is unavailable or
# unauthenticated, for example under sudo where env_reset drops the gh
# keyring session.

# write_fake_curl_writes_dest writes a stub curl that, for any invocation
# carrying `-o <dest>`, writes fixed bytes to dest and exits 0. Matches the
# `curl -fsSL -o "${scratch_dir}/${asset}" "$url"` call shape download_via_curl
# uses for its primary (unauthenticated) download attempt.
write_fake_curl_writes_dest() {
  local dir="$1"
  cat > "${dir}/curl" <<'EOF'
#!/usr/bin/env bash
out=""
for ((i = 1; i <= $#; i++)); do
  if [[ "${!i}" == "-o" ]]; then
    j=$((i + 1))
    out="${!j}"
  fi
done
if [[ -n "$out" ]]; then
  echo "fake-asset-bytes" > "$out"
fi
exit 0
EOF
  chmod +x "${dir}/curl"
}

@test "download_via_curl's RETURN trap does not fire again when its caller download_release returns" {
  local fake_bin="${WORK_DIR}/fakebin-curl-only"
  mkdir -p "$fake_bin"
  # No gh on PATH: have_gh_auth fails immediately, forcing the same curl
  # fallback download_release takes in production under sudo.
  ln -s "$(command -v bash)" "${fake_bin}/bash"
  write_fake_curl_writes_dest "$fake_bin"

  local scratch_dir="${WORK_DIR}/scratch"
  mkdir -p "$scratch_dir"

  run env PATH="$fake_bin" bash -c "
    source '$SCRIPT'
    download_release 'owner/repo' 'v9.9.9' '${scratch_dir}' 'some-asset'
    echo AFTER_DOWNLOAD_RELEASE_RETURNED
  "

  [ "$status" -eq 0 ]
  [[ "$output" != *"unbound variable"* ]]
  [[ "$output" == *"AFTER_DOWNLOAD_RELEASE_RETURNED"* ]]
  [ -f "${scratch_dir}/some-asset" ]
}

@test "download_via_curl still cleans up its Authorization header temp file after a successful call" {
  local fake_bin="${WORK_DIR}/fakebin-curl-token"
  mkdir -p "$fake_bin"
  ln -s "$(command -v bash)" "${fake_bin}/bash"
  write_fake_curl_writes_dest "$fake_bin"

  local scratch_dir="${WORK_DIR}/scratch"
  mkdir -p "$scratch_dir"
  local tmpdir="${WORK_DIR}/tmpdir"
  mkdir -p "$tmpdir"

  run env PATH="${fake_bin}:${PATH}" TMPDIR="$tmpdir" GH_TOKEN=dummy-token bash -c "
    source '$SCRIPT'
    download_via_curl 'owner/repo' 'v9.9.9' '${scratch_dir}' 'some-asset'
  "
  [ "$status" -eq 0 ]

  run bash -c "ls -A '$tmpdir'"
  [ -z "$output" ]
}

@test "atomic_install fails closed with a distinct message when the final rename fails, and leaves the prior binary in place" {
  make_fixture_set "$WORK_DIR" v9.9.9
  local install_dir="${WORK_DIR}/install"
  mkdir -p "$install_dir"
  echo "OLD-openbrain-web" > "${install_dir}/openbrain-web"
  chmod 755 "${install_dir}/openbrain-web"

  local fake_bin="${WORK_DIR}/fakebin-mv"
  mkdir -p "$fake_bin"
  cat > "${fake_bin}/mv" <<'EOF'
#!/usr/bin/env bash
echo "mv: simulated failure" >&2
exit 1
EOF
  chmod +x "${fake_bin}/mv"

  run env PATH="${fake_bin}:${PATH}" bash -c "source '$SCRIPT'; atomic_install '${WORK_DIR}' '${install_dir}' v9.9.9 openbrain-web"
  [ "$status" -ne 0 ]
  [[ "$output" == *"finalize"* ]]

  # The prior binary is untouched: the rename never happened.
  run cat "${install_dir}/openbrain-web"
  [ "$output" = "OLD-openbrain-web" ]

  # The staged temp copy is intentionally left in place for inspection,
  # distinct from the mktemp/cp/chmod failure paths, which clean it up.
  run bash -c "ls -A '${install_dir}'"
  [[ "$output" == *".openbrain-web."* ]]
}
