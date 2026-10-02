package auth

import (
	"net/http"
)

// sessionLoginURL builds the absolute login-page URL for the request's own
// host:port. Redirect targets must name the server the client is actually
// talking to — a hard-coded host or port sends clients to the wrong place
// whenever the listen address differs from that constant.
func sessionLoginURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host + SessionLoginPath
}

// HandleLoginPage serves the sign-in form at GET /dashboard/login. The form
// posts back to SessionLoginPath; all failure handling stays in HandleLogin.
func (m *SessionManager) HandleLoginPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(sessionLoginPageHTML))
}

// sessionLoginPageHTML is the sign-in form: a bare document, no session, no
// CSRF token yet (the token only exists once a session does). Field names
// match what HandleLogin reads off the form.
const sessionLoginPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sign in · better-docker-registry</title>
<style>
:root { color-scheme: light dark; }
* { box-sizing: border-box; }
body {
	margin: 0;
	font-family: ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif;
	line-height: 1.5;
	display: grid;
	place-items: center;
	min-height: 100vh;
}
main { width: min(22rem, 100%); padding: 1.25rem; }
h1 { font-size: 1.25rem; margin: 0 0 1rem; }
label { display: block; margin: 0 0 0.75rem; font-size: 0.875rem; }
input {
	display: block;
	width: 100%;
	margin-top: 0.25rem;
	padding: 0.45rem 0.65rem;
	font: inherit;
	border: 1px solid rgba(127, 127, 127, 0.5);
	border-radius: 0.375rem;
}
button {
	font: inherit;
	padding: 0.35rem 0.75rem;
	border: 1px solid rgba(127, 127, 127, 0.5);
	border-radius: 0.375rem;
	background: transparent;
	cursor: pointer;
}
</style>
</head>
<body>
<main>
<h1>Sign in</h1>
<form method="post" action="` + SessionLoginPath + `">
<label>Username
<input type="text" name="username" autocomplete="username" required data-testid="login-username">
</label>
<label>Password
<input type="password" name="password" autocomplete="current-password" required data-testid="login-password">
</label>
<button type="submit" data-testid="login-submit">Sign in</button>
</form>
</main>
</body>
</html>
`
