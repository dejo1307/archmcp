package diff

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/enola-labs/enola/internal/facts"
)

// FindingIdentity is the stable identity of a finding across snapshots, as a
// short hex digest of the same key the diff pairs findings on. It is what a
// writer that leaves the binary (a SARIF fingerprint, an annotation a host
// deduplicates between builds) carries, so the identity CI sees is the one
// the verdict was computed with and never a second derivation.
func FindingIdentity(in facts.Insight) string {
	sum := sha256.Sum256([]byte(findingKey(in)))
	return hex.EncodeToString(sum[:8])
}
