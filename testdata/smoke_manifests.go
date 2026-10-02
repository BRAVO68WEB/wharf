//go:build ignore

// Smoke harness for the task-9 QA scenarios: serves the manifest routes over
// a real socket backed by the filesystem driver and a real SQLite database.
//
//	go run testdata/smoke_manifests.go
//
// Then, in another shell:
//
//	curl -i -X PUT -H 'Content-Type: application/vnd.oci.image.manifest.v1+json' \
//	  --data-binary @manifest.json 127.0.0.1:5059/v2/test/manifests/latest
//	curl -i 127.0.0.1:5059/v2/test/manifests/latest
//	curl -I 127.0.0.1:5059/v2/test/manifests/latest
//	curl -i -X DELETE 127.0.0.1:5059/v2/test/manifests/sha256:<digest>
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

const addr = "127.0.0.1:5059"

func main() {
	root, err := os.MkdirTemp("", "bdr-smoke-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(root)

	store, err := filesystem.New(filesystem.Config{Root: root})
	if err != nil {
		log.Fatal(err)
	}

	sqlDB, err := db.OpenAndMigrate(context.Background(), filepath.Join(root, "registry.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer sqlDB.Close()

	fmt.Println("SMOKE_ADDR=" + addr)
	log.Fatal(http.ListenAndServe(addr, api.NewRouter(store, db.New(sqlDB))))
}
