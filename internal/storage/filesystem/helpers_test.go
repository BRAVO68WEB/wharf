package filesystem_test

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BRAVO68WEB/wharf/internal/storage/filesystem"
)

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// makeBlob builds deterministic pseudo-random content of n bytes so tests
// never depend on wall-clock or global randomness.
func makeBlob(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + i/256)
	}
	return b
}

// newStore returns a store rooted in a fresh temp directory alongside that
// root path. maxBlobSize zero means unlimited.
func newStore(t *testing.T, maxBlobSize int64) (*filesystem.Store, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "blobs")
	s, err := filesystem.New(filesystem.Config{Root: root, MaxBlobSize: maxBlobSize})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, root
}

// blobPath returns the documented layout path for a digest:
// <root>/<digest[:2]>/<digest[2:]>.
func blobPath(root, digest string) string {
	return filepath.Join(root, digest[:2], digest[2:])
}

// assertNoTempFiles fails if any in-flight Put litter (.tmp-*) survives under
// root; a failed Put must not leave anything readable behind.
func assertNoTempFiles(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), ".tmp-") {
			t.Errorf("temp litter left behind: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk root: %v", err)
	}
}

// assertNoFiles fails if any regular file exists under root.
func assertNoFiles(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			t.Errorf("unexpected file after failed Put: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk root: %v", err)
	}
}
