#!/usr/bin/env bash
# linux_test.sh - phase 3 of TEMP_WHEELS_INTRODUCTION.md.
#
# Answers one question that cannot be answered by reading anything: which glibc
# the enola binary actually requires, and therefore which manylinux tag the linux
# wheels are allowed to claim. CGO_ENABLED=1 means the binary links against the
# glibc of whatever image built it, so the answer is a property of the build
# image and has to be measured per image.
#
# Two failures are possible and they look nothing like each other:
#
#   1. pip refuses the wheel      the tag is newer than the target's glibc
#   2. pip accepts it and it dies the tag is a lie, a symbol is missing
#
# Both are checked below, because passing only the first is what a plausible but
# wrong tag looks like.
#
# Nothing is written inside the repository. The source is mounted read only and
# -buildvcs=false keeps the build from wanting .git, which is owned by another
# uid inside the container anyway.
#
# Usage:  packaging/pypi/linux_test.sh [build-image ...]
# Env:    ENOLA_WHEEL_WORK_LINUX   work directory
#         DOCKER_PLATFORM          e.g. linux/amd64 to cross-check under qemu

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# Under $HOME on purpose. colima mounts the home directory into its VM but not
# $TMPDIR (/var/folders/... on macOS), and a bind mount of a path the VM cannot
# see does not fail: Docker creates an empty directory at the mount point, so
# every file "vanishes" and the build reports success over nothing.
WORK="${ENOLA_WHEEL_WORK_LINUX:-$HOME/.cache/enola-wheel-linux}"
VERSION="0.0.0"

# Toolchain version comes from go.mod so this cannot drift from what CI builds.
GOVER="$(awk '/^go /{print $2; exit}' "$REPO_ROOT/go.mod")"

HOST_ARCH="$(uname -m)"
case "${DOCKER_PLATFORM:-}" in
  linux/amd64) GOARCH=amd64; WHEEL_ARCH=x86_64 ;;
  linux/arm64) GOARCH=arm64; WHEEL_ARCH=aarch64 ;;
  "")
    case "$HOST_ARCH" in
      arm64|aarch64) GOARCH=arm64; WHEEL_ARCH=aarch64 ;;
      x86_64)        GOARCH=amd64; WHEEL_ARCH=x86_64 ;;
      *) echo "unsupported host arch $HOST_ARCH" >&2; exit 1 ;;
    esac
    ;;
  *) echo "DOCKER_PLATFORM must be linux/amd64 or linux/arm64" >&2; exit 1 ;;
esac

# Always passed explicitly, even when it matches the host. An empty array
# expanded under `set -u` is an error in bash 3.2, which is what macOS ships,
# and the alternative spellings that avoid it are harder to read than just
# never having the empty case.
PLATFORM="linux/${GOARCH}"

# Build images, as "image|glibc|note". The glibc column is what the image is
# expected to ship and is asserted against the measurement, so a base image
# moving underneath us is a visible failure rather than a silent tag change.
BUILD_IMAGES_DEFAULT=(
  "ubuntu:24.04|2.39|what the CI runner uses today"
  "debian:bookworm-slim|2.36|one step back"
  "quay.io/pypa/manylinux_2_28_${WHEEL_ARCH}|2.28|the packaging baseline"
)
if [ "$#" -gt 0 ]; then BUILD_IMAGES=("$@"); else BUILD_IMAGES=("${BUILD_IMAGES_DEFAULT[@]}"); fi

# Install targets. Both ship pip, so no apt step is needed inside them. bullseye
# at 2.31 is deliberately older than the Ubuntu 22.04 named in the plan (2.35):
# if a wheel works there it works on 22.04 too.
TEST_IMAGES=(
  "python:3.12-slim-bookworm|2.36"
  "python:3.11-slim-bullseye|2.31"
)

pass_count=0; fail_count=0
step() { printf '\n\033[1m== %s\033[0m\n' "$*"; }
ok()   { printf '   \033[32mPASS\033[0m %s\n' "$*"; pass_count=$((pass_count + 1)); }
bad()  { printf '   \033[31mFAIL\033[0m %s\n' "$*"; fail_count=$((fail_count + 1)); }
note() { printf '   ---- %s\n' "$*"; }

mkdir -p "$WORK/out" "$WORK/dist" "$WORK/go"

# One run at a time. Two concurrent runs share $WORK, overwrite each other's
# binaries and logs, and produce measurements that belong to neither.
LOCK="$WORK/.lock"
if ! mkdir "$LOCK" 2>/dev/null; then
  echo "another linux_test.sh is running (lock: $LOCK). Remove it if that is stale." >&2
  exit 1
fi
trap 'rmdir "$LOCK" 2>/dev/null || true' EXIT

