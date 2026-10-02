package dashboard

import (
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/user/better-docker-registry/internal/auth"
	"github.com/user/better-docker-registry/internal/db"
)

// Audit action names recorded in audit_log.action by the mutating dashboard
// flows. Keep them snake_case and stable: they are the audit trail's
// vocabulary (delete flows, plan task 17; retag flow, plan task 21).
const (
	auditActionTagDelete      = "delete_tag"
	auditActionManifestDelete = "delete_manifest"
	auditActionTagRetag       = "retag_tag"
)

// audit records one completed mutation in audit_log. target is a docker-style
// reference — name:tag for a tag, name@digest for a manifest, and
// "srcName:srcTag->dstName:newTag" for a retag — and user_id is the signed-in
// operator (NULL without a session).
//
// The insert is best-effort like the GC sweep's cleanup writes: the mutation
// has already committed and db.Querier exposes no transaction to make the
// pair atomic, so a gap in the audit trail is warned about rather than
// reported as a failure the client could still act on.
func audit(q db.Querier, r *http.Request, action, target string) {
	var userID sql.NullInt64
	if user, ok := auth.SessionUserFromContext(r.Context()); ok {
		userID = sql.NullInt64{Int64: user.ID, Valid: true}
	}
	err := q.InsertAuditEntry(r.Context(), db.InsertAuditEntryParams{
		Action:    action,
		UserID:    userID,
		Target:    target,
		CreatedAt: time.Now().Unix(),
	})
	if err != nil {
		slog.Warn("dashboard: audit insert failed", "action", action, "target", target, "err", err)
	}
}
