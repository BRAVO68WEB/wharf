// Command registry serves the better-docker-registry OCI Distribution Spec
// registry and the dashboard embedded in this binary.
//
// Startup wires configuration, the SQLite metadata store, the blob storage
// backend, and the health endpoints. Shutdown is graceful: SIGINT/SIGTERM
// drain in-flight requests (upload sessions included) within 30s, or abort
// them with an error naming what was cut.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-chi/chi/v5"

	"github.com/user/better-docker-registry/internal/api"
	"github.com/user/better-docker-registry/internal/auth"
	"github.com/user/better-docker-registry/internal/config"
	"github.com/user/better-docker-registry/internal/dashboard"
	"github.com/user/better-docker-registry/internal/db"
	"github.com/user/better-docker-registry/internal/gc"
	"github.com/user/better-docker-registry/internal/middleware"
	"github.com/user/better-docker-registry/internal/storage"
	"github.com/user/better-docker-registry/internal/storage/filesystem"
	"github.com/user/better-docker-registry/internal/storage/s3"
	"github.com/user/better-docker-registry/site"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "registry: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("registry", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to configuration file (required)")
	addr := fs.String("addr", "", "HTTP listen address (overrides server.port from config)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	listenAddr := *addr
	if listenAddr == "" {
		listenAddr = cfg.Server.ListenAddr()
	}

	// Cancels on SIGINT/SIGTERM; everything below drains on that cancel.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sqlDB, err := initDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	if err := seedAdminUser(ctx, db.New(sqlDB)); err != nil {
		return err
	}

	store, err := initStorage(ctx, cfg)
	if err != nil {
		return err
	}

	health := api.NewHealth(sqlDB, store)
	handler, err := newMux(ctx, health, store, db.New(sqlDB), cfg)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", listenAddr, err)
	}

	srv := newServer(ln, handler, health)
	// Config-driven and zero by default: the registry streams multi-GB
	// blobs, so a non-zero Read/WriteTimeout would truncate them.
	srv.http.ReadTimeout = cfg.Server.ReadTimeout.Duration()
	srv.http.WriteTimeout = cfg.Server.WriteTimeout.Duration()
	return srv.Serve(ctx)
}