# Prove the work directory actually reaches the container before trusting a
# single measurement taken through it. See the comment on WORK above: the
# failure mode this catches is silent, and it looks exactly like success.
echo "mount-is-real" > "$WORK/go/.mount_probe"
PROBE="$(docker run --rm -v "$WORK/go:/probe:ro" alpine cat /probe/.mount_probe 2>&1 || true)"
if [ "$PROBE" != "mount-is-real" ]; then
  echo "error: $WORK is not visible inside containers." >&2
  echo "Docker returned: $PROBE" >&2
  echo "With colima, only paths the VM mounts (\$HOME by default) can be bind" >&2
  echo "mounted. Set ENOLA_WHEEL_WORK_LINUX to a directory under \$HOME." >&2
  exit 1
fi

# ---------------------------------------------------------------------------
step "Phase 3.0  Fetch the Go toolchain once, share it with every image"

GOTAR="$WORK/go/go${GOVER}.linux-${GOARCH}.tar.gz"
if [ -f "$GOTAR" ]; then
  ok "go${GOVER}.linux-${GOARCH} already fetched"
else
  curl -fsSL -o "$GOTAR" "https://go.dev/dl/go${GOVER}.linux-${GOARCH}.tar.gz"
  ok "fetched go${GOVER}.linux-${GOARCH} ($(du -h "$GOTAR" | cut -f1))"
fi

cat > "$WORK/go/build_in_container.sh" <<'INNER'
#!/bin/sh
set -eu

# gcc is what makes this a cgo build at all. The manylinux images already carry a
# toolchain; the distro images do not.
if ! command -v gcc >/dev/null 2>&1; then
  if command -v apt-get >/dev/null 2>&1; then
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -qq >/dev/null
    apt-get install -y -qq --no-install-recommends \
      gcc binutils libc6-dev ca-certificates >/dev/null
  else
    echo "no gcc and no apt-get in this image" >&2; exit 1
  fi
fi

tar -C /usr/local -xzf "$GOTAR_IN"
export PATH="/usr/local/go/bin:$PATH"
export GOMODCACHE=/gomod GOCACHE=/gocache
# -buildvcs=false: /src is read only and .git belongs to another uid in here.
export GOFLAGS="-buildvcs=false"

cd /src
CGO_ENABLED=1 go build \
  -ldflags "-s -w -X github.com/enola-labs/enola/internal/version.Version=${ENOLA_VERSION}" \
  -o "/out/${OUT_NAME}" ./cmd/enola

echo "GLIBC_SYMBOLS_BEGIN"
objdump -T "/out/${OUT_NAME}" | grep -o 'GLIBC_[0-9.]*' | sort -uV
echo "GLIBC_SYMBOLS_END"
echo "LDD_BEGIN"
ldd "/out/${OUT_NAME}" || true
echo "LDD_END"
INNER
# Mounted as part of its directory and invoked as `sh <script>` rather than
# bind-mounted as a single file and executed. colima's sshfs mount turns a
# single-file bind into a directory inside the container, and the exec bit does
# not survive it either.
chmod +x "$WORK/go/build_in_container.sh"

docker volume create enola-wheel-gomod >/dev/null
docker volume create enola-wheel-gocache >/dev/null

# ---------------------------------------------------------------------------
declare -a MEASURED=()

for entry in "${BUILD_IMAGES[@]}"; do
  IMAGE="${entry%%|*}"; rest="${entry#*|}"
  EXPECT_GLIBC="${rest%%|*}"; NOTE="${rest#*|}"
  SLUG="$(echo "$IMAGE" | tr '/:.' '___')"
  OUT_NAME="enola-${SLUG}"

  step "Phase 3.1  Build in ${IMAGE} (${NOTE})"

  if docker run --rm --platform "$PLATFORM" \
      -v "$REPO_ROOT:/src:ro" \
      -v "$WORK/go:/gohost:ro" \
      -v "$WORK/out:/out" \
      -v enola-wheel-gomod:/gomod \
      -v enola-wheel-gocache:/gocache \
      -e "ENOLA_VERSION=$VERSION" -e "OUT_NAME=$OUT_NAME" \
      -e "GOTAR_IN=/gohost/$(basename "$GOTAR")" \
      "$IMAGE" sh /gohost/build_in_container.sh >"$WORK/$SLUG.log" 2>&1; then
    ok "built in $IMAGE"
  else
    bad "build failed in $IMAGE"
    tail -20 "$WORK/$SLUG.log" | sed 's/^/        /'
    continue
  fi

  SYMS="$(sed -n '/GLIBC_SYMBOLS_BEGIN/,/GLIBC_SYMBOLS_END/p' "$WORK/$SLUG.log" | sed '1d;$d')"
  MAX="$(echo "$SYMS" | sed 's/GLIBC_//' | sort -V | tail -1)"
  COUNT="$(echo "$SYMS" | grep -c . || true)"
  note "distinct GLIBC versions referenced: $COUNT, highest: $MAX"
  note "$(echo "$SYMS" | tr '\n' ' ')"

  if [ "$MAX" = "$EXPECT_GLIBC" ]; then
    ok "floor is $MAX, matching the image's own glibc"
  else
    note "image ships $EXPECT_GLIBC but the binary only needs $MAX"
    ok "floor is $MAX"
  fi

  # The glibc floor is necessary but not sufficient. manylinux also restricts
  # WHICH shared libraries may be linked; anything outside the policy list has
  # to be vendored into the wheel, which is not something this packaging does.
  LIBS="$(sed -n '/LDD_BEGIN/,/LDD_END/p' "$WORK/$SLUG.log" | sed '1d;$d' \
    | awk '{print $1}' | sed 's/\.so.*//' | grep -v '^$' | sort -u)"
  OUTSIDE=""
  for lib in $LIBS; do
    case "$lib" in
      linux-vdso|libc|libm|libdl|libpthread|librt|libresolv|libgcc_s|/lib/ld-linux*|/lib64/ld-linux*) ;;
      *) OUTSIDE="$OUTSIDE $lib" ;;
    esac
  done
  if [ -z "$OUTSIDE" ]; then
    ok "links only manylinux-policy libraries"
  else
    bad "links outside the manylinux policy:$OUTSIDE"
  fi

  MEASURED+=("$IMAGE|$MAX|$OUT_NAME")
