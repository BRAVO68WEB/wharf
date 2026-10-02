package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"golang.org/x/crypto/bcrypt"

	"github.com/user/better-docker-registry/internal/db"
)

const (
	adminUserEnv = "REGISTRY_ADMIN_USER"
	adminPassEnv = "REGISTRY_ADMIN_PASSWORD"
)

// seedAdminUser provisions the bootstrap administrator from the environment:
// when both REGISTRY_ADMIN_USER and REGISTRY_ADMIN_PASSWORD are set and no
// such user exists, one is created as admin. The pair must be set together or
// not at all — a half-configured pair fails startup instead of creating an
// account with an empty password. An existing user is never rewritten: a
// rotated password set through the dashboard survives a restart.
func seedAdminUser(ctx context.Context, q db.Querier) error {
	username, password := os.Getenv(adminUserEnv), os.Getenv(adminPassEnv)
	switch {
	case username == "" && password == "":
		return nil
	case username == "" || password == "":
		return fmt.Errorf("admin bootstrap: %s and %s must be set together or not at all",
			adminUserEnv, adminPassEnv)
	}
	if _, err := q.GetUserByUsername(ctx, username); err == nil {
		slog.Info("admin bootstrap: user already exists, not touching it", "user", username)
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("admin bootstrap: lookup %s: %w", username, err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("admin bootstrap: hash password: %w", err)
	}
	if _, err := q.CreateUser(ctx, db.CreateUserParams{
		Username:     username,
		PasswordHash: string(hash),
		IsAdmin:      true,
	}); err != nil {
		return fmt.Errorf("admin bootstrap: create %s: %w", username, err)
	}
	slog.Info("admin bootstrap: created admin user", "user", username)
	return nil
}
