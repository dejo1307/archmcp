#!/bin/sh
# The gate, end to end, on a module small enough to read: pin a baseline, make one
# change that crosses a declared layer, and grade it three ways.
#
# It runs in a COPY under /tmp, so the fixture in this repository is never edited and
# the demo can be re-run as often as you like.
set -e
cd "$(dirname "$0")"

ENOLA="${ENOLA:-enola}"
if ! command -v "$ENOLA" >/dev/null 2>&1; then
  echo "enola not found on PATH. Build it first:" >&2
  echo "    go build -o enola ./cmd/enola   # from the repository root" >&2
  echo "then re-run with:  ENOLA=../../enola ./run.sh" >&2
  exit 1
fi

DEMO="$(mktemp -d)"
trap 'rm -rf "$DEMO"' EXIT
cp -R . "$DEMO/layers-gate"
rm -rf "$DEMO/layers-gate/.enola"
DEMO="$DEMO/layers-gate"

echo "==> Freezing the architecture as it is now"
"$ENOLA" baseline pin "$DEMO" >/dev/null 2>&1

echo "==> Making the change: storage emails the buyer a receipt"
cat > "$DEMO/storage/storage.go" <<'EOF'
package storage

import "layersgate/notify"

// ReadPrice is the innermost layer: it depends on nothing above it.
func ReadPrice(item string) int {
	return len(item)
}

// LoadPrice emails the buyer a receipt — from inside the storage layer.
func LoadPrice(item, buyer string) int {
	price := ReadPrice(item)
	notify.SendReceipt(buyer, item)
	return price
}
EOF

echo
echo "########## 1. The default: reports, fails nothing (exit 0)"
"$ENOLA" check "$DEMO" 2>/dev/null || true

echo
echo "########## 2. Enforcing the declared layer order (exit 1)"
"$ENOLA" check --fail-on=layers "$DEMO" 2>/dev/null || true

# The third run needs something the change's own description did not cover, so the
# second edit lands here rather than above: runs 1 and 2 grade the layer crossing
# alone, and only this one has a scope to breach.
echo
echo "==> One more edit, in a package nobody mentioned"
cat >> "$DEMO/telemetry/telemetry.go" <<'EOF'

// RecordSale is the second edit: a package the author's --target never named.
func RecordSale(item string) {
	Record(item)
}
EOF

echo
echo "########## 3. …and holding the change to the scope its author declared (exit 1)"
"$ENOLA" check --fail-on=layers --target=storage --max-spillover=0 "$DEMO" 2>/dev/null || true

echo
echo "==> Read README.md for what each of the three is enforcing."
