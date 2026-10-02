package s3_test

// Configuration and credential handling for the S3 driver.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"

	s3store "github.com/user/better-docker-registry/internal/storage/s3"
)

// Given: a store
// When:  New is called with an incomplete configuration
// Then:  it fails naming the missing field instead of building a broken store
func Test_New_when_ConfigIncomplete_then_ErrorNamesField(t *testing.T) {
	cases := []struct {
		name string
		cfg  s3store.Config
		want string
	}{
		{"no bucket", s3store.Config{Region: region}, "bucket"},
		{"no region", s3store.Config{Bucket: "b"}, "region"},
		{"key without secret", s3store.Config{Region: region, Bucket: "b", AccessKey: "k"}, "secret key"},
		{"secret without key", s3store.Config{Region: region, Bucket: "b", SecretKey: "s"}, "access key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s3store.New(context.Background(), tc.cfg)
			if err == nil {
				t.Fatal("New = nil error, want failure")
			}
			if !bytes.Contains([]byte(err.Error()), []byte(tc.want)) {
				t.Fatalf("New err = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// Given: a reachable MinIO and explicit static credentials
// When:  a store is built with them and used
// Then:  the round-trip works, proving Config.AccessKey/SecretKey are honoured
func Test_Roundtrip_when_StaticCreds_then_Succeeds(t *testing.T) {
	store, err := s3store.New(context.Background(), s3store.Config{
		Endpoint:       harness.endpoint,
		Region:         region,
		Bucket:         newBucket(t),
		ForcePathStyle: true,
		AccessKey:      os.Getenv("AWS_ACCESS_KEY_ID"),
		SecretKey:      os.Getenv("AWS_SECRET_ACCESS_KEY"),
	})
	if err != nil {
		t.Fatalf("s3.New: %v", err)
	}

	ctx := context.Background()
	body, digest := blobOf(2048)
	if _, err := store.Put(ctx, digest, bytes.NewReader(body)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	rc, err := store.Get(ctx, digest)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatal("Get body differs from what was stored")
	}
}

// Given: a store
// When:  a call is made with an already-cancelled context
// Then:  it fails rather than silently reaching the backend
func Test_Store_when_ContextCancelled_then_Fails(t *testing.T) {
	store := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := store.Put(ctx, digestOf([]byte("x")), bytes.NewReader([]byte("x")))
	if err == nil {
		t.Fatal("Put with cancelled ctx = nil error, want failure")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Put err = %v, want it to match context.Canceled", err)
	}
}
