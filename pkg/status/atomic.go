package status

import (
	"os"
	"path/filepath"
)

// writeFileAtomic writes data to a temp file in the destination directory and
// renames it over path, so a concurrent reader never observes a torn or partial
// file. Several enola processes read each other's status files while they are
// being rewritten, which makes the rename the only safe publication step.
//
// It mirrors the engine's writer of the same name (internal/engine/global_receipt.go);
// duplicated rather than exported so pkg/status keeps depending on nothing but
// the standard library.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
