#!/usr/bin/env bash
# index_test.sh - phase 4 of TEMP_WHEELS_INTRODUCTION.md.
#
# Serves the whole wheel set from a local package index and checks that each
# platform picks its own wheel out of it.
#
# Installing a wheel by file path, which is what phases 1 and 3 do, never
# exercises the part that decides what a real user gets: pip choosing one
# candidate from several. That choice is made from the filename tags alone, so a
# tag that is well formed and wrong is invisible until an index has more than one
# wheel in it. This is the last check before PyPI that can still catch it, and it
# touches nothing outside this machine.
#
# The index is pypiserver with auth disabled, and the wheels get there by twine
# upload rather than by being copied into place, so the publish step is
# rehearsed rather than simulated.
#
# Usage:  packaging/pypi/index_test.sh
# Env:    ENOLA_WHEEL_INDEX_WORK   work directory (must be under $HOME, see below)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# Under $HOME because colima does not mount $TMPDIR into its VM, and a bind
# mount of a path the VM cannot see silently becomes an empty directory.
WORK="${ENOLA_WHEEL_INDEX_WORK:-$HOME/.cache/enola-wheel-index}"
VERSION="0.0.0"

DIST="$WORK/dist"
PORT=8089
NET=enola-index-net
SRV=enola-index-server
# A named volume, not a bind mount. pypiserver runs as UID 9898 and chowns its
# package directory at startup, which fails on a colima sshfs bind mount and
# takes the container down with it. Nothing here needs host access to the served
# copies: twine puts them there and pip reads them back over HTTP.
VOL=enola-index-packages

pass_count=0; fail_count=0
step() { printf '\n\033[1m== %s\033[0m\n' "$*"; }
ok()   { printf '   \033[32mPASS\033[0m %s\n' "$*"; pass_count=$((pass_count + 1)); }
bad()  { printf '   \033[31mFAIL\033[0m %s\n' "$*"; fail_count=$((fail_count + 1)); }
note() { printf '   ---- %s\n' "$*"; }

cleanup() {
  docker rm -f "$SRV" >/dev/null 2>&1 || true
  docker volume rm "$VOL" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
}
trap cleanup EXIT

rm -rf "$WORK"; mkdir -p "$DIST"

# ---------------------------------------------------------------------------
step "Phase 4.1  Assemble the wheel set that would actually be published"

# Only wheels that would really ship. The manylinux_2_34 wheels from phase 3 are
# deliberately left out: publishing both floors would let pip prefer 2.34 on a
# new enough distro, which is not what a release does and would make this test
# measure a situation that never occurs.
collect() {
  local src="$1" label="$2"
  if [ -f "$src" ]; then
    cp "$src" "$DIST/"
    ok "$label: $(basename "$src")"
  else
    bad "$label missing ($src). Run the phase that produces it first."
  fi
}

MAC_WORK="${TMPDIR:-/tmp}/enola-wheel-test/dist"
LNX_ARM="$HOME/.cache/enola-wheel-linux/dist"
LNX_X86="$HOME/.cache/enola-wheel-linux-amd64/dist"

collect "$MAC_WORK/enola_cli-${VERSION}-py3-none-macosx_12_0_arm64.whl"   "darwin/arm64 (phase 1)"
collect "$LNX_ARM/enola_cli-${VERSION}-py3-none-manylinux_2_17_aarch64.whl" "linux/arm64 (phase 3)"
collect "$LNX_X86/enola_cli-${VERSION}-py3-none-manylinux_2_17_x86_64.whl"  "linux/amd64 (phase 3)"

# darwin/amd64 and win_amd64 cannot be built and run on this machine. They are
# included anyway, from a cross-build and a placeholder, because their job in
# this test is to be present as candidates that must NOT be chosen. A five-wheel
# index where two are decoys is the situation a release actually creates.
S_CROSS="$WORK/enola-darwin-amd64"
if [ -f "${ENOLA_DARWIN_AMD64:-/nonexistent}" ]; then
  cp "$ENOLA_DARWIN_AMD64" "$S_CROSS"
  python3 "$REPO_ROOT/packaging/pypi/build_wheel.py" --binary "$S_CROSS" \
    --version "$VERSION" --platform-tag macosx_12_0_x86_64 --outdir "$DIST" >/dev/null
  ok "darwin/amd64 (cross-built decoy)"
