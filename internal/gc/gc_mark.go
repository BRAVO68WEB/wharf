package gc

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/user/better-docker-registry/internal/db"
)

// gcRepoPage bounds one keyset page of the repository walk in the mark phase.
const gcRepoPage = 500

// mark returns every digest that must survive the sweep: each stored
// manifest's own digest plus its config, layer and (for indexes) child
// manifest digests, and every digest an active upload session still claims.
//
// A manifest whose payload cannot be parsed is treated as referencing every
// blob its repo recorded in blob_refs. That over-retains (the task-9 superset)
// but never deletes a blob a broken manifest might still need — corrupt
// metadata must cost disk, never data.
func (r *Runner) mark(ctx context.Context) (map[string]struct{}, error) {
	referenced := make(map[string]struct{})

	// An active upload session may already name a blob in the store (a
	// re-push of an existing layer, a cross-repo mount about to land). The
	// blob belongs to that in-flight upload until the session ends, so it is
	// never a sweep candidate. Expired sessions are the sweeper's to reap
	// (ListExpiredUploads); until a row is gone it is conservative to keep
	// treating its digest as live.
	uploads, err := r.q.ListActiveUploads(ctx, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	for _, u := range uploads {
		if u.Digest != "" {
			referenced[u.Digest] = struct{}{}
		}
	}

	after := ""
	for {
		repos, err := r.q.ListRepositories(ctx, db.ListRepositoriesParams{After: after, Limit: gcRepoPage})
		if err != nil {
			return nil, err
		}
		for _, repo := range repos {
			if err := r.markRepo(ctx, repo.ID, referenced); err != nil {
				return nil, err
			}
		}
		if len(repos) < gcRepoPage {
			return referenced, nil
		}
		after = repos[len(repos)-1].Name
	}
}

// markRepo adds one repository's manifest graph to referenced.
func (r *Runner) markRepo(ctx context.Context, repoID int64, referenced map[string]struct{}) error {
	manifests, err := r.q.ListManifestsByRepo(ctx, repoID)
	if err != nil {
		return err
	}
	refFallbackUsed := false
	for _, m := range manifests {
		referenced[m.Digest] = struct{}{}
		var p gcPayload
		if err := json.Unmarshal(m.Payload, &p); err != nil {
			if !refFallbackUsed {
				refFallbackUsed = true
				refs, refErr := r.q.ListBlobRefsByRepo(ctx, repoID)
				if refErr != nil {
					return refErr
				}
				for _, ref := range refs {
					referenced[ref.Digest] = struct{}{}
				}
				slog.Warn("gc: manifest payload unreadable; retaining repo blob refs", "digest", m.Digest)
			}
			continue
		}
		p.mark(referenced)
	}
	return nil
}

// gcPayload mirrors the reference-bearing fields of an OCI/Docker manifest or
// index: the config blob, the layer blobs, and (for an index) child
// manifests. Local to gc on purpose — same reasoning as task 16's
// manifestPayload: 8 lines of struct beats a cross-package dependency on
// internal/api's parse types.
type gcPayload struct {
	Config    *gcDescriptor  `json:"config"`
	Layers    []gcDescriptor `json:"layers"`
	Manifests []gcDescriptor `json:"manifests"`
}

// gcDescriptor is one OCI descriptor; GC only cares about the digest.
type gcDescriptor struct {
	Digest string `json:"digest"`
}

// mark adds every digest the payload references to referenced.
func (p gcPayload) mark(referenced map[string]struct{}) {
	if p.Config != nil && p.Config.Digest != "" {
		referenced[p.Config.Digest] = struct{}{}
	}
	for _, d := range p.Layers {
		if d.Digest != "" {
			referenced[d.Digest] = struct{}{}
		}
	}
	for _, d := range p.Manifests {
		if d.Digest != "" {
			referenced[d.Digest] = struct{}{}
		}
	}
}
