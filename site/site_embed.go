//go:build embed

package site

import (
	"embed"
	"io/fs"
)

// embedded is the Vite production build. `make build` writes site/out and
// then compiles with -tags embed. all:out keeps dotfiles the build may emit.
//
//go:embed all:out
var embedded embed.FS

func siteFS() (fs.FS, error) {
	return fs.Sub(embedded, "out")
}
