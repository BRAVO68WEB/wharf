package storage

import (
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"hash"
	"strings"
)

// DigestHasher verifies streamed content against an expected OCI digest. The
// digest string itself selects the hash algorithm ("sha256:..." or
// "sha512:..."), so one streaming pass both writes the blob and proves its
// content address. Drivers tee every byte through it while storing and call
// Got to compare.
type DigestHasher struct {
	algo string
	h    hash.Hash
}

// NewDigestHasher returns a hasher for the algorithm named in expected.
// expected must be a well-formed "algo:hex" digest; the API layer validates
// syntax, this only refuses algorithms it cannot hash with.
func NewDigestHasher(expected string) (*DigestHasher, error) {
	algo, _, ok := strings.Cut(expected, ":")
	if !ok {
		return nil, fmt.Errorf("storage: malformed digest %q", expected)
	}
	var h hash.Hash
	switch algo {
	case "sha256":
		h = sha256.New()
	case "sha512":
		h = sha512.New()
	default:
		return nil, fmt.Errorf("storage: unsupported digest algorithm %q", algo)
	}
	return &DigestHasher{algo: algo, h: h}, nil
}

// Write implements io.Writer so the hasher can sit in an io.MultiWriter or
// behind an io.TeeReader on the blob's path to storage.
func (d *DigestHasher) Write(p []byte) (int, error) {
	return d.h.Write(p)
}

// Got is the digest the streamed content hashes to, in "algo:hex" form.
func (d *DigestHasher) Got() string {
	return d.algo + ":" + fmt.Sprintf("%x", d.h.Sum(nil))
}
