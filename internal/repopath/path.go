// Package repopath finds the repository name inside a /v2 URL.
// The name runs up to the last operation segment, so the name itself
// may contain the words blobs, manifests, tags, or referrers.
package repopath

import "strings"

// segments end a repository name. The cut is the last one in the path.
var segments = []string{"/blobs/", "/manifests/", "/tags/", "/referrers/"}

// Name returns the repository name in path. path may be a full /v2 URL
// or the mount-relative route path. ok is false when path names no
// repository operation, such as the version check or the catalog.
func Name(path string) (string, bool) {
	if rest, ok := strings.CutPrefix(path, "/v2/"); ok {
		path = "/" + rest
	}
	cut := -1
	for _, seg := range segments {
		if i := strings.LastIndex(path, seg); i > cut {
			cut = i
		}
	}
	if cut <= 1 {
		return "", false
	}
	name := path[1:cut]
	if name == "" {
		return "", false
	}
	return name, true
}

// Suffix returns the operation portion of a mount-relative route path,
// including its leading slash ("/blobs/<digest>"). ok is false when
// path has no operation.
func Suffix(path string) (string, bool) {
	if _, ok := Name(path); !ok {
		return "", false
	}
	cut := -1
	for _, seg := range segments {
		if i := strings.LastIndex(path, seg); i > cut {
			cut = i
		}
	}
	return path[cut:], true
}
