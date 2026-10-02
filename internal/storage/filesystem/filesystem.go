// Package filesystem stores registry blobs on the local filesystem.
//
// Layout: a blob lives at <root>/<digest[:2]>/<digest[2:]> — the digest split
// after its first two characters. Concatenating the two path elements
// reproduces the digest verbatim, so List yields digests without parsing them
// and the driver never re-validates digest syntax (the API boundary owns
// that; here only path safety is checked).
//
// Writes are atomic: content streams into a temp file beside its destination,
// is fsynced, verified against the digest, then renamed into place. A failed
// Put never leaves a readable blob behind.
package filesystem

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/user/better-docker-registry/internal/storage"
)

// tempPrefix marks in-flight Put files living beside their destination.
// CreateTemp supplies the random suffix; List skips the prefix.
const tempPrefix = ".tmp-"

// Config configures a filesystem blob store.
type Config struct {
	// Root is the blob directory tree. It is created if missing.
	Root string
	// MaxBlobSize bounds the content accepted by Put, in bytes.
	// Zero means unlimited. Wired from config.ServerConfig.MaxUploadSize.
	MaxBlobSize int64
}

// Store is a storage.Storage over a directory tree. Safe for concurrent use:
// the fields are immutable after construction and the filesystem provides
// the synchronisation.
type Store struct {
	root        string
	maxBlobSize int64
}

// Compile-time proof that Store honours the storage contract.
var _ storage.Storage = (*Store)(nil)

// New creates cfg.Root if needed and returns a store over it.
func New(cfg Config) (*Store, error) {
	if cfg.Root == "" {
		return nil, errors.New("filesystem: Config.Root must not be empty")
	}
	if cfg.MaxBlobSize < 0 {
		return nil, errors.New("filesystem: Config.MaxBlobSize must not be negative")
	}
	if err := os.MkdirAll(cfg.Root, 0o755); err != nil {
		return nil, wrapUnavailable("create root", cfg.Root, err)
	}
	return &Store{root: cfg.Root, maxBlobSize: cfg.MaxBlobSize}, nil
}

// Get streams the blob's content. See the storage.Storage contract.
func (s *Store) Get(ctx context.Context, digest string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, ok := s.blobPath(digest)
	if !ok {
		return nil, fmt.Errorf("get %s: %w", digest, storage.ErrBlobNotFound)
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("get %s: %w", digest, storage.ErrBlobNotFound)
		}
		return nil, wrapUnavailable("get", digest, err)
	}
	return f, nil
}

// Put streams r into the store under digest and returns the bytes consumed.
// Content that does not hash to digest fails with ErrBlobInvalid and leaves
// no readable blob behind. See the storage.Storage contract.
func (s *Store) Put(ctx context.Context, digest string, r io.Reader) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	path, ok := s.blobPath(digest)
	if !ok {
		return 0, fmt.Errorf("put %s: %w", digest, storage.ErrBlobInvalid)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, wrapUnavailable("put", digest, err)
	}
	// Temp beside the destination keeps the rename on one filesystem, so it
	// is atomic.
	tmp, err := os.CreateTemp(filepath.Dir(path), tempPrefix+"*")
	if err != nil {
		return 0, wrapUnavailable("put", digest, err)
	}
	tmpName := tmp.Name()
	renamed := false
	defer func() {
		if !renamed {
			// Best-effort cleanup; the primary error matters more.
			_ = os.Remove(tmpName)
		}
	}()

	src := &ctxReader{ctx: ctx, r: r}
	var body io.Reader = src
	if s.maxBlobSize > 0 {
		body = io.LimitReader(src, s.maxBlobSize+1)
	}
	h, err := storage.NewDigestHasher(digest)
	if err != nil {
		return 0, fmt.Errorf("put %s: %w", digest, storage.ErrBlobInvalid)
	}
	n, err := io.Copy(io.MultiWriter(tmp, h), body)
	if err != nil {
		_ = tmp.Close()
		if src.readErr() != nil {
			// The input broke (or was canceled), not the backend.
			return n, src.readErr()
		}
		return n, wrapUnavailable("put", digest, err)
	}
	if s.maxBlobSize > 0 && n > s.maxBlobSize {
		_ = tmp.Close()
		return n, fmt.Errorf("put %s: %w: content of %d bytes exceeds max size %d",
			digest, storage.ErrBlobInvalid, n, s.maxBlobSize)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return n, wrapUnavailable("put", digest, err)
	}
	if err := tmp.Close(); err != nil {
		return n, wrapUnavailable("put", digest, err)
	}
	if got := h.Got(); got != digest {
		return n, fmt.Errorf("put %s: %w (content hashes to %s)", digest, storage.ErrBlobInvalid, got)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return n, wrapUnavailable("put", digest, err)
	}
	renamed = true
	return n, nil
}

