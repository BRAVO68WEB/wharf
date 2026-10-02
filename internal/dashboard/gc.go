package dashboard

import (
	"fmt"
	"net/http"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"

	"github.com/user/better-docker-registry/internal/db"
	"github.com/user/better-docker-registry/internal/gc"
	"github.com/user/better-docker-registry/internal/storage"
	"github.com/user/better-docker-registry/templates"
)

// GCHandler serves the garbage-collection page (plan task 18): stats, a
// dry-run listing of what a sweep would delete, and the real mark-and-sweep
// run. GC is manual-only — nothing here runs on a timer or at startup.
type GCHandler struct {
	runner *gc.Runner
	lock   *gc.Lock
}

// NewGCHandler returns a handler over the blobs in store with metadata in q.
// lock is the quiesce lock the run holds exclusively; /v2 write routes must
// share it (api.WithWriteQuiesce) for the 503-during-GC contract to hold.
// runnerOpts tune the engine (gc.WithBatchSize for config gc.batch_size).
func NewGCHandler(q db.Querier, store storage.Storage, lock *gc.Lock, runnerOpts ...gc.RunnerOption) *GCHandler {
	return &GCHandler{runner: gc.NewRunner(q, store, runnerOpts...), lock: lock}
}

// Mount registers the GC routes on r. Paths are absolute, matching
// auth.SessionManager.Mount and NewRepoHandler — register on the root mux.
func (h *GCHandler) Mount(r chi.Router) {
	r.Get("/dashboard/gc", h.servePage)
	r.Post("/dashboard/gc/dry-run", h.serveDryRun)
	r.Post("/dashboard/gc/run", h.serveRun)
}

// gcBlob is one collectable blob row: content digest and stored size.
type gcBlob struct {
	Digest string
	Size   int64
}

// gcStats is the summary panel data (plan task 18's "GC stats").
type gcStats struct {
	TotalBlobs        int64
	ReferencedBlobs   int64
	UnreferencedBlobs int64
	UnreferencedBytes int64
}

// gcPanelData is the view model of the whole GC page.
type gcPanelData struct {
	Stats          gcStats
	CSRFToken      string
	ConfirmMessage string
}

// gcResultsData is the swappable outcome of a dry run or a real run. Heading
// and Run distinguish the two without a variant type; the template stays
// presentation-only.
type gcResultsData struct {
	Heading string
	Blobs   []gcBlob
	Bytes   int64
	Failed  int
	Run     bool
}

// servePage renders the GC page: collection stats, the dry-run and run
// buttons (the run button carries the confirmation dialog) and an empty
// #gc-results target for the HTMX swaps.
func (h *GCHandler) servePage(w http.ResponseWriter, r *http.Request) {
	stats, err := h.runner.Stats(r.Context())
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	data := h.panelData(r, stats)
	h.render(w, r, GcPanel(data), GcPanelWithResults(data, gcResultsData{}))
}

// serveDryRun lists exactly the blobs a real run would delete. It never
// touches the store — the button is safe to press at any time.
func (h *GCHandler) serveDryRun(w http.ResponseWriter, r *http.Request) {
	blobs, err := h.runner.DryRun(r.Context())
	if err != nil {
		h.renderError(w, r, "dry run failed: internal server error")
		return
	}
	res := gcResultsData{
		Heading: fmt.Sprintf("Dry run: %d blob(s) would be deleted, %s freed", len(blobs), FormatBytes(gcBytes(blobs))),
		Blobs:   toGCBlobs(blobs),
		Bytes:   gcBytes(blobs),
	}
	h.render(w, r, GcResults(res), GcPanelWithResults(h.mustPanel(r), res))
}

