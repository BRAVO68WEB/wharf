package dashboard

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"modernc.org/sqlite"

	"github.com/go-chi/chi/v5"

	"github.com/BRAVO68WEB/wharf/internal/auth"
	"github.com/BRAVO68WEB/wharf/internal/db"
)

var accountNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)

func (h *HTTPAPI) listAccounts(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(w, r); !ok {
		return
	}
	rows, err := h.q.ListUsers(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	accounts := make([]accountJSON, 0, len(rows))
	for _, row := range rows {
		accounts = append(accounts, accountJSON{ID: row.ID, Username: row.Username, Admin: row.IsAdmin, Service: row.IsService})
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": accounts})
}

func (h *HTTPAPI) createAccount(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(w, r); !ok {
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Service  bool   `json:"service"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid account")
		return
	}
	username := strings.TrimSpace(body.Username)
	if !accountNamePattern.MatchString(username) {
		writeAPIError(w, http.StatusBadRequest, "username must match [A-Za-z0-9][A-Za-z0-9_.-]{0,62}")
		return
	}
	hash, err := accountPasswordHash(body.Password, body.Service)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	user, err := h.q.CreateUser(r.Context(), db.CreateUserParams{
		Username:     username,
		PasswordHash: hash,
		IsAdmin:      false,
		IsService:    body.Service,
	})
	if isUniqueViolation(err) {
		writeAPIError(w, http.StatusConflict, "that username is taken")
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	resp := accountJSON{ID: user.ID, Username: user.Username, Service: user.IsService}
	if body.Service {
		plain, err := issueToken(r.Context(), h.q, user.ID, "default")
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		resp.Token = plain
	}
	audit(h.q, r, "create_account", username)
	writeJSON(w, http.StatusCreated, resp)
}

func (h *HTTPAPI) deleteAccount(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.admin(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "not found")
		return
	}
	if id == actor.ID {
		writeAPIError(w, http.StatusBadRequest, "you cannot delete your own account")
		return
	}
	user, err := h.q.GetUserByID(r.Context(), id)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "not found")
		return
	}
	if user.IsAdmin {
		n, err := h.q.CountAdmins(r.Context())
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if n <= 1 {
			writeAPIError(w, http.StatusBadRequest, "the last admin cannot be deleted")
			return
		}
	}
	if err := h.q.DeleteUser(r.Context(), id); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	audit(h.q, r, "delete_account", user.Username)
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTPAPI) createAccountToken(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(w, r); !ok {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "not found")
		return
	}
	user, err := h.q.GetUserByID(r.Context(), id)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "not found")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid token")
		return
	}
	name := strings.TrimSpace(body.Name)
	var plain string
	if user.IsService {
		plain, err = h.rotateServiceToken(r, user.ID)
	} else if name == "" {
		writeAPIError(w, http.StatusBadRequest, "token name is required")
		return
	} else {
		plain, err = issueToken(r.Context(), h.q, user.ID, name)
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	audit(h.q, r, "create_token", user.Username)
	writeJSON(w, http.StatusCreated, map[string]string{"token": plain, "username": user.Username})
}

func (h *HTTPAPI) rotateServiceToken(r *http.Request, userID int64) (string, error) {
	tx, err := h.sqlDB.BeginTx(r.Context(), nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	q := db.New(tx)
	if err := q.RevokeAccessTokensByUser(r.Context(), userID); err != nil {
		return "", err
	}
	plain, err := issueToken(r.Context(), q, userID, "default")
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return plain, nil
}

func issueToken(ctx context.Context, q db.Querier, userID int64, name string) (string, error) {
	plain, hash, err := auth.NewAccessToken()
	if err != nil {
		return "", err
	}
	_, err = q.CreateAccessToken(ctx, db.CreateAccessTokenParams{
		UserID:    userID,
		Name:      name,
		TokenHash: hash,
	})
	if err != nil {
		return "", err
	}
	return plain, nil
}

func isUniqueViolation(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	// 2067 is SQLITE_CONSTRAINT_UNIQUE. 19 is the primary constraint code
	// some drivers return; only a UNIQUE message counts in that case.
	if sqliteErr.Code() == 2067 {
		return true
	}
	return sqliteErr.Code() == 19 && strings.Contains(sqliteErr.Error(), "UNIQUE")
}

func (h *HTTPAPI) admin(w http.ResponseWriter, r *http.Request) (auth.SessionUser, bool) {
	user, ok := auth.SessionUserFromContext(r.Context())
	if !ok || !user.IsAdmin {
		writeAPIError(w, http.StatusForbidden, "admin required")
		return auth.SessionUser{}, false
	}
	return user, true
}

func accountPasswordHash(password string, service bool) (string, error) {
	if service {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return "", errors.New("internal server error")
		}
		password = string(buf)
	} else if len(password) < 8 {
		return "", errors.New("password must be at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", errors.New("internal server error")
	}
	return string(hash), nil
}

type accountJSON struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Admin    bool   `json:"admin"`
	Service  bool   `json:"service"`
	Token    string `json:"token,omitempty"`
}
