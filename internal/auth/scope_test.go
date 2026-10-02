package auth_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/user/better-docker-registry/internal/auth"
)

// Test_ParseScope_when_RepositoryScope_then_SplitsTypeNameActions is the
// acceptance mapping "repository:name:pull,push" -> action list.
func Test_ParseScope_when_RepositoryScope_then_SplitsTypeNameActions(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		name    string
		actions []string
	}{
		{"repository:name:pull", "name", []string{"pull"}},
		{"repository:name:pull,push", "name", []string{"pull", "push"}},
		{"repository:library/nginx:pull,push", "library/nginx", []string{"pull", "push"}},
		{"repository:acme/team/api:delete", "acme/team/api", []string{"delete"}},
		{"repository:name:*", "name", []string{"*"}},
		{"repository:name:pull,push,pull", "name", []string{"pull", "push", "pull"}},
	} {
		got, err := auth.ParseScope(tc.raw)
		if err != nil {
			t.Errorf("ParseScope(%q): %v", tc.raw, err)
			continue
		}
		if got.Type != "repository" {
			t.Errorf("ParseScope(%q).Type = %q, want repository", tc.raw, got.Type)
		}
		if got.Name != tc.name {
			t.Errorf("ParseScope(%q).Name = %q, want %q", tc.raw, got.Name, tc.name)
		}
		if strings.Join(got.Actions, ",") != strings.Join(tc.actions, ",") {
			t.Errorf("ParseScope(%q).Actions = %v, want %v", tc.raw, got.Actions, tc.actions)
		}
	}
}

// Test_ParseScope_when_MalformedScope_then_Error is the table of refused
// scopes: the boundary rejects them, callers never see a half-parsed entry.
func Test_ParseScope_when_MalformedScope_then_Error(t *testing.T) {
	for _, raw := range []string{
		"",                          // nothing
		"repository",                // no colons
		"repository:name",           // no actions part
		"repository:",               // empty name and actions
		"repository::pull",          // empty name
		"repository:name:",          // empty actions
		"repository:name:pull,,push", // empty action
		"repository:name:pull,",     // trailing empty action
		"repository:name:pull push", // space inside an action
		"repository:name:Pull",      // upper-case action
		"repository:name:pull:push", // colon inside actions
		"repository:my name:pull",   // space inside the name
		"registry:catalog:*",        // unsupported resource type
		":name:pull",                // empty type
		"name:pull",                 // too few parts
		"repository:" + strings.Repeat("n", 256) + ":pull", // name too long
	} {
		got, err := auth.ParseScope(raw)
		if err == nil {
			t.Errorf("ParseScope(%q) = %+v, want an error", raw, got)
			continue
		}
		if !errors.Is(err, auth.ErrInvalidScope) {
			t.Errorf("ParseScope(%q) error = %v, want errors.Is(err, auth.ErrInvalidScope)", raw, err)
		}
		if got.Type != "" || got.Name != "" || got.Actions != nil {
			t.Errorf("ParseScope(%q) entry = %+v, want the zero entry on error", raw, got)
		}
	}
}

// Test_ParseScope_when_NameAtLengthLimit_then_Accepted pins the 255-character
// name boundary of the OCI name grammar.
func Test_ParseScope_when_NameAtLengthLimit_then_Accepted(t *testing.T) {
	// Given: a scope whose name is exactly 255 characters
	name := strings.Repeat("n", 255)

	// When: it is parsed
	got, err := auth.ParseScope("repository:" + name + ":pull")

	// Then: it is accepted
	if err != nil {
		t.Fatalf("ParseScope: %v", err)
	}
	if got.Name != name {
		t.Errorf("name = %d chars, want 255", len(got.Name))
	}
}
