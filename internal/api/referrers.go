package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/BRAVO68WEB/wharf/internal/db"
	"github.com/BRAVO68WEB/wharf/internal/storage"
)

// ReferrersHandler serves the OCI referrers API:
// GET /v2/{name}/referrers/{digest} lists the manifests in the repository
// whose "subject" is the addressed digest. The response is an image index;
// an unknown subject or repository yields an empty index, never a 404, so
// discovery clients can poll unconditionally.
type ReferrersHandler struct {
	q db.Querier
}

// NewReferrersHandler returns the referrers endpoint over the manifests in q.
func NewReferrersHandler(q db.Querier) *ReferrersHandler {
	return &ReferrersHandler{q: q}
}

// Mount registers the referrers route on r, a /v2 route tree.
func (h *ReferrersHandler) Mount(r chi.Router) {
	r.Get("/{name}/referrers/{digest}", h.list)
}

// referrerEntry is one manifest descriptor as the referrers index reports it.
type referrerEntry struct {
	MediaType    string            `json:"mediaType"`
	Digest       string            `json:"digest"`
	Size         int64             `json:"size"`
	ArtifactType string            `json:"artifactType,omitempty"`
	Annotations  map[string]string `json:"annotations,omitempty"`
}

// referrerIndex is the referrers response body.
type referrerIndex struct {
	SchemaVersion int             `json:"schemaVersion"`
	MediaType     string          `json:"mediaType"`
	Manifests     []referrerEntry `json:"manifests"`
}

func (h *ReferrersHandler) list(w http.ResponseWriter, r *http.Request) {
	name := RepoName(r)
	subject := chi.URLParam(r, "digest")
	if !ValidName(name) {
		WriteError(w, storage.NewError(storage.ErrorCodeNameInvalid, "invalid repository name"))
		return
	}
	if !ValidDigest(subject) {
		WriteError(w, storage.NewError(storage.ErrorCodeDigestInvalid, "invalid content digest"))
		return
	}

	entries := []referrerEntry{}
	repo, err := h.q.GetRepositoryByName(r.Context(), name)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		WriteError(w, internalFailure())
		return
	default:
		entries, err = h.collect(r, repo.ID, subject)
		if err != nil {
			WriteError(w, internalFailure())
			return
		}
	}

	filter := r.URL.Query().Get("artifactType")
	if filter != "" {
		entries = filterArtifactType(entries, filter)
		w.Header().Set("OCI-Filters-Applied", "artifactType")
	}
	w.Header().Set("Content-Type", MediaTypeOCIIndex)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(referrerIndex{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCIIndex,
		Manifests:     entries,
	})
}

// collect returns the descriptors of the repository manifests whose subject
// is subject. Subjects are read from the stored payloads: referrers is a
// discovery surface, not a hot path, and parsing avoids a derived index that
// could drift from the manifest truth.
func (h *ReferrersHandler) collect(r *http.Request, repoID int64, subject string) ([]referrerEntry, error) {
	rows, err := h.q.ListManifestsByRepo(r.Context(), repoID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return []referrerEntry{}, nil
		}
		return nil, err
	}
	entries := make([]referrerEntry, 0, len(rows))
	for _, m := range rows {
		var shape manifestShape
		if err := json.Unmarshal(m.Payload, &shape); err != nil || shape.Subject == nil {
			continue
		}
		if shape.Subject.Digest != subject {
			continue
		}
		entry := referrerEntry{
			MediaType:    m.MediaType,
			Digest:       m.Digest,
			Size:         m.Size,
			ArtifactType: shape.ArtifactType,
			Annotations:  shape.Annotations,
		}
		if entry.ArtifactType == "" && shape.Config != nil {
			entry.ArtifactType = shape.Config.MediaType
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// filterArtifactType keeps the entries whose artifact type matches, filling
// the count into the response header contract (OCI-Filters-Applied).
func filterArtifactType(entries []referrerEntry, artifactType string) []referrerEntry {
	kept := make([]referrerEntry, 0, len(entries))
	for _, e := range entries {
		if e.ArtifactType == artifactType {
			kept = append(kept, e)
		}
	}
	return kept
}
