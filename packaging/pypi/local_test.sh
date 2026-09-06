#!/usr/bin/env bash
# local_test.sh - phases 1 and 2 of TEMP_WHEELS_INTRODUCTION.md.
#
# Builds a darwin/arm64 wheel from the current tree, installs it into a throwaway
# venv, and checks that what lands on PATH is the Go binary rather than a Python
# launcher. CI is meant to call this same script later, so that the local and CI
# paths cannot drift apart.
#
# Everything is written under a work directory outside the repository. The one
# thing that could reach outside it is `uv tool install`, which defaults to
# ~/.local/bin and would overwrite an already-installed enola; UV_TOOL_BIN_DIR
# and UV_TOOL_DIR below redirect it into the work directory so a test run cannot
# clobber the real installation.
#
# Usage:  packaging/pypi/local_test.sh
# Env:    ENOLA_WHEEL_WORK   work directory (default: $TMPDIR/enola-wheel-test)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="${ENOLA_WHEEL_WORK:-${TMPDIR:-/tmp}/enola-wheel-test}"

# Deliberately not a real release number. A wheel built by this script must never
# be mistakable for one that could ship.
VERSION="0.0.0"
PLATFORM_TAG="macosx_12_0_arm64"
# Keep in step with PLATFORM_TAG above.
MACOS_MIN="12.0"

VENV="$WORK/venv"
DIST="$WORK/dist"
BIN="$WORK/enola"

export UV_TOOL_BIN_DIR="$WORK/toolbin"
export UV_TOOL_DIR="$WORK/tools"

pass_count=0
fail_count=0
skip_count=0

step()  { printf '\n\033[1m== %s\033[0m\n' "$*"; }
ok()    { printf '   \033[32mPASS\033[0m %s\n' "$*"; pass_count=$((pass_count + 1)); }
bad()   { printf '   \033[31mFAIL\033[0m %s\n' "$*"; fail_count=$((fail_count + 1)); }
skip()  { printf '   \033[33mSKIP\033[0m %s\n' "$*"; skip_count=$((skip_count + 1)); }
note()  { printf '   ---- %s\n' "$*"; }

if [ "$(uname -s)" != "Darwin" ] || [ "$(uname -m)" != "arm64" ]; then
  echo "This script covers the darwin/arm64 leg only; see phase 3 for linux." >&2
  exit 1
fi

rm -rf "$WORK"
mkdir -p "$WORK" "$DIST"

# ---------------------------------------------------------------------------
step "Phase 1.1  Build the binary with release flags"

# The upgrade guard (phase 6) may not exist yet. Detecting it from the source
# rather than assuming decides two things below: whether to stamp InstallMethod,
# and whether it is safe to run `enola upgrade` at all. Without the guard that
# command performs a real network self-update and overwrites the binary under
# test, so an unguarded run is not a test, it is a live upgrade.
LDFLAGS="-s -w -X github.com/enola-labs/enola/internal/version.Version=$VERSION"
GUARD=0
if grep -q 'InstallMethod' "$REPO_ROOT/internal/version/version.go" 2>/dev/null; then
  GUARD=1
  LDFLAGS="$LDFLAGS -X github.com/enola-labs/enola/internal/version.InstallMethod=pip"
fi

# MACOSX_DEPLOYMENT_TARGET is what makes the platform tag honest. With cgo the
# link goes through clang, which stamps LC_BUILD_VERSION minos with the HOST SDK
# version unless this is set, so a binary built on macOS 26 claims to need macOS
# 26. Without this line the wheel tag and the binary disagree, and the binary is
# the one users find out about. 12.0 is Go's own oldest supported macOS release
# (cmd/link/internal/ld/macho.go stamps exactly this in internal link mode), so
# it is the floor we can actually stand behind.
#
# The link emits one "built for newer macOS version" warning per Go object file,
# roughly sixty of them, because the Go runtime objects are compiled against the
# host. They are benign, and are filtered here so that a real error stays visible.
( cd "$REPO_ROOT" && MACOSX_DEPLOYMENT_TARGET="$MACOS_MIN" \
    go build -ldflags "$LDFLAGS" -o "$BIN" ./cmd/enola ) 2>"$WORK/build.log" || {
  cat "$WORK/build.log" >&2; exit 1
}
grep -v 'built for newer\|^# github.com' "$WORK/build.log" | sed 's/^/   /' || true
ok "built $(du -h "$BIN" | cut -f1) binary (deployment target $MACOS_MIN)"