else
  note "no darwin/amd64 binary supplied; that decoy is skipped"
fi

printf 'not a real binary, structural placeholder only\n' > "$WORK/enola-win-placeholder"
python3 "$REPO_ROOT/packaging/pypi/build_wheel.py" --binary "$WORK/enola-win-placeholder" \
  --version "$VERSION" --platform-tag win_amd64 --outdir "$DIST" >/dev/null
ok "win_amd64 (placeholder decoy)"

# Phase 5. Windows cannot be built or run here, so the only thing that can be
# checked locally is the shape of the wheel: the entry has to be named enola.exe,
# because pip copies that name verbatim into Scripts\ and it is what the user
# ends up typing. Getting this wrong produces a wheel that installs cleanly and
# leaves no working command behind.
WIN_WHEEL="$DIST/enola_cli-${VERSION}-py3-none-win_amd64.whl"
WIN_ENTRIES="$(python3 -c "import zipfile,sys; print('\n'.join(zipfile.ZipFile(sys.argv[1]).namelist()))" "$WIN_WHEEL")"
if echo "$WIN_ENTRIES" | grep -qx "enola_cli-${VERSION}.data/scripts/enola.exe"; then
  ok "win_amd64 wheel carries .data/scripts/enola.exe"
else
  bad "win_amd64 wheel has no .data/scripts/enola.exe:"
  echo "$WIN_ENTRIES" | sed 's/^/        /'
fi
if echo "$WIN_ENTRIES" | grep -q "dist-info/licenses/LICENSE"; then
  ok "win_amd64 wheel carries the licence files"
else
  bad "win_amd64 wheel is missing the licence files"
fi

note "index will hold: $(find "$DIST" -name '*.whl' | wc -l | tr -d ' ') wheels"

# ---------------------------------------------------------------------------
step "Phase 4.2  Stand up the index and publish to it with twine"

docker network create "$NET" >/dev/null 2>&1 || true
docker rm -f "$SRV" >/dev/null 2>&1 || true

# -P . -a . disables authentication for every action, including upload.
docker volume rm "$VOL" >/dev/null 2>&1 || true
docker volume create "$VOL" >/dev/null
docker run -d --name "$SRV" --network "$NET" -p "$PORT:8080" \
  -v "$VOL:/data/packages" \
  pypiserver/pypiserver:latest run -P . -a . /data/packages >/dev/null

# Wait for it rather than assuming, so a slow start is not read as a bad tag.
for _ in $(seq 1 30); do
  if curl -fsS "http://localhost:$PORT/simple/" >/dev/null 2>&1; then break; fi
  sleep 1
done
if curl -fsS "http://localhost:$PORT/simple/" >/dev/null 2>&1; then
  ok "index reachable on localhost:$PORT"
else
  bad "index never came up"
  docker logs "$SRV" 2>&1 | tail -10 | sed 's/^/        /'
  exit 1
fi