// Exists reports whether digest is held. See the storage.Storage contract.
func (s *Store) Exists(ctx context.Context, digest string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	path, ok := s.blobPath(digest)
	if !ok {
		return false, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, wrapUnavailable("stat", digest, err)
	}
	return !info.IsDir(), nil
}

// Delete removes digest and prunes its prefix directory once empty. See the
// storage.Storage contract: a missing digest fails with ErrBlobNotFound.
func (s *Store) Delete(ctx context.Context, digest string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, ok := s.blobPath(digest)
	if !ok {
		return fmt.Errorf("delete %s: %w", digest, storage.ErrBlobNotFound)
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("delete %s: %w", digest, storage.ErrBlobNotFound)
		}
		return wrapUnavailable("delete", digest, err)
	}
	// Prune the prefix directory once this was its last blob. Failure here is
	// hygiene only: the blob is gone either way, and a concurrent Put may
	// have legitimately refilled the directory.
	_ = os.Remove(filepath.Dir(path))
	return nil
}

// Size returns the stored byte length of digest. See the storage.Storage
// contract.
func (s *Store) Size(ctx context.Context, digest string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	path, ok := s.blobPath(digest)
	if !ok {
		return 0, fmt.Errorf("size %s: %w", digest, storage.ErrBlobNotFound)
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, fmt.Errorf("size %s: %w", digest, storage.ErrBlobNotFound)
		}
		return 0, wrapUnavailable("stat", digest, err)
	}
	return info.Size(), nil
}

// List invokes fn once per digest in the tree, scanning only the two-level
// layout and skipping in-flight temp files. See the storage.Storage
// contract: fn errors abort the walk and propagate unwrapped, and no locks
// are held while fn runs (there are none to hold).
func (s *Store) List(ctx context.Context, fn func(digest string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var cbErr error
	err := filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			cbErr = ctxErr
			return ctxErr
		}
		if path == s.root {
			return nil
		}
		rel, relErr := filepath.Rel(s.root, path)
		if relErr != nil {
			return relErr
		}
		depth := strings.Count(rel, string(filepath.Separator))
		if d.IsDir() {
			if depth >= 1 {
				// Below the prefix-directory level: not a blob path.
				return fs.SkipDir
			}
			return nil
		}
		if depth != 1 || !d.Type().IsRegular() || strings.HasPrefix(d.Name(), tempPrefix) {
			return nil
		}
		// dir+name reproduces the digest verbatim by construction.
		if err := fn(filepath.Base(filepath.Dir(path)) + d.Name()); err != nil {
			cbErr = err
			return err
		}
		return nil
	})
	if cbErr != nil {
		return cbErr
	}
	if err != nil {
		return wrapUnavailable("list", s.root, err)
	}
	return nil
}

// blobPath maps digest to its file under the layout. ok is false when the
// digest would not name a plain two-level path inside root (too short, or
// carrying separators): such a digest is never held and never writable. This
// is path safety, not digest validation — well-formed digests pass through
// untouched.
func (s *Store) blobPath(digest string) (string, bool) {
	if len(digest) < 3 {
		return "", false
	}
	dir, base := digest[:2], digest[2:]
	if !safeElem(dir) || !safeElem(base) {
		return "", false
	}
	return filepath.Join(s.root, dir, base), true
}

// safeElem reports whether name is a single path element that cannot escape
// its parent directory.
func safeElem(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`)
}

// wrapUnavailable reports a filesystem failure as ErrStorageUnavailable while
// keeping the underlying cause matchable via errors.Is/errors.As.
func wrapUnavailable(op, detail string, err error) error {
	return fmt.Errorf("%s %s: %w (%w)", op, detail, storage.ErrStorageUnavailable, err)
}

// ctxReader aborts a streaming Put when its context is canceled and records
// the first read-side failure, so Put can tell "input broke" from "disk
// broke".
type ctxReader struct {
	ctx context.Context
	r   io.Reader
	err error
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	if err := c.ctx.Err(); err != nil {
		c.err = err
		return 0, err
	}
	n, err := c.r.Read(p)
	if err != nil && err != io.EOF {
		c.err = err
	}
	return n, err
}

// readErr reports the first read-side failure, if any.
func (c *ctxReader) readErr() error { return c.err }
