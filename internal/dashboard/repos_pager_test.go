package dashboard_test

import (
	"fmt"
	"strings"
	"testing"
)

// Given: 25 repositories and a page size of 20.
// When:  GET /dashboard/repos?page=2.
// Then:  the second page renders its 5 rows and the pager shows page 2 of 2.
func Test_listResults_when_pageRequested_then_pageSliceRendered(t *testing.T) {
	h, q := repoTestRouter(t)
	for i := range 25 {
		repoTestSeed(t, q, fmt.Sprintf("repo-%02d", i), 0, 0)
	}

	body := repoTestGET(t, h, "/dashboard/repos?page=2").Body.String()

	if got := repoTestID(body, "repo-row"); got != 5 {
		t.Fatalf(`data-testid="repo-row" count = %d, want 5`, got)
	}
	if !strings.Contains(body, "Page 2 of 2") {
		t.Errorf("body missing pager status %q", "Page 2 of 2")
	}
	if got := repoTestID(body, "repo-page-next"); got != 0 {
		t.Errorf(`data-testid="repo-page-next" count = %d, want 0 on the last page`, got)
	}
	if got := repoTestID(body, "repo-page-prev"); got != 1 {
		t.Errorf(`data-testid="repo-page-prev" count = %d, want 1 on page 2`, got)
	}
}

// Given: 25 repositories matching the filter "repo".
// When:  GET /dashboard/repos?search=repo.
// Then:  the pager offers Next and its link carries the active filter.
func Test_listResults_when_firstPage_then_pagerCarriesSearch(t *testing.T) {
	h, q := repoTestRouter(t)
	for i := range 25 {
		repoTestSeed(t, q, fmt.Sprintf("repo-%02d", i), 0, 0)
	}

	body := repoTestGET(t, h, "/dashboard/repos?search=repo").Body.String()

	if got := repoTestID(body, "repo-page-next"); got != 1 {
		t.Fatalf(`data-testid="repo-page-next" count = %d, want 1`, got)
	}
	if !strings.Contains(body, "search=repo") {
		t.Errorf("pager links dropped the active filter")
	}
	if !strings.Contains(body, "Page 1 of 2") {
		t.Errorf("body missing pager status %q", "Page 1 of 2")
	}
}

// Given: a page number beyond the last page.
// When:  GET /dashboard/repos?page=99.
// Then:  the response is the empty state with a working Previous link rather
// than an error — pagination is a bounding hint, not a validated input.
func Test_listResults_when_pagePastEnd_then_emptyStateWithPrev(t *testing.T) {
	h, q := repoTestRouter(t)
	repoTestSeed(t, q, "alpha", 1, 1)

	body := repoTestGET(t, h, "/dashboard/repos?page=99").Body.String()

	if got := repoTestID(body, "repo-row"); got != 0 {
		t.Errorf(`data-testid="repo-row" count = %d, want 0`, got)
	}
	if got := repoTestID(body, "repo-page-prev"); got != 1 {
		t.Errorf(`data-testid="repo-page-prev" count = %d, want 1`, got)
	}
}
