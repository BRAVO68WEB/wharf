package main

import (
	"context"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/BRAVO68WEB/wharf/internal/db"
)

func newBootstrapDB(t *testing.T) db.Querier {
	t.Helper()
	sqlDB, err := db.OpenAndMigrate(t.Context(), filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db.New(sqlDB)
}

// Test_seedAdminUser_when_NoEnv_then_Noop pins that an unset bootstrap pair
// is not an error: deployments without a seeded admin stay untouched.
func Test_seedAdminUser_when_NoEnv_then_Noop(t *testing.T) {
	// Given: no REGISTRY_ADMIN_USER / REGISTRY_ADMIN_PASSWORD
	t.Setenv("REGISTRY_ADMIN_USER", "")
	t.Setenv("REGISTRY_ADMIN_PASSWORD", "")
	q := newBootstrapDB(t)

	// When: the bootstrap runs
	err := seedAdminUser(t.Context(), q)

	// Then: it succeeds and no user was created
	if err != nil {
		t.Fatalf("seedAdminUser: %v", err)
	}
	n, err := q.CountUsers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("users = %d, want 0", n)
	}
}

// Test_seedAdminUser_when_BothEnvSet_then_CreatesAdmin pins the fresh-deploy
// path: the env pair becomes an admin user whose password the token endpoint
// accepts.
func Test_seedAdminUser_when_BothEnvSet_then_CreatesAdmin(t *testing.T) {
	// Given: a complete bootstrap pair and an empty user table
	t.Setenv("REGISTRY_ADMIN_USER", "admin")
	t.Setenv("REGISTRY_ADMIN_PASSWORD", "s3cret")
	q := newBootstrapDB(t)

	// When: the bootstrap runs
	err := seedAdminUser(t.Context(), q)

	// Then: the user exists as an admin with a bcrypt password hash
	if err != nil {
		t.Fatalf("seedAdminUser: %v", err)
	}
	user, err := q.GetUserByUsername(t.Context(), "admin")
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	if !user.IsAdmin {
		t.Error("IsAdmin = false, want true")
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte("s3cret")) != nil {
		t.Error("password hash does not verify the configured password")
	}
}

// Test_seedAdminUser_when_UserExists_then_LeavesItUntouched pins idempotent
// re-provisioning: a restart never rewrites an existing account (so an
// operator-changed password is not silently reverted).
func Test_seedAdminUser_when_UserExists_then_LeavesItUntouched(t *testing.T) {
	// Given: an existing user whose password differs from the env pair
	q := newBootstrapDB(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("rotated"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateUser(t.Context(), db.CreateUserParams{
		Username:     "admin",
		PasswordHash: string(hash),
		IsAdmin:      false,
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REGISTRY_ADMIN_USER", "admin")
	t.Setenv("REGISTRY_ADMIN_PASSWORD", "s3cret")

	// When: the bootstrap runs
	err = seedAdminUser(t.Context(), q)

	// Then: the stored credentials are unchanged
	if err != nil {
		t.Fatalf("seedAdminUser: %v", err)
	}
	user, err := q.GetUserByUsername(t.Context(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte("rotated")) != nil {
		t.Error("existing password hash was overwritten")
	}
	if user.IsAdmin {
		t.Error("IsAdmin changed to true, want the stored value kept")
	}
}

// Test_seedAdminUser_when_HalfSetEnv_then_Error pins fail-fast: a
// half-configured bootstrap pair refuses startup instead of creating a user
// with an empty password.
func Test_seedAdminUser_when_HalfSetEnv_then_Error(t *testing.T) {
	for _, tc := range []struct{ user, pass string }{
		{"admin", ""},
		{"", "s3cret"},
	} {
		t.Setenv("REGISTRY_ADMIN_USER", tc.user)
		t.Setenv("REGISTRY_ADMIN_PASSWORD", tc.pass)
		q := newBootstrapDB(t)

		err := seedAdminUser(context.Background(), q)

		if err == nil {
			t.Errorf("seedAdminUser(user=%q): want an error", tc.user)
		}
	}
}