# ---------------------------------------------------------------------------
step "Phase 2  Confirm the macOS tag matches the binary"

MINOS="$(otool -l "$BIN" | awk '/LC_BUILD_VERSION/{f=1} f&&/minos/{print $2; exit}')"
note "LC_BUILD_VERSION minos = ${MINOS:-<none>}"
TAG_MIN="${PLATFORM_TAG#macosx_}"; TAG_MIN="${TAG_MIN%%_arm64}"; TAG_MIN="${TAG_MIN//_/.}"
if [ -z "$MINOS" ]; then
  bad "no LC_BUILD_VERSION in the binary; cannot justify the $PLATFORM_TAG tag"
elif [ "$(printf '%s\n%s\n' "$MINOS" "$TAG_MIN" | sort -V | tail -1)" = "$TAG_MIN" ]; then
  ok "binary needs macOS $MINOS, tag claims $TAG_MIN"
else
  bad "binary needs macOS $MINOS but tag claims $TAG_MIN; raise the tag"
fi

# ---------------------------------------------------------------------------
step "Phase 1.2  Build the wheel"

python3 "$REPO_ROOT/packaging/pypi/build_wheel.py" \
  --binary "$BIN" --version "$VERSION" \
  --platform-tag "$PLATFORM_TAG" --outdir "$DIST"

WHEEL="$(echo "$DIST"/*.whl)"
[ -f "$WHEEL" ] && ok "wheel built: $(basename "$WHEEL")" || bad "no wheel produced"

# Same inputs must give the same bytes; the repo has a determinism job and
# packaging should not be the exception to it.
python3 "$REPO_ROOT/packaging/pypi/build_wheel.py" \
  --binary "$BIN" --version "$VERSION" \
  --platform-tag "$PLATFORM_TAG" --outdir "$WORK/dist2" >/dev/null
if cmp -s "$WHEEL" "$WORK/dist2/$(basename "$WHEEL")"; then
  ok "rebuild is byte-identical"
else
  bad "rebuild differs; the wheel is not deterministic"
fi

# ---------------------------------------------------------------------------
step "Phase 1.3  Validate the artifact"

python3 -m zipfile -l "$WHEEL"

if uvx twine check "$WHEEL"; then
  ok "twine check"
else
  bad "twine check"
fi

# Advisory only. This wheel deliberately contains no importable module, so a
# tool that looks for one has something to complain about by design. Read the
# output, do not gate on it.
note "check-wheel-contents (advisory, a scripts-only wheel has no modules):"
uvx check-wheel-contents "$WHEEL" 2>&1 | sed 's/^/        /' || true

# ---------------------------------------------------------------------------
step "Phase 1.4  Install into a throwaway venv"

uv venv "$VENV" >/dev/null
uv pip install --python "$VENV/bin/python" "$WHEEL" >/dev/null
[ -e "$VENV/bin/enola" ] && ok "enola landed in the venv" || bad "no enola in $VENV/bin"

# ---------------------------------------------------------------------------
step "Phase 1.5  What landed is the binary, not a shim"

[ -x "$VENV/bin/enola" ] && ok "executable bit is set" || bad "not executable"

FILETYPE="$(file -b "$VENV/bin/enola")"
note "file: $FILETYPE"
case "$FILETYPE" in
  *"Mach-O 64-bit executable arm64"*) ok "Mach-O arm64 executable" ;;
  *) bad "expected a Mach-O arm64 executable" ;;
