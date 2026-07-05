// Package cachecov holds the enforced coverage guard for the extractor
// cacheVersion changelog in internal/engine/cache.go.
//
// It intentionally has no production code and no heavy (CGO/tree-sitter)
// dependencies, so the guard test builds and runs in well under a second — cheap
// enough to run from a git pre-push hook, before CI.
package cachecov