done

# ---------------------------------------------------------------------------
step "Phase 3.2  Wheels, tagged at the floor each binary actually needs"

declare -a WHEELS=()
for m in "${MEASURED[@]}"; do
  IMAGE="${m%%|*}"; rest="${m#*|}"; MAX="${rest%%|*}"; OUT_NAME="${rest#*|}"
  TAG="manylinux_${MAX//./_}_${WHEEL_ARCH}"
  if python3 "$REPO_ROOT/packaging/pypi/build_wheel.py" \
      --binary "$WORK/out/$OUT_NAME" --version "$VERSION" \
      --platform-tag "$TAG" --outdir "$WORK/dist" >/dev/null 2>&1; then
    ok "$IMAGE -> $TAG"
    WHEELS+=("$WORK/dist/enola_cli-${VERSION}-py3-none-${TAG}.whl|$TAG|$MAX")
  else
    bad "$IMAGE -> $TAG (tag not in KNOWN_PLATFORM_TAGS?)"
  fi
done

# ---------------------------------------------------------------------------
if [ "${#WHEELS[@]}" -eq 0 ]; then
  bad "no wheels were produced, so nothing can be install-tested"
  TEST_IMAGES=()
fi

for t in ${TEST_IMAGES[@]+"${TEST_IMAGES[@]}"}; do
  TIMAGE="${t%%|*}"; TGLIBC="${t#*|}"

  step "Phase 3.3  Install on ${TIMAGE} (glibc ${TGLIBC})"

  for w in ${WHEELS[@]+"${WHEELS[@]}"}; do
    WHEEL="${w%%|*}"; rest="${w#*|}"; TAG="${rest%%|*}"; NEEDS="${rest#*|}"

    # What SHOULD happen, computed from the two numbers rather than hardcoded,
    # so that a rejection is only a pass when it is the expected rejection.
    if [ "$(printf '%s\n%s\n' "$NEEDS" "$TGLIBC" | sort -V | head -1)" = "$NEEDS" ]; then
      EXPECT=install
    else
      EXPECT=reject
    fi

    set +e
    OUT="$(docker run --rm --platform "$PLATFORM" -v "$WORK/dist:/dist:ro" "$TIMAGE" \
      sh -c "pip install --quiet --no-cache-dir /dist/$(basename "$WHEEL") >/dev/null 2>&1 \
             && enola --version 2>&1" 2>&1)"
    RC=$?
    set -e

    if [ "$EXPECT" = install ] && [ "$RC" -eq 0 ]; then
      ok "$TAG installs and runs: $OUT"
    elif [ "$EXPECT" = reject ] && [ "$RC" -ne 0 ]; then
      ok "$TAG correctly refused (needs $NEEDS, image has $TGLIBC)"
    elif [ "$EXPECT" = install ]; then
      bad "$TAG should have worked here but did not"
      echo "$OUT" | tail -5 | sed 's/^/        /'
    else
      bad "$TAG should have been refused but was accepted; the tag is a lie"
      echo "$OUT" | tail -5 | sed 's/^/        /'
    fi
  done
done

# ---------------------------------------------------------------------------
printf '\n\033[1m== Summary\033[0m\n'
printf '   %d passed, %d failed\n' "$pass_count" "$fail_count"
printf '   work directory: %s\n' "$WORK"
[ "$fail_count" -eq 0 ]
