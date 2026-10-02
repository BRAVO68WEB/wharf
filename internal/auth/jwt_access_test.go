package auth_test

import (
	"testing"

	"github.com/user/better-docker-registry/internal/auth"
)

// Test_HasAccess_when_GrantedAction_then_True covers the check the Bearer
// middleware (task 13) will run per route.
func Test_HasAccess_when_GrantedAction_then_True(t *testing.T) {
	// Given: claims with pull,push on one repository
	claims := auth.Claims{Access: []auth.AccessEntry{{
		Type: "repository", Name: "library/nginx", Actions: []string{"pull", "push"},
	}}}

	// When/Then: both granted actions are allowed on that name
	if !claims.HasAccess("library/nginx", "pull") {
		t.Error("pull must be granted")
	}
	if !claims.HasAccess("library/nginx", "push") {
		t.Error("push must be granted")
	}
}

// Test_HasAccess_when_AccessEntryMissing_then_False is the table of denial
// cases: wrong name, wrong action, empty and foreign entries.
func Test_HasAccess_when_AccessEntryMissing_then_False(t *testing.T) {
	claims := auth.Claims{Access: []auth.AccessEntry{
		{Type: "repository", Name: "library/nginx", Actions: []string{"pull"}},
		{Type: "registry", Name: "library/nginx", Actions: []string{"push"}},
	}}

	for _, tc := range []struct {
		name, action string
	}{
		{"library/nginx", "push"},   // action not granted
		{"library/redis", "pull"},   // name not granted
		{"", "pull"},                // no name
		{"library/nginx", ""},       // no action
	} {
		if claims.HasAccess(tc.name, tc.action) {
			t.Errorf("HasAccess(%q, %q) = true, want false", tc.name, tc.action)
		}
	}

	// And: a "registry"-type entry never grants repository access
	foreign := auth.Claims{Access: []auth.AccessEntry{{
		Type: "registry", Name: "library/nginx", Actions: []string{"push"},
	}}}
	if foreign.HasAccess("library/nginx", "push") {
		t.Error("non-repository entries must not grant repository access")
	}
}

// Test_HasAccess_when_WildcardAction_then_GrantsEverything pins the "*"
// action of the token spec.
func Test_HasAccess_when_WildcardAction_then_GrantsEverything(t *testing.T) {
	// Given: an access entry with the wildcard action
	claims := auth.Claims{Access: []auth.AccessEntry{{
		Type: "repository", Name: "acme/api", Actions: []string{"*"},
	}}}

	// Then: any action on that name is granted
	for _, action := range []string{"pull", "push", "delete"} {
		if !claims.HasAccess("acme/api", action) {
			t.Errorf("HasAccess(acme/api, %q) = false, want true", action)
		}
	}
}

// Test_HasAccess_when_MultipleEntries_then_MatchesAny pins that entries are
// OR-ed: one matching entry is enough.
func Test_HasAccess_when_MultipleEntries_then_MatchesAny(t *testing.T) {
	// Given: two repository entries
	claims := auth.Claims{Access: []auth.AccessEntry{
		{Type: "repository", Name: "acme/api", Actions: []string{"pull"}},
		{Type: "repository", Name: "acme/web", Actions: []string{"push"}},
	}}

	// Then: each name gets its own actions
	if !claims.HasAccess("acme/web", "push") {
		t.Error("second entry must grant push on acme/web")
	}
	if claims.HasAccess("acme/web", "pull") {
		t.Error("first entry must not leak pull onto acme/web")
	}
}
