//go:build ignore

package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/user/better-docker-registry/internal/api"
	"github.com/user/better-docker-registry/internal/db"
	"github.com/user/better-docker-registry/internal/storage/filesystem"
)

func main() {
	root, err := os.MkdirTemp("", "bdr-smoke-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(root)

	sqlDB, err := db.OpenAndMigrate(context.Background(), filepath.Join(root, "registry.db"))
	if err != nil {
		log.Fatal(err)
	}
	q := db.New(sqlDB)
	ctx := context.Background()
	for _, name := range []string{"alpha", "test", "library/nginx", "gamma", "beta"} {
		repo, err := q.CreateRepository(ctx, name)
		if err != nil {
			log.Fatal(err)
		}
		digest := fmt.Sprintf("sha256:%064x", repo.ID)
		if err := q.UpsertManifest(ctx, db.UpsertManifestParams{
			RepoID: repo.ID, Digest: digest,
			MediaType: "application/vnd.docker.distribution.manifest.v2+json",
			Payload:   []byte("{}"), Size: 2,
		}); err != nil {
			log.Fatal(err)
		}
		for _, tag := range []string{"v1", "v2", "v3"} {
			if err := q.SetTag(ctx, db.SetTagParams{
				RepoID: repo.ID, Name: tag, ManifestDigest: digest, UpdatedAt: 1,
			}); err != nil {
				log.Fatal(err)
			}
		}
	}

	store, err := filesystem.New(filesystem.Config{Root: filepath.Join(root, "blobs")})
	if err != nil {
		log.Fatal(err)
	}
	log.Fatal(http.ListenAndServe("127.0.0.1:5056", api.NewRouter(store, q)))
}
