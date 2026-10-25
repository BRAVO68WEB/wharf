//go:build ignore

// Smoke harness for the JWT token endpoint (task 11). Run:
//
//	go run testdata/smoke_token.go
//
// then, in another shell:
//
//	curl -s -u alice:s3cret '127.0.0.1:5061/token?service=registry.test.example&scope=repository:library/nginx:pull,push'
//	curl -si -u alice:wrong '127.0.0.1:5061/token?service=registry.test.example'
//	curl -s 127.0.0.1:5061/v2/_keys
package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/BRAVO68WEB/wharf/internal/auth"
	"github.com/BRAVO68WEB/wharf/internal/config"
	"github.com/BRAVO68WEB/wharf/internal/db"
)

const addr = "127.0.0.1:5061"

func main() {
	root, err := os.MkdirTemp("", "bdr-smoke-token-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(root)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		log.Fatal(err)
	}
	keyPath := filepath.Join(root, "jwt_private.pem")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		log.Fatal(err)
	}

	signer, err := auth.LoadSigner(config.JWTConfig{
		PrivateKeyPath: keyPath,
		Issuer:         "wharf",
		Service:        "registry.test.example",
		TokenTTL:       config.Duration(15 * time.Minute),
	})
	if err != nil {
		log.Fatal(err)
	}

	sqlDB, err := db.OpenAndMigrate(context.Background(), filepath.Join(root, "registry.db"))
	if err != nil {
		log.Fatal(err)
	}
	q := db.New(sqlDB)
	hash, err := bcrypt.GenerateFromPassword([]byte("s3cret"), bcrypt.MinCost)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := q.CreateUser(context.Background(), db.CreateUserParams{
		Username: "alice", PasswordHash: string(hash),
	}); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle("/token", auth.NewTokenHandler(q, signer))
	mux.HandleFunc("/v2/_keys", signer.ServeJWKS)
	log.Printf("smoke token endpoint on %s (user alice / s3cret)", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
