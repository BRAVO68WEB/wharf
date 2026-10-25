//go:build !embed

package site

import (
	"io/fs"
	"testing/fstest"
)

// slimIndex is what `go test` and an untagged `go build` serve at /dashboard.
// The real UI is the Vite build, compiled in with -tags embed.
const slimIndex = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Registry</title></head>
<body><p>Registry dashboard assets are not built.</p></body>
</html>
`

func siteFS() (fs.FS, error) {
	return fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(slimIndex)},
	}, nil
}
