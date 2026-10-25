package dashboard

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/user/better-docker-registry/internal/auth"
	"github.com/user/better-docker-registry/internal/db"
)

// Personal access tokens belong to the signed-in human. Revoking one does
// not change that person's password. Service accounts keep the admin token
// route, which replaces the single token instead of accumulating them.
func (h *HTTPAPI) listMyTokens(w http.ResponseWriter, r *http.Request) {
	user, ok := sessionHuman(w, r)
	if !ok {
		return
	}
	rows, err := h.q.ListAccessTokensByUser(r.Context(), user.ID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	tokens := make([]tokenJSON, 0)
	for _, row := range rows {
		if row.RevokedAt.Valid {
			continue
		}
		tokens = append(tokens, tokenJSON{
			ID:        row.ID,
			Name:      row.Name,
			CreatedAt: time.Unix(row.CreatedAt, 0).UTC(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": tokens})
}

func (h *HTTPAPI) createMyToken(w http.ResponseWriter, r *http.Request) {
	user, ok := sessionHuman(w, r)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "token name is required")
		return
	}
	name, err := cleanTokenName(body.Name)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	plain, err := issueToken(r.Context(), h.q, user.ID, name)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	audit(h.q, r, "create_token", user.Username)
	writeJSON(w, http.StatusCreated, map[string]string{"token": plain, "name": name})
}

func (h *HTTPAPI) revokeMyToken(w http.ResponseWriter, r *http.Request) {
	user, ok := sessionHuman(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeAPIError(w, http.StatusNotFound, "not found")
		return
	}
	n, err := h.q.RevokeAccessToken(r.Context(), db.RevokeAccessTokenParams{ID: id, UserID: user.ID})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if n == 0 {
		writeAPIError(w, http.StatusNotFound, "not found")
		return
	}
	audit(h.q, r, "revoke_token", user.Username)
	w.WriteHeader(http.StatusNoContent)
}

func sessionHuman(w http.ResponseWriter, r *http.Request) (auth.SessionUser, bool) {
	user, ok := auth.SessionUserFromContext(r.Context())
	if !ok || user.IsService {
		writeAPIError(w, http.StatusUnauthorized, "unauthenticated")
		return auth.SessionUser{}, false
	}
	return user, true
}

func cleanTokenName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" || utf8.RuneCountInString(name) > 64 {
		return "", errors.New("token name is required")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return "", errors.New("token name is required")
		}
	}
	return name, nil
}

type tokenJSON struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}
