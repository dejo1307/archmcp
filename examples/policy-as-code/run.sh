#!/bin/sh
# Demonstrate PCI DSS and GDPR-inspired constraints on a small Go module.
#
# Work in a temporary copy so the example files remain unchanged.
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

echo "==> Indexing the module and saving its initial architecture"
"$ENOLA" baseline pin "$DEMO" >/dev/null 2>&1

echo
echo "########## 1. Show what each selector matches"
"$ENOLA" constraints lint "$DEMO" 2>/dev/null | sed -n '/Component resolution/,$p' || true

echo
echo "########## 2. Explain which policies apply to cardholder/vault.go"
"$ENOLA" constraints explain cardholder/vault.go "$DEMO" 2>/dev/null | head -18 || true

echo
echo "########## 3. Check the initial state"
"$ENOLA" check --fail-on=constraints "$DEMO" 2>/dev/null || true

echo
echo "########## 4. Preview the constraints for the files we will change"
"$ENOLA" plan --paths analytics/report.go,cardholder/rotate.go "$DEMO" 2>/dev/null || true

echo
echo "==> Making three changes that violate the declared constraints"

# Add an indirect path from analytics into the cardholder-data environment.
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

# Add a file to the audited boundary without linking it from a policy page.
cat > "$DEMO/cardholder/rotate.go" <<'EOF'
package cardholder

// Rotate re-tokenizes a stored card.
func Rotate(oldToken, newToken string) Card {
	return Store(newToken, ReadPAN(oldToken))
}
EOF

# Log personal data from the customers package.
cat > "$DEMO/customers/store.go" <<'EOF'
// Package customers holds personal data, so the constraints in
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
echo "########## 5. Check whether the Go toolchain accepts the changes"
if command -v go >/dev/null 2>&1; then
  (cd "$DEMO" && go build ./... && go vet ./... && echo "go build and go vet: passed") || true
else
  echo "Go is not installed; skipping go build and go vet"
fi

echo
echo "########## 6. Run the constraint check again (expected exit: 1)"
"$ENOLA" check --fail-on=constraints "$DEMO" 2>/dev/null || true

# The ledger reads the saved snapshot, so update it before generating the
# summary.
"$ENOLA" --generate "$DEMO" >/dev/null 2>&1

echo
echo "########## 7. Summarize compliance and exemptions"
"$ENOLA" constraints ledger "$DEMO" 2>/dev/null || true

echo
echo "==> See README.md for details and limitations."