esac

# The failure this guards against is subtle: an entry-point wheel also puts an
# executable named enola on PATH, and it also runs. It is just a text file
# starting with a shebang, with an interpreter start in front of every call.
if [ "$(head -c 2 "$VENV/bin/enola")" = "#!" ]; then
  bad "installed file starts with a shebang, so it is a launcher, not the binary"
else
  ok "no shebang, so no interpreter in front of the command"
fi

if cmp -s "$BIN" "$VENV/bin/enola"; then
  ok "installed file is byte-identical to the built binary"
else
  bad "installed file differs from the binary that went into the wheel"
fi

# ---------------------------------------------------------------------------
step "Phase 1.6  Run it"

if "$VENV/bin/enola" --version; then ok "--version"; else bad "--version"; fi
if "$VENV/bin/enola" --version --json; then ok "--version --json"; else bad "--version --json"; fi

SAMPLE="$REPO_ROOT/internal/engine/testdata/repos/python_sample"
if "$VENV/bin/enola" --explain "$SAMPLE" >"$WORK/explain.txt" 2>&1; then
  ok "--explain over python_sample ($(wc -l <"$WORK/explain.txt" | tr -d ' ') lines)"
else
  bad "--explain failed; see $WORK/explain.txt"
fi

# ---------------------------------------------------------------------------
step "Phase 1.7  enola upgrade refuses to self-update a pip install"

if [ "$GUARD" -eq 0 ]; then
  skip "no InstallMethod in internal/version; phase 6 is not done yet."
  note "Not running 'enola upgrade': without the guard it would download the"
  note "latest release over the network and overwrite the binary under test."
else
  BEFORE="$(shasum -a 256 "$VENV/bin/enola" | cut -d' ' -f1)"
  set +e
  UPGRADE_OUT="$("$VENV/bin/enola" upgrade 2>&1)"; UPGRADE_RC=$?
  set -e
  note "exit $UPGRADE_RC: $UPGRADE_OUT"
  AFTER="$(shasum -a 256 "$VENV/bin/enola" | cut -d' ' -f1)"
  [ "$BEFORE" = "$AFTER" ] && ok "binary untouched" || bad "binary was replaced"
  case "$UPGRADE_OUT" in
    *"pip install -U enola-cli"*) ok "points at the pip upgrade path" ;;
    *) bad "does not tell the user to use pip" ;;
  esac
fi

# ---------------------------------------------------------------------------
step "Phase 1.8  Uninstall leaves nothing behind"

uv pip uninstall --python "$VENV/bin/python" enola-cli >/dev/null
[ -e "$VENV/bin/enola" ] && bad "enola survived uninstall" || ok "enola removed"

# ---------------------------------------------------------------------------
step "Phase 1.9  The pipx-shaped paths"

if uvx --from "$WHEEL" enola --version >/dev/null 2>&1; then
  ok "uvx --from <wheel> enola --version"
else
  bad "uvx --from <wheel> failed"
fi

if uv tool install --force "$WHEEL" >/dev/null 2>&1; then
  if [ -x "$UV_TOOL_BIN_DIR/enola" ] && "$UV_TOOL_BIN_DIR/enola" --version >/dev/null 2>&1; then
    ok "uv tool install put a working enola in the isolated bin dir"
  else
    bad "uv tool install produced no working enola"
  fi
  uv tool uninstall enola-cli >/dev/null 2>&1 || true
else
  bad "uv tool install failed"
fi

# ---------------------------------------------------------------------------
printf '\n\033[1m== Summary\033[0m\n'
printf '   %d passed, %d failed, %d skipped\n' "$pass_count" "$fail_count" "$skip_count"
printf '   work directory: %s\n' "$WORK"
[ "$fail_count" -eq 0 ]
