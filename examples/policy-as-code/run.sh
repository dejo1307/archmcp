#!/bin/sh
# Two compliance regimes declared as law over one small module, and one change
# that breaches three of their controls.
#
# It runs in a COPY under /tmp, so the fixture in this repository is never
# edited and the demo can be re-run as often as you like.
set -e
cd "$(dirname "$0")"

ENOLA="${ENOLA:-enola}"
if ! command -v "$ENOLA" >/dev/null 2>&1; then
  echo "enola not found on PATH. Install it:" >&2
  echo "    curl -fsSL https://raw.githubusercontent.com/enola-labs/enola/main/install.sh | sh" >&2
  echo "    # or: pip install enola-cli" >&2
  echo "then re-run:  ./run.sh" >&2
  exit 1
fi

DEMO="$(mktemp -d)"
trap 'rm -rf "$DEMO"' EXIT
cp -R . "$DEMO/policy-as-code"
rm -rf "$DEMO/policy-as-code/.enola"
DEMO="$DEMO/policy-as-code"

echo "==> Indexing the module and freezing the architecture as it is now"
"$ENOLA" baseline pin "$DEMO" >/dev/null 2>&1

echo
echo "########## 1. What the declaration actually selects"
"$ENOLA" constraints lint "$DEMO" 2>/dev/null | sed -n '/Component resolution/,$p' || true

echo
echo "########## 2. Why this file is under this policy, and which selector said so"
"$ENOLA" constraints explain cardholder/vault.go "$DEMO" 2>/dev/null | head -18 || true

echo
echo "########## 3. The clean run: every control obeyed, two signed carve-outs"
"$ENOLA" check --fail-on=constraints "$DEMO" 2>/dev/null || true

echo
echo "########## 4. Which law governs the files we are about to touch"
"$ENOLA" plan --paths analytics/report.go,cardholder/rotate.go "$DEMO" 2>/dev/null || true

echo
echo "==> Making the change: three edits, each of them reasonable in review"

# Reporting reconciles a charge. Two hops later it is inside the cardholder
# data environment, which is the thing the boundary exists to prevent.
cat > "$DEMO/analytics/report.go" <<'EOF'
package analytics

import "policyascode/gateway"

// Revenue totals a day's orders. It counts cents, never cards.
func Revenue(amounts []int) int {
	total := 0
	for _, cents := range amounts {
		total += cents
	}
	return total
}

// Reconcile re-runs the charge to check the ledger agrees.
func Reconcile(token string, cents int) bool {
	return gateway.Charge(token, cents)
}
EOF

# A new file inside the audited boundary that no policy page anchors.
cat > "$DEMO/cardholder/rotate.go" <<'EOF'
package cardholder

// Rotate re-tokenizes a stored card.
func Rotate(oldToken, newToken string) Card {
	return Store(newToken, ReadPAN(oldToken))
}
EOF

# One log line, in the package that holds personal data.
cat > "$DEMO/customers/store.go" <<'EOF'
// Package customers holds personal data, which is what makes the laws in
// enola/constraints/gdpr.yaml apply to it.
package customers

import "log"

// ProfileStore holds names and addresses.
type ProfileStore struct {
	rows map[string]string
}

// Erase removes every trace of one subject.
func (s *ProfileStore) Erase(subject string) {
	log.Printf("erasing %s", subject)
	delete(s.rows, subject)
}

// ConsentStore holds what each subject agreed to.
type ConsentStore struct {
	rows map[string]bool
}

// Erase removes every trace of one subject.
func (s *ConsentStore) Erase(subject string) {
	delete(s.rows, subject)
}
EOF

echo
echo "########## 5. The toolchain is happy about all three edits"
(cd "$DEMO" && go build ./... && go vet ./... && echo "compiler: fine") || true

echo
echo "########## 6. The gate is not (exit 1)"
"$ENOLA" check --fail-on=constraints "$DEMO" 2>/dev/null || true

# The ledger reads the snapshot on disk rather than the working tree, so both
# halves of every ratio come from one state of the law. Re-index first.
"$ENOLA" --generate "$DEMO" >/dev/null 2>&1

echo
echo "########## 7. How much of the law is being obeyed, and how much excused"
"$ENOLA" constraints ledger "$DEMO" 2>/dev/null || true

echo
echo "==> Read README.md for what each control is and what it cannot say."
