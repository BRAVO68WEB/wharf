package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// RequestIDHeader is the request/response header carrying the per-request
// correlation ID. An inbound value is reused when well-formed; otherwise one
// is generated.
const RequestIDHeader = "X-Request-ID"

// requestIDLen is the byte count of a generated request ID (32 hex chars).
const requestIDLen = 16

// healthCheckPaths are never logged: probes are high-frequency and carry no
// diagnostic signal (noise reduction).
var healthCheckPaths = map[string]struct{}{
	"/healthz": {},
	"/readyz":  {},
	"/livez":   {},
}

type requestIDKey struct{}

// RequestID returns the request ID the logging middleware attached to ctx.
func RequestID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(requestIDKey{}).(string)
	return id, ok && id != ""
}

// RequestLogger returns middleware emitting one structured slog line per
// request: method, path, status, duration, bytes and request_id. The
// Authorization header is never logged — only its presence, as REDACTED.
// Health check paths are skipped entirely. Passing a nil logger falls back to
// slog.Default().
func RequestLogger(logger *slog.Logger, next http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isHealthCheck(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		id := requestID(r)
		w.Header().Set(RequestIDHeader, id)
		ctx := context.WithValue(r.Context(), requestIDKey{}, id)

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r.WithContext(ctx))

		attrs := []slog.Attr{
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Duration("duration", time.Since(start)),
			slog.Int64("bytes", rec.bytes),
			slog.String("request_id", id),
		}
		if _, ok := r.Header["Authorization"]; ok {
			// The value is a bearer/basic credential: never log it, only the
			// fact that one was present.
			attrs = append(attrs, slog.String("authorization", "REDACTED"))
		}
		// Intentionally no header dump: cookies and tokens must not leak into
		// logs.
		logger.LogAttrs(ctx, slog.LevelInfo, "http request", attrs...)
	})
}

// isHealthCheck reports whether path is a health probe endpoint.
func isHealthCheck(path string) bool {
	_, ok := healthCheckPaths[path]
	return ok
}

// requestID returns the inbound X-Request-ID when it is a safe, bounded token;
// otherwise a fresh random ID. Inbound values are validated so a hostile
// header cannot inject arbitrary content into logs or response headers.
func requestID(r *http.Request) string {
	if inbound := r.Header.Get(RequestIDHeader); validRequestID(inbound) {
		return inbound
	}
	var buf [requestIDLen]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand failure is not recoverable here; a timestamp keeps the
		// request traceable rather than logging an empty ID.
		return strings.ReplaceAll(time.Now().UTC().Format("20060102T150405.000000000"), ".", "")
	}
	return hex.EncodeToString(buf[:])
}

// validRequestID reports whether an inbound request ID is safe to reuse:
// printable ASCII only, bounded length, no control characters.
func validRequestID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if c < 0x21 || c > 0x7e {
			return false
		}
	}
	return true
}

// statusRecorder captures the response status and byte count for the log
// line. Flush passes through so streaming responses (blob downloads) are not
// held back.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func (w *statusRecorder) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusRecorder) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.status = http.StatusOK
		w.wroteHeader = true
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}

// Flush forwards to the underlying ResponseWriter when it supports flushing,
// keeping chunked/streamed registry responses live.
func (w *statusRecorder) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap keeps http.ResponseController able to reach optional interfaces
// (Hijack, SetReadDeadline, ...) on the underlying writer.
func (w *statusRecorder) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
