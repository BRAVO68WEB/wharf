// Package version is the build identity stamped into the wharf binary.
// Release builds set Version, Commit, and Date with -ldflags -X.
// A local build leaves Version as dev.
package version

import "strings"

// Version, Commit, and Date are set with -ldflags -X.
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// Line is the text printed by --version after the program name.
func Line() string {
	version := strings.TrimSpace(Version)
	if version == "" {
		version = "dev"
	}
	commit := strings.TrimSpace(Commit)
	date := strings.TrimSpace(Date)
	switch {
	case commit != "" && date != "":
		return version + " (" + commit + ", " + date + ")"
	case commit != "":
		return version + " (" + commit + ")"
	case date != "":
		return version + " (" + date + ")"
	default:
		return version
	}
}