if TWINE_USERNAME=x TWINE_PASSWORD=x uvx twine upload \
    --repository-url "http://localhost:$PORT/" --disable-progress-bar \
    "$DIST"/*.whl >"$WORK/upload.log" 2>&1; then
  ok "twine upload accepted $(find "$DIST" -name '*.whl' | wc -l | tr -d ' ') wheels"
else
  bad "twine upload failed"; sed 's/^/        /' "$WORK/upload.log"; exit 1
fi

SERVED="$(curl -fsS "http://localhost:$PORT/simple/enola-cli/" | grep -o 'enola_cli[^"<]*\.whl' | sort -u)"
note "index lists: $(echo "$SERVED" | tr '\n' ' ')"

# ---------------------------------------------------------------------------
# expect_pick <label> <expected wheel tag> <docker image|host>
expect_pick() {
  local label="$1" want="$2" where="$3"
  local got

  if [ "$where" = host ]; then
    rm -rf "$WORK/dl"; mkdir -p "$WORK/dl"
    # --seed, because a bare `uv venv` has no pip in it, and pip is the thing
    # under test here: the point is what a normal `pip install enola-cli`
    # resolves to, not what uv's own resolver would pick.
    [ -x "$WORK/hostvenv/bin/pip" ] || uv venv --seed "$WORK/hostvenv" >/dev/null 2>&1
    got="$("$WORK/hostvenv/bin/python" -m pip download --no-deps --quiet \
      --index-url "http://localhost:$PORT/simple/" --trusted-host localhost \
      --dest "$WORK/dl" enola-cli 2>&1 >/dev/null; \
      find "$WORK/dl" -name '*.whl' -exec basename {} \; 2>/dev/null | head -1)"
  else
    got="$(docker run --rm --network "$NET" --platform "${4:-linux/arm64}" "$where" sh -c "
      pip download --no-deps --quiet --index-url http://$SRV:8080/simple/ \
        --trusted-host $SRV --dest /dl enola-cli >/dev/null 2>&1
      ls /dl 2>/dev/null | head -1" 2>/dev/null)"
  fi

  if [ -z "$got" ]; then
    bad "$label: pip found no candidate at all"
  elif [ "$got" = "enola_cli-${VERSION}-py3-none-${want}.whl" ]; then
    ok "$label picked $got"
  else
    bad "$label picked $got, expected ...${want}.whl"
  fi
}

step "Phase 4.3  Every platform picks its own wheel"

expect_pick "macOS arm64 (host)"    "macosx_12_0_arm64"      host
expect_pick "linux/arm64 bookworm"  "manylinux_2_17_aarch64" python:3.12-slim-bookworm linux/arm64
expect_pick "linux/arm64 bullseye"  "manylinux_2_17_aarch64" python:3.11-slim-bullseye linux/arm64
expect_pick "linux/amd64 bookworm"  "manylinux_2_17_x86_64"  python:3.12-slim-bookworm linux/amd64

# ---------------------------------------------------------------------------
step "Phase 4.4  Installing from the index produces a working enola"

for spec in "python:3.12-slim-bookworm|linux/arm64" "python:3.12-slim-bookworm|linux/amd64"; do
  IMG="${spec%%|*}"; PLAT="${spec#*|}"
  OUT="$(docker run --rm --network "$NET" --platform "$PLAT" "$IMG" sh -c "
    pip install --quiet --no-cache-dir --index-url http://$SRV:8080/simple/ \
      --trusted-host $SRV enola-cli >/dev/null 2>&1 && enola --version" 2>&1 || true)"
  case "$OUT" in
    *"enola version $VERSION"*) ok "$PLAT installed from the index and runs: $OUT" ;;
    *) bad "$PLAT install from index failed: $OUT" ;;
  esac
done

# The host venv is the one place a real user's `pip install enola-cli` can be
# run end to end here, so it is worth doing rather than inferring from 4.3.
if "$WORK/hostvenv/bin/python" -m pip install --quiet \
    --index-url "http://localhost:$PORT/simple/" --trusted-host localhost enola-cli >/dev/null 2>&1 \
    && "$WORK/hostvenv/bin/enola" --version >/dev/null 2>&1; then
  ok "macOS host installed from the index and runs: $("$WORK/hostvenv/bin/enola" --version 2>&1)"
else
  bad "macOS host install from the index failed"
fi

# ---------------------------------------------------------------------------
printf '\n\033[1m== Summary\033[0m\n'
printf '   %d passed, %d failed\n' "$pass_count" "$fail_count"
printf '   work directory: %s\n' "$WORK"
[ "$fail_count" -eq 0 ]
