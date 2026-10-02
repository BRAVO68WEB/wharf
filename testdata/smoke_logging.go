//go:build ignore

// Smoke harness for task 14 (middleware). Serves one request carrying an
// Authorization credential through RequestLogger writing JSON to a real file,
// so the acceptance grep ('Bearer eyJ' -> 0 matches) runs against actual log
// output rather than a test buffer.
//
// Run: go run testdata/smoke_logging.go
package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/user/better-docker-registry/internal/middleware"
)

const logPath = "/tmp/bdr-smoke-request.log"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "smoke: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	logFile, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("create log file: %w", err)
	}
	defer logFile.Close()

	logger := slog.New(slog.NewJSONHandler(logFile, nil))
	h := middleware.RequestLogger(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodPost, "/dashboard/repos/test/delete", nil)
	req.Header.Set("Authorization", "Bearer eyJhbGciOiJIUzI1NiJ9.payload.sig")
	req = req.WithContext(middleware.WithCSRFToken(req.Context(), "smoke-token"))
	req.Header.Set(middleware.CSRFHeader, "smoke-token")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	h2 := middleware.SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rr2 := httptest.NewRecorder()
	h2.ServeHTTP(rr2, httptest.NewRequest(http.MethodGet, "/dashboard/repos", nil))

	if err := logFile.Sync(); err != nil {
		return fmt.Errorf("sync log file: %w", err)
	}

	logged, err := os.ReadFile(logPath)
	if err != nil {
		return fmt.Errorf("read log file: %w", err)
	}
	fmt.Printf("log file: %s\n", logPath)
	fmt.Printf("log content:\n%s\n", logged)
	fmt.Printf("security headers on /dashboard/repos: X-Content-Type-Options=%q X-Frame-Options=%q CSP set=%v\n",
		rr2.Header().Get("X-Content-Type-Options"),
		rr2.Header().Get("X-Frame-Options"),
		rr2.Header().Get("Content-Security-Policy") != "")
	fmt.Printf("csrf POST with token status: %d\n", rr.Code)
	if strings.Contains(string(logged), "Bearer eyJ") {
		return fmt.Errorf("FAIL: Authorization value leaked into log")
	}
	if !strings.Contains(string(logged), `"authorization":"REDACTED"`) {
		return fmt.Errorf("FAIL: redaction marker missing from log")
	}
	fmt.Println("redaction check: PASS (grep 'Bearer eyJ' -> 0 matches)")
	return nil
}
