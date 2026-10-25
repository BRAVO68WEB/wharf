//go:build ignore

// Smoke harness for the task-7 acceptance criteria: serves the blob routes
// over a real socket backed by the filesystem driver and answers curl.
//
//	go run testdata/smoke_blobs.go
//
// Then, in another shell:
//
//	curl -I 127.0.0.1:5055/v2/test/blobs/sha256:<digest>
//	curl -H 'Range: bytes=0-99' 127.0.0.1:5055/v2/test/blobs/sha256:<digest>
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/BRAVO68WEB/wharf/internal/api"
	"github.com/BRAVO68WEB/wharf/internal/db"
	"github.com/BRAVO68WEB/wharf/internal/storage/filesystem"
)

const addr = "127.0.0.1:5055"

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

	data := []byte(strings.Repeat("0123456789", 100)) // 1000 bytes
	sum := sha256.Sum256(data)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if _, err := store.Put(context.Background(), digest, bytes.NewReader(data)); err != nil {
		log.Fatal(err)
	}

	fmt.Println("SMOKE_ADDR=" + addr)
	fmt.Println("SMOKE_DIGEST=" + digest)
	fmt.Println("SMOKE_SIZE=1000")
	log.Fatal(http.ListenAndServe(addr, api.NewRouter(store, db.New(sqlDB))))
}