// newMux assembles the full route table: health probes, the /token auth
// service, the dashboard JSON API behind session + CSRF, the embedded
// dashboard files, and the /v2 registry API. /v2 is the fallback handler:
// everything the root router does not claim goes to it (and 404s there), so
// no path is ever silently unrouted. The SPA fallback never owns /v2 or
// /dashboard/api.
//
// Mount order inside the dashboard group is the contract task 15 pinned:
// RequireSession injects the session, WithSessionCSRF copies its token into
// middleware.CSRF's context slot, then CSRF enforces it. Login stays outside
// the group: it is the request that creates a session and cannot carry its
// CSRF token yet. GET /dashboard/login is a client route served by the SPA.
func newMux(ctx context.Context, health *api.Health, store storage.Storage, q db.Querier, cfg *config.Config) (http.Handler, error) {
	created, err := auth.EnsureRSAPrivateKey(cfg.Auth.JWT.PrivateKeyPath)
	if err != nil {
		return nil, err
	}
	if created {
		slog.Info("generated jwt signing key", "path", cfg.Auth.JWT.PrivateKeyPath)
	}
	signer, err := auth.LoadSigner(cfg.Auth.JWT)
	if err != nil {
		return nil, err
	}
	sessions, err := auth.NewSessionManager(q, cfg.Auth.Session, false)
	if err != nil {
		return nil, err
	}
	token := auth.NewTokenHandler(q, signer)

	lock := gc.NewLock()
	v2 := api.NewRouter(store, q,
		api.WithWriteQuiesce(lock.WriteGuard),
		api.WithJWKS(http.HandlerFunc(signer.ServeJWKS)),
		api.WithMaxUploadSize(cfg.Server.MaxUploadSize),
	)
	if !cfg.Auth.DevAnonymous {
		v2 = middleware.BearerAuth(signer, cfg.Auth.JWT.Service)(v2)
	} else {
		slog.Warn("auth.dev_anonymous: /v2/* is served WITHOUT bearer auth — dev only")
	}

	pullHost, err := cfg.Server.PullHost()
	if err != nil {
		return nil, err
	}

	root := chi.NewRouter()
	root.Get("/healthz", health.Liveness)
	root.Get("/readyz", health.Readiness)
	// All methods: docker probes POST /token first; the handler answers the
	// GET/POST contract and 405s everything else itself.
	root.Handle("/token", http.HandlerFunc(token.ServeHTTP))
	root.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dashboard/", http.StatusFound)
	})
	root.Post(auth.SessionLoginPath, sessions.HandleLogin)
	spa := site.Handler()
	root.Route("/dashboard", func(dash chi.Router) {
		dash.Post("/api/login", sessions.HandleLoginJSON)
		apiHandler := dashboard.NewHTTPAPI(q, store, lock, gc.WithBatchSize(cfg.GC.BatchSize))
		apiHandler.SetPullHost(pullHost)
		apiHandler.SetGCInterval(cfg.GC.Interval.Duration())
		gc.StartSchedule(ctx, cfg.GC.Interval.Duration(), func(ctx context.Context) error {
			ran, err := apiHandler.CollectIfIdle(ctx)
			if err == nil && !ran {
				slog.Info("gc: scheduled run skipped, a collection is already running")
			}
			return err
		})
		apiHandler.MountPublic(dash)
		dash.Group(func(authed chi.Router) {
			authed.Use(sessions.RequireSession)
			authed.Use(dashboard.WithSessionCSRF)
			authed.Use(middleware.CSRF)
			authed.Get("/api/session", sessions.HandleSessionJSON)
			authed.Post("/api/logout", sessions.HandleLogoutJSON)
			apiHandler.Mount(authed)
		})
		// GET and HEAD only. A catch-all that accepted POST would race the
		// JSON routes for methods they do not register.
		dash.Method(http.MethodGet, "/", spa)
		dash.Method(http.MethodHead, "/", spa)
		dash.Method(http.MethodGet, "/*", spa)
		dash.Method(http.MethodHead, "/*", spa)
	})
	root.NotFound(v2.ServeHTTP)

	return middleware.RequestLogger(nil, middleware.SecurityHeaders(root)), nil
}

// initDB opens the metadata database and applies pending migrations.
func initDB(ctx context.Context, cfg *config.Config) (*sql.DB, error) {
	sqlDB, err := db.OpenAndMigrate(ctx, cfg.Database.Path)
	if err != nil {
		return nil, err
	}
	slog.Info("database ready", "path", cfg.Database.Path)
	return sqlDB, nil
}

// initStorage constructs the blob storage backend. Credentials for the s3
// backend resolve from the environment variables NAMED in config (never from
// the file); empty names leave the keys unset and the AWS SDK default
// credential chain applies.
func initStorage(ctx context.Context, cfg *config.Config) (storage.Storage, error) {
	switch cfg.Storage.Type {
	case config.StorageFilesystem:
		store, err := filesystem.New(filesystem.Config{
			Root:        cfg.Storage.Path,
			MaxBlobSize: cfg.Server.MaxUploadSize,
		})
		if err != nil {
			return nil, err
		}
		slog.Info("blob storage ready", "type", cfg.Storage.Type, "path", cfg.Storage.Path)
		return store, nil
	case config.StorageS3:
		store, err := s3.New(ctx, s3.Config{
			Endpoint:       cfg.Storage.S3.Endpoint,
			Region:         cfg.Storage.S3.Region,
			Bucket:         cfg.Storage.S3.Bucket,
			ForcePathStyle: cfg.Storage.S3.ForcePathStyle,
			AccessKey:      os.Getenv(cfg.Storage.S3.AccessKeyEnv),
			SecretKey:      os.Getenv(cfg.Storage.S3.SecretKeyEnv),
		})
		if err != nil {
			return nil, err
		}
		slog.Info("blob storage ready", "type", cfg.Storage.Type, "bucket", cfg.Storage.S3.Bucket)
		return store, nil
	default:
		return nil, fmt.Errorf("storage.type must be %q or %q, got %q",
			config.StorageFilesystem, config.StorageS3, cfg.Storage.Type)
	}
}
