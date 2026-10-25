package version

import "testing"

func TestLine_when_localBuild_then_dev(t *testing.T) {
	version, commit, date := Version, Commit, Date
	t.Cleanup(func() {
		Version, Commit, Date = version, commit, date
	})
	Version, Commit, Date = "dev", "", ""
	if got := Line(); got != "dev" {
		t.Fatalf("Line() = %q, want dev", got)
	}
}

func TestLine_when_release_then_includesCommitAndDate(t *testing.T) {
	version, commit, date := Version, Commit, Date
	t.Cleanup(func() {
		Version, Commit, Date = version, commit, date
	})
	Version, Commit, Date = "0.1.0", "abc123", "2026-10-25T00:00:00Z"
	if got, want := Line(), "0.1.0 (abc123, 2026-10-25T00:00:00Z)"; got != want {
		t.Fatalf("Line() = %q, want %q", got, want)
	}
}
