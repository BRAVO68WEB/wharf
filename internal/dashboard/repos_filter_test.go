package dashboard_test

import (
	"net/http"
	"strings"
	"testing"
)

// Given: repositories "alpha" and "beta".
// When:  GET /dashboard/repos?search=alpha.
// Then:  only alpha's row is rendered.
func Test_listResults_when_searchMatches_then_filteredRows(t *testing.T) {
	h, q := repoTestRouter(t)
	repoTestSeed(t, q, "alpha", 1, 1)
	repoTestSeed(t, q, "beta", 1, 1)

	rec := repoTestGET(t, h, "/dashboard/repos?search=alpha")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if got := repoTestID(body, "repo-row"); got != 1 {
		t.Fatalf(`data-testid="repo-row" count = %d, want 1`, got)
	}
	if !strings.Contains(body, `data-repo="alpha"`) {
		t.Errorf("body missing alpha row")
	}
	if strings.Contains(body, `data-repo="beta"`) {
		t.Errorf("body contains beta row despite the filter")
	}
}

// Given: repositories "a.b" and "axb".
// When:  GET /dashboard/repos?search=a.b.
// Then:  the dot matches literally, so only "a.b" is returned.
func Test_listResults_when_searchHasDot_then_matchedLiterally(t *testing.T) {
	h, q := repoTestRouter(t)
	repoTestSeed(t, q, "a.b", 1, 1)
	repoTestSeed(t, q, "axb", 1, 1)

	body := repoTestGET(t, h, "/dashboard/repos?search=a.b").Body.String()

	if got := repoTestID(body, "repo-row"); got != 1 {
		t.Fatalf(`data-testid="repo-row" count = %d, want 1`, got)
	}
	if !strings.Contains(body, `data-repo="a.b"`) {
		t.Errorf("body missing the literal match a.b")
	}
	if strings.Contains(body, `data-repo="axb"`) {
		t.Errorf("body contains axb: the dot behaved as a LIKE wildcard")
	}
}

// Given: repositories "a%b" and "axb".
// When:  GET /dashboard/repos?search=a%b (URL-encoded as a%25b).
// Then:  the percent sign matches literally rather than as a wildcard.
func Test_listResults_when_searchHasPercent_then_matchedLiterally(t *testing.T) {
	h, q := repoTestRouter(t)
	repoTestSeed(t, q, "a%b", 1, 1)
	repoTestSeed(t, q, "axb", 1, 1)

	body := repoTestGET(t, h, "/dashboard/repos?search=a%25b").Body.String()

	if got := repoTestID(body, "repo-row"); got != 1 {
		t.Fatalf(`data-testid="repo-row" count = %d, want 1`, got)
	}
	if !strings.Contains(body, `data-repo="a%b"`) {
		t.Errorf("body missing the literal a%%b match")
	}
	if strings.Contains(body, `data-repo="axb"`) {
		t.Errorf("body contains axb: the %% behaved as a LIKE wildcard")
	}
}

// Given: repositories "alpha" and "beta".
// When:  GET /dashboard/repos?search=gamma.
// Then:  the empty state is rendered.
func Test_listResults_when_noMatch_then_emptyState(t *testing.T) {
	h, q := repoTestRouter(t)
	repoTestSeed(t, q, "alpha", 1, 1)
	repoTestSeed(t, q, "beta", 1, 1)

	body := repoTestGET(t, h, "/dashboard/repos?search=gamma").Body.String()

	if got := repoTestID(body, "repo-row"); got != 0 {
		t.Errorf(`data-testid="repo-row" count = %d, want 0`, got)
	}
	if got := repoTestID(body, "repo-empty"); got != 1 {
		t.Errorf(`data-testid="repo-empty" count = %d, want 1`, got)
	}
}

// Given: repositories exist.
// When:  GET /dashboard/repos (the HTMX swap target).
// Then:  the response is the results fragment without the base layout.
func Test_listResults_when_fragmentRequested_then_noLayout(t *testing.T) {
	h, q := repoTestRouter(t)
	repoTestSeed(t, q, "alpha", 1, 1)

	body := repoTestGET(t, h, "/dashboard/repos").Body.String()

	if !strings.Contains(body, `data-testid="repo-row"`) {
		t.Errorf("fragment missing the results table")
	}
	if strings.Contains(body, "<html") {
		t.Errorf("fragment must not contain the base layout's <html> element")
	}
	if got := repoTestID(body, "nav-logout"); got != 0 {
		t.Errorf(`data-testid="nav-logout" count = %d in fragment, want 0`, got)
	}
}