// serveRun executes the real mark-and-sweep under the quiesce lock. A second
// concurrent request is refused rather than queued — the plan's confirmation
// flow means only one sweep should ever be in flight.
func (h *GCHandler) serveRun(w http.ResponseWriter, r *http.Request) {
	release, ok := h.lock.BeginGC()
	if !ok {
		h.renderError(w, r, "a collection is already running")
		return
	}
	defer release()

	result, err := h.runner.Run(r.Context())
	if err != nil {
		h.renderError(w, r, "collection failed: internal server error")
		return
	}
	res := gcResultsData{
		Heading: fmt.Sprintf("Collection complete: %d blob(s) deleted, %s freed", len(result.Deleted), FormatBytes(result.BytesFreed)),
		Blobs:   toGCBlobs(result.Deleted),
		Bytes:   result.BytesFreed,
		Failed:  result.Failed,
		Run:     true,
	}
	h.render(w, r, GcResults(res), GcPanelWithResults(h.mustPanel(r), res))
}

// panelData builds the page model, including the confirmation dialog text
// with live counts ("This will delete N blobs, total X GB. Continue?"). The
// engine's Stats is mapped into the view model here — one boundary, one copy.
func (h *GCHandler) panelData(r *http.Request, s gc.Stats) gcPanelData {
	return gcPanelData{
		Stats: gcStats{
			TotalBlobs:        s.TotalBlobs,
			ReferencedBlobs:   s.ReferencedBlobs,
			UnreferencedBlobs: s.UnreferencedBlobs,
			UnreferencedBytes: s.UnreferencedBytes,
		},
		CSRFToken:      sessionCSRF(r),
		ConfirmMessage: gcConfirmMessage(s.UnreferencedBlobs, s.UnreferencedBytes),
	}
}

// mustPanel re-renders the shell data for non-HTMX callers. A dry run or run
// has already succeeded here, so a stats reload failure cannot happen for any
// reason the response could still correct — an empty summary is the honest
// degrade rather than a second error page.
func (h *GCHandler) mustPanel(r *http.Request) gcPanelData {
	stats, err := h.runner.Stats(r.Context())
	if err != nil {
		return h.panelData(r, gc.Stats{})
	}
	return h.panelData(r, stats)
}

// render writes the fragment (HTMX) or the full page (direct navigation),
// mirroring the tags/manifests handlers.
func (h *GCHandler) render(w http.ResponseWriter, r *http.Request, fragment templ.Component, page templ.Component) {
	component := fragment
	if !isHTMX(r) {
		component = templates.Layout(templates.Chrome{
			Title:     "Garbage collection",
			Username:  sessionUser(r),
			CSRFToken: sessionCSRF(r),
		}, page)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = component.Render(r.Context(), w)
}

// renderError shows an error fragment (already-running, engine failure) with
// a 200 so HTMX actually swaps it into view — same choice the manifest detail
// view makes for its parse-error state.
func (h *GCHandler) renderError(w http.ResponseWriter, r *http.Request, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if isHTMX(r) {
		_ = GcError(message).Render(r.Context(), w)
		return
	}
	_ = templates.Layout(templates.Chrome{
		Title:     "Garbage collection",
		Username:  sessionUser(r),
		CSRFToken: sessionCSRF(r),
	}, GcPanelWithResults(h.mustPanel(r), gcResultsData{Heading: message})).Render(r.Context(), w)
}

// gcConfirmMessage is the plan's literal confirmation dialog with live
// counts filled in: "This will delete N blobs, total X GB. Continue?".
func gcConfirmMessage(n int64, bytes int64) string {
	return fmt.Sprintf("This will delete %d blobs, total %s. Continue?", n, FormatBytes(bytes))
}

// gcBytes sums the sizes of a blob listing.
func gcBytes(blobs []gc.Blob) int64 {
	var total int64
	for _, b := range blobs {
		total += b.Size
	}
	return total
}

// toGCBlobs maps engine rows into view rows.
func toGCBlobs(blobs []gc.Blob) []gcBlob {
	out := make([]gcBlob, 0, len(blobs))
	for _, b := range blobs {
		out = append(out, gcBlob{Digest: b.Digest, Size: b.Size})
	}
	return out
}
