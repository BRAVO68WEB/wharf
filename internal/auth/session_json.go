package auth

import (
	"encoding/json"
	"net/http"
)

// sessionView is what the dashboard SPA stores in memory. csrfToken is the
// raw session token. The session cookie is HttpOnly, so this response is the
// only way the page can learn the value it must send back as X-CSRF-Token.
type sessionView struct {
	Username  string `json:"username"`
	CSRFToken string `json:"csrfToken"`
	Admin     bool   `json:"admin"`
}

type loginBody struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// HandleLoginJSON starts a session from a JSON body and returns the CSRF
// token the SPA must keep. Failures use the same 401 text as the form login
// so the two doors do not tell an attacker which one they hit.
func (m *SessionManager) HandleLoginJSON(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, sessionMaxFormBytes)
	var body loginBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid login")
		return
	}
	opened, status := m.startSession(r, body.Username, body.Password)
	if status == http.StatusInternalServerError {
		writeAuthError(w, status, "internal server error")
		return
	}
	if status != http.StatusOK {
		writeAuthError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	http.SetCookie(w, m.sessionCookie(opened.token, opened.expires))
	writeAuthJSON(w, http.StatusOK, sessionView{Username: opened.username, CSRFToken: opened.token, Admin: opened.admin})
}

// HandleSessionJSON reports the signed-in operator. RequireSession has already
// rejected a missing cookie; this is the read the SPA uses after a reload,
// because the HttpOnly cookie cannot be inspected from script.
func (m *SessionManager) HandleSessionJSON(w http.ResponseWriter, r *http.Request) {
	sess, ok := SessionFromContext(r.Context())
	if !ok {
		writeAuthError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	writeAuthJSON(w, http.StatusOK, sessionView{Username: sess.User.Username, CSRFToken: sess.Token, Admin: sess.User.IsAdmin})
}

// HandleLogoutJSON ends the session without a redirect. The SPA navigates
// itself. CSRF still applies: this handler does not check the header, the
// middleware in front of it does.
func (m *SessionManager) HandleLogoutJSON(w http.ResponseWriter, r *http.Request) {
	m.endSession(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func writeAuthJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeAuthError(w http.ResponseWriter, status int, message string) {
	writeAuthJSON(w, status, map[string]string{"error": message})
}
