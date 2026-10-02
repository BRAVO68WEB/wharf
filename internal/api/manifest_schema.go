package api

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"mime"
	"strings"

	"github.com/user/better-docker-registry/internal/storage"
)

// Manifest media types the registry accepts and serves. Docker schema1 is
// deliberately absent: it is deprecated, and a push of one is refused.
const (
	MediaTypeDockerManifest = "application/vnd.docker.distribution.manifest.v2+json"
	MediaTypeDockerList     = "application/vnd.docker.distribution.manifest.list.v2+json"
	MediaTypeOCIManifest    = "application/vnd.oci.image.manifest.v1+json"
	MediaTypeOCIIndex       = "application/vnd.oci.image.index.v1+json"
)

// schema1MediaTypes are recognised only to be refused with a clear error.
var schema1MediaTypes = map[string]bool{
	"application/vnd.docker.distribution.manifest.v1+json":      true,
	"application/vnd.docker.distribution.manifest.v1+prettyjws": true,
}

// maxManifestSize caps a manifest payload at the same 4 MiB Docker's
// registry accepts. The body is read through io.LimitReader, never
// unbounded, so a client cannot OOM the process; anything larger is refused
// with SIZE_INVALID rather than truncated. A single bounded copy is
// unavoidable: database/sql binds a BLOB as []byte, so the payload must be
// whole before the write and is re-served verbatim from the row.
const maxManifestSize = 4 << 20

// descriptor is the part of an OCI descriptor the registry validates: the
// digest a layer, config or child manifest is addressed by.
type descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
}

// manifestShape is the union of the fields of every supported manifest type.
// Pointers distinguish an absent field from a present-but-empty one: a
// missing "layers" is invalid while "layers": [] is a legal empty layer list.
type manifestShape struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType"`
	ArtifactType  string            `json:"artifactType"`
	Annotations   map[string]string `json:"annotations"`
	Subject       *descriptor       `json:"subject"`
	Config        *descriptor       `json:"config"`
	Layers        *[]descriptor     `json:"layers"`
	Manifests     *[]descriptor     `json:"manifests"`
}

// manifestInfo is everything a manifest PUT records: the media type it is
// stored under, the digest its payload hashes to, the digests it references
// (config + layers, or the entries of an index), and the referrers index
// fields (subject, artifact type, annotations).
type manifestInfo struct {
	mediaType    string
	digest       string
	references   []string
	subject      string
	artifactType string
	annotations  map[string]string
}

// parseManifest validates one manifest payload and describes it. contentType
// is the request's Content-Type; when it names a supported media type that
// wins, otherwise the payload's own mediaType field decides, and failing that
// the shape of the payload does. algo names the hash algorithm the digest is
// computed with ("sha256" or "sha512"); it comes from a digest reference or
// defaults to the canonical sha256. The returned error is the wire error the
// handler must write.
func parseManifest(contentType, algo string, payload []byte) (manifestInfo, *storage.Error) {
	var shape manifestShape
	if err := json.Unmarshal(payload, &shape); err != nil {
		return manifestInfo{}, invalidManifest("manifest is not a valid JSON object")
	}
	if shape.SchemaVersion == 1 {
		return manifestInfo{}, schema1Error()
	}

	mediaType, merr := resolveMediaType(contentType, shape)
	if merr != nil {
		return manifestInfo{}, merr
	}
	if shape.SchemaVersion != 2 {
		return manifestInfo{}, invalidManifest("manifest schemaVersion must be 2")
	}

	info := manifestInfo{
		mediaType:    mediaType,
		digest:       digestWithAlgo(algo, payload),
		artifactType: shape.ArtifactType,
		annotations:  shape.Annotations,
	}
	if shape.Subject != nil {
		info.subject = shape.Subject.Digest
	}
	if info.artifactType == "" && shape.Config != nil {
		info.artifactType = shape.Config.MediaType
	}
	var verr *storage.Error
	if mediaType == MediaTypeDockerList || mediaType == MediaTypeOCIIndex {
		verr = indexReferences(&info, shape)
	} else {
		verr = imageReferences(&info, shape)
	}
	if verr != nil {
		return manifestInfo{}, verr
	}
	return info, nil
}

// indexReferences validates a manifest list / image index and collects the
// digests of its entries.
func indexReferences(info *manifestInfo, shape manifestShape) *storage.Error {
	if shape.Manifests == nil {
		return invalidManifest("index does not list any manifests field")
	}
	for i, entry := range *shape.Manifests {
		if schema1MediaTypes[strings.ToLower(entry.MediaType)] {
			return schema1Error()
		}
		if !ValidDigest(entry.Digest) {
			return invalidManifest(fmt.Sprintf("manifests[%d] has an invalid digest", i))
		}
		info.references = append(info.references, entry.Digest)
	}
	return nil
}

// imageReferences validates an image manifest and collects the digests of its
// config and layers.
func imageReferences(info *manifestInfo, shape manifestShape) *storage.Error {
	if shape.Config == nil {
		return invalidManifest("manifest does not have a config")
	}
	if !ValidDigest(shape.Config.Digest) {
		return invalidManifest("config has an invalid digest")
	}
	info.references = append(info.references, shape.Config.Digest)

	if shape.Layers == nil {
		return invalidManifest("manifest does not have a layers field")
	}
	for i, layer := range *shape.Layers {
		if !ValidDigest(layer.Digest) {
			return invalidManifest(fmt.Sprintf("layers[%d] has an invalid digest", i))
		}
		info.references = append(info.references, layer.Digest)
	}
	return nil
}

// resolveMediaType picks the media type a manifest is stored and served
// under: the request's Content-Type if it names a supported type, the
// payload's mediaType field otherwise, or the type implied by the payload's
// shape when neither declares one. Unrecognised declarations are refused.
func resolveMediaType(contentType string, shape manifestShape) (string, *storage.Error) {
	ct, _, ctErr := mime.ParseMediaType(contentType)
	if ctErr != nil {
		ct = "" // absent or unparseable: the payload decides
	}
	switch {
	case supportedMediaType(ct):
		return ct, nil
	case schema1MediaTypes[ct]:
		return "", schema1Error()
	}

	declared := strings.ToLower(shape.MediaType)
	switch {
	case supportedMediaType(declared):
		return declared, nil
	case schema1MediaTypes[declared]:
		return "", schema1Error()
	}

	switch {
	case shape.Config != nil && shape.Layers != nil:
		return MediaTypeDockerManifest, nil
	case shape.Manifests != nil:
		return MediaTypeDockerList, nil
	default:
		return "", invalidManifest("manifest media type is missing or unsupported")
	}
}

func supportedMediaType(mediaType string) bool {
	switch mediaType {
	case MediaTypeDockerManifest, MediaTypeDockerList, MediaTypeOCIManifest, MediaTypeOCIIndex:
		return true
	default:
		return false
	}
}

// digestWithAlgo is the content address of a manifest payload under the
// named hash algorithm; an empty or unknown algo means the canonical sha256.
func digestWithAlgo(algo string, payload []byte) string {
	var h hash.Hash
	switch algo {
	case "sha512":
		h = sha512.New()
	default:
		algo, h = "sha256", sha256.New()
	}
	h.Write(payload)
	return algo + ":" + hex.EncodeToString(h.Sum(nil))
}

func invalidManifest(message string) *storage.Error {
	return storage.NewError(storage.ErrorCodeManifestInvalid, message)
}

func schema1Error() *storage.Error {
	return invalidManifest("docker schema1 manifests are not supported")
}
