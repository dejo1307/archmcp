package engine

import (
	"testing"

	"github.com/enola-labs/enola/internal/config"
	"github.com/enola-labs/enola/internal/linkers/vocab"
)

// TestConfigHash_MovesWithLinkingVocabulary is the correctness requirement behind the
// whole `linking:` feature, and the one thing about it that can be silently wrong.
//
// The vocabulary decides which cross-repo edges are drawn, so it changes emitted facts.
// If it were not folded into the config hash, two snapshots taken under DIFFERENT
// vocabularies would carry the same hash and read as comparable — and diff_snapshot
// would report the resulting edge changes as a delta someone made to the code. The
// receipt would be quietly lying about what the graph was computed under.
func TestConfigHash_MovesWithLinkingVocabulary(t *testing.T) {
	base := computeConfigHash(config.Default())

	withVocab := config.Default()
	withVocab.Linking = &vocab.Overlay{
		FrameworkConventions: vocab.ListOverlay{Add: []string{"BaseViewController"}},
	}
	if got := computeConfigHash(withVocab); got == base {
		t.Error("config hash did not change when the linking vocabulary did — " +
			"two snapshots under different vocabularies would compare as equivalent")
	}

	// And an equivalent overlay must produce the SAME hash, or every snapshot would
	// look different from the last for no reason.
	same := config.Default()
	same.Linking = &vocab.Overlay{
		FrameworkConventions: vocab.ListOverlay{Add: []string{"BaseViewController"}},
	}
	if computeConfigHash(same) != computeConfigHash(withVocab) {
		t.Error("two identical overlays produced different config hashes")
	}
}

// TestConfigHash_UnchangedWithoutOverlay pins that adding the feature did not move the
// hash for everyone who never uses it. A gratuitous change would invalidate every
// existing baseline and report a delta on every repo at once.
func TestConfigHash_UnchangedWithoutOverlay(t *testing.T) {
	explicitDefaults := config.Default()
	explicitDefaults.Linking = &vocab.Overlay{}
	if computeConfigHash(explicitDefaults) != computeConfigHash(config.Default()) {
		t.Error("an empty `linking:` block changed the config hash; it must be equivalent to omitting it")
	}
}

// TestConfigHash_InvalidOverlayIsDistinct: a snapshot taken with a broken overlay ran on
// default vocabulary, and must not be mistaken for one deliberately taken on defaults.
func TestConfigHash_InvalidOverlayIsDistinct(t *testing.T) {
	bad := config.Default()
	zero := 0
	bad.Linking = &vocab.Overlay{Thresholds: vocab.ThresholdOverlay{MinSharedSymbols: &zero}}
	if computeConfigHash(bad) == computeConfigHash(config.Default()) {
		t.Error("an invalid overlay hashed the same as no overlay")
	}
}
