package dashboard

import (
	"context"
	"encoding/json"
	"io"
	"sort"
	"strings"

	"github.com/BRAVO68WEB/wharf/internal/storage"
)

// configBlobMaxBytes bounds the config-blob read. Image config blobs are a
// few KB, so a limit rather than a full stream: a blob larger than this is
// not a container config the detail view can meaningfully show, and it must
// not be buffered whole just because the caller asked.
const configBlobMaxBytes = 1 << 20

// containerConfigDisplay is the structured view of an image's container
// configuration — the "config" object of the image config blob. Only the
// fields the detail view shows are held; the blob itself is never rendered.
type containerConfigDisplay struct {
	Cmd          []string
	Entrypoint   []string
	Env          []string
	WorkingDir   string
	ExposedPorts []string
	User         string
}

// ImageLayer is one step of an image's build history, paired with the
// manifest layer it produced. Empty steps (ENV, CMD, EXPOSE) have no digest
// and a zero size. Command is the full created_by text: the detail view wraps
// it and never shortens it. A shortened command hides the instruction.
type ImageLayer struct {
	Command string
	Size    int64
	Digest  string
}

// historyEntry is one object of the image config's history array.
type historyEntry struct {
	CreatedBy  string `json:"created_by"`
	Comment    string `json:"comment"`
	EmptyLayer bool   `json:"empty_layer"`
}

// configBlob mirrors the JSON shape of an OCI/Docker image config blob.
// The detail view reads the container config object and the history array.
// The blob itself is never rendered.
type configBlob struct {
	Config struct {
		User         string              `json:"User"`
		Env          []string            `json:"Env"`
		Entrypoint   []string            `json:"Entrypoint"`
		Cmd          []string            `json:"Cmd"`
		WorkingDir   string              `json:"WorkingDir"`
		ExposedPorts map[string]struct{} `json:"ExposedPorts"`
	} `json:"config"`
	History []historyEntry `json:"history"`
}

// display turns the decoded blob into the structured view. Exposed port keys
// are sorted so the rendered list is stable.
func (b configBlob) display() *containerConfigDisplay {
	c := &containerConfigDisplay{
		Cmd:        b.Config.Cmd,
		Entrypoint: b.Config.Entrypoint,
		Env:        b.Config.Env,
		WorkingDir: b.Config.WorkingDir,
		User:       b.Config.User,
	}
	if n := len(b.Config.ExposedPorts); n > 0 {
		c.ExposedPorts = make([]string, 0, n)
		for p := range b.Config.ExposedPorts {
			c.ExposedPorts = append(c.ExposedPorts, p)
		}
		sort.Strings(c.ExposedPorts)
	}
	return c
}

// imageLayers pairs history with the manifest's layer descriptors, oldest
// first. An empty_layer step takes no descriptor. Every other step consumes
// the next descriptor, which is how the image spec lines history up with
// layers. Descriptors history does not mention are appended, so a layer is
// never dropped because the history array is short.
func (b configBlob) imageLayers(layers []layerDisplay) []ImageLayer {
	if len(b.History) == 0 {
		return nil
	}
	out := make([]ImageLayer, 0, len(b.History)+len(layers))
	next := 0
	for _, h := range b.History {
		row := ImageLayer{Command: layerCommand(h)}
		if h.EmptyLayer {
			out = append(out, row)
			continue
		}
		if next < len(layers) {
			row.Size = layers[next].Size
			row.Digest = layers[next].Digest
			next++
		}
		out = append(out, row)
	}
	for ; next < len(layers); next++ {
		out = append(out, ImageLayer{
			Command: layers[next].Digest,
			Size:    layers[next].Size,
			Digest:  layers[next].Digest,
		})
	}
	return out
}

// nopMarker is the classic builder prefix on a Dockerfile instruction that
// did not run a shell. The instruction that follows it is the layer command.
const nopMarker = "#(nop)"

// layerCommand returns the full history command. The #(nop) wrapper is
// removed when an instruction follows it; the instruction itself, including
// a long ADD file digest or a long RUN script, is kept verbatim.
func layerCommand(h historyEntry) string {
	s := strings.TrimSpace(h.CreatedBy)
	if s == "" {
		s = strings.TrimSpace(h.Comment)
	}
	if s == "" {
		return "(no command recorded)"
	}
	if i := strings.Index(s, nopMarker); i >= 0 {
		if rest := strings.TrimSpace(s[i+len(nopMarker):]); rest != "" {
			return rest
		}
	}
	return s
}

// loadImageDetails reads the image config blob at digest. Absence is a nil
// config and no history, not an error: a blob the store does not hold (or
// one that is oversized or malformed) leaves those sections out, while the
// manifest's own metadata and layer table still render.
func loadImageDetails(ctx context.Context, store storage.Storage, digest string, layers []layerDisplay) (*containerConfigDisplay, []ImageLayer) {
	if store == nil || digest == "" {
		return nil, nil
	}
	rc, err := store.Get(ctx, digest)
	if err != nil {
		return nil, nil
	}
	defer func() { _ = rc.Close() }()

	raw, err := io.ReadAll(io.LimitReader(rc, configBlobMaxBytes))
	if err != nil {
		return nil, nil
	}
	var b configBlob
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, nil
	}
	return b.display(), b.imageLayers(layers)
}
