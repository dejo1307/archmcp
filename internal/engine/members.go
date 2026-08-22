package engine

import (
	"path/filepath"

	"github.com/enola-labs/enola/internal/facts"
)

func withMemberProvenance(union, turn facts.SnapshotMeta) facts.SnapshotMeta {
	m := union
	m.RepoPath = turn.RepoPath
	m.RepoLabel = turn.RepoLabel
	m.Git = turn.Git
	m.GeneratedAt = turn.GeneratedAt
	m.Duration = turn.Duration
	m.Extractors = turn.Extractors
	m.Providers = turn.Providers
	m.FileHashes = turn.FileHashes
	m.FilesSeen = turn.FilesSeen
	m.FilesParsed = turn.FilesParsed
	m.SourceBytes = turn.SourceBytes
	m.FilesSkipped = turn.FilesSkipped
	m.DirsSkipped = turn.DirsSkipped
	m.SkippedSample = turn.SkippedSample
	m.ShadowedExtractors = turn.ShadowedExtractors
	m.ParseErrors = turn.ParseErrors
	m.ParseErrorSample = turn.ParseErrorSample
	m.Census = turn.Census
	return m
}

func memberMetas(prev *snapshotBundle, appendMode bool, absRepo string, turn facts.SnapshotMeta) map[string]facts.SnapshotMeta {
	members := map[string]facts.SnapshotMeta{}
	if appendMode && prev != nil {
		for path, meta := range prev.members {
			members[path] = meta
		}
	}
	members[absRepo] = turn
	return members
}

// MetaFor is the meta the published union writes into repoPath's own dir: the
// union's fields with the member's provenance overlaid, when that member is known
// to the bundle, and the published meta otherwise. A baseline pinned from a member
// dir carries this shape, so anything comparing the current state against it has
// to ask for the same member rather than the union's last turn.
func (e *Engine) MetaFor(repoPath string) facts.SnapshotMeta {
	b := e.current.Load()
	if b.snapshot == nil {
		return facts.SnapshotMeta{}
	}
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		return b.snapshot.Meta
	}
	if turn, ok := b.members[abs]; ok {
		return withMemberProvenance(b.snapshot.Meta, turn)
	}
	return b.snapshot.Meta
}
