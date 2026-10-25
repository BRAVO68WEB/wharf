package s3_test

// Shared MinIO lifecycle for the S3 driver tests.
//
// Target selection, in order:
//
//  1. BDR_S3_TEST_ENDPOINT is set -> use that already-running S3-compatible
//     server (credentials from BDR_S3_TEST_ACCESS_KEY / BDR_S3_TEST_SECRET_KEY,
//     defaulting to minioadmin/minioadmin). This is the docker-compose path.
//  2. a `minio` binary is on PATH -> run `minio server <dir>` locally.
//  3. docker is available -> run the image named by BDR_S3_TEST_IMAGE
//     (default minio/minio) as `server /data`.
//  4. none of the above -> every test skips, naming the reason.
//
// Tier 2 matters because Docker Hub deleted the minio/minio repository in
// September 2026 (upstream archived MinIO Community Edition in April 2026).
// A local binary runs the same `minio server` command without depending on a
// registry that may not serve the image.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	s3store "github.com/BRAVO68WEB/wharf/internal/storage/s3"
)

const (
	envEndpoint  = "BDR_S3_TEST_ENDPOINT"
	envAccessKey = "BDR_S3_TEST_ACCESS_KEY"
	envSecretKey = "BDR_S3_TEST_SECRET_KEY"
	envImage     = "BDR_S3_TEST_IMAGE"

	// region is the signing region. MinIO accepts any non-empty value; the
	// AWS SDK always requires one.
	region = "us-east-1"

	// MinIO's documented default root credentials, used only for the
	// throwaway test server.
	minioAccessKey = "minioadmin"
	minioSecretKey = "minioadmin"

	// defaultImage is the container image the plan names. Docker Hub deleted
	// this repository in September 2026; set BDR_S3_TEST_IMAGE to point at
	// quay.io/minio/minio or another mirror.
	defaultImage = "minio/minio"

	readyTimeout = 60 * time.Second
)

// minioHarness is a reachable S3-compatible server plus its endpoint.
type minioHarness struct {
	endpoint string
	stop     func()
}

var (
	harness    *minioHarness
	harnessErr error
)

func TestMain(m *testing.M) {
	h, err := startMinIO()
	harness, harnessErr = h, err
	code := m.Run()
	if h != nil {
		h.stop()
	}
	os.Exit(code)
}

// newStore returns a store backed by a fresh, empty bucket. The bucket is
// emptied and removed when the test finishes, so List-based tests see only
// what the test itself stored.
//
// Credentials are deliberately left empty so the store is built through the
// AWS SDK default credential chain, which the harness feeds from the
// environment.
func newStore(t *testing.T) *s3store.Store {
	t.Helper()
	store, err := s3store.New(context.Background(), s3store.Config{
		Endpoint:       harness.endpoint,
		Region:         region,
		Bucket:         newBucket(t),
		ForcePathStyle: true,
	})
	if err != nil {
		t.Fatalf("s3.New: %v", err)
	}
	return store
}

// newBucket creates an empty throwaway bucket and returns its name, removing
// it when the test finishes. It skips when no MinIO is reachable.
func newBucket(t *testing.T) string {
	t.Helper()
	if harnessErr != nil {
		t.Skipf("MinIO unavailable: %v", harnessErr)
	}

	bucket := "bdr-s3-test-" + randomHex(8)
	client := harness.rawClient(t)

	if _, err := client.CreateBucket(context.Background(), &awss3.CreateBucketInput{
		Bucket: aws.String(bucket),
	}); err != nil {
		t.Fatalf("create bucket %s: %v", bucket, err)
	}
	t.Cleanup(func() { destroyBucket(client, bucket) })
	return bucket
}

// rawClient returns an SDK client for the harness server, used by the tests to
// arrange and inspect bucket state independently of the driver under test.
func (h *minioHarness) rawClient(t *testing.T) *awss3.Client {
	t.Helper()
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(region),
		awsconfig.WithBaseEndpoint(h.endpoint),
	)
	if err != nil {
		t.Fatalf("load aws config: %v", err)
	}
	return awss3.NewFromConfig(cfg, func(o *awss3.Options) { o.UsePathStyle = true })
}

func startMinIO() (*minioHarness, error) {
	accessKey, secretKey := minioAccessKey, minioSecretKey
	if v := os.Getenv(envAccessKey); v != "" {
		accessKey = v
	}
	if v := os.Getenv(envSecretKey); v != "" {
		secretKey = v
	}

	// Feed the AWS SDK default credential chain. This is the same path a
	// deployment takes, so the tests exercise it rather than bypassing it.
	os.Setenv("AWS_ACCESS_KEY_ID", accessKey)
	os.Setenv("AWS_SECRET_ACCESS_KEY", secretKey)
	os.Unsetenv("AWS_SESSION_TOKEN")

	if ep := os.Getenv(envEndpoint); ep != "" {
		h := &minioHarness{endpoint: ep, stop: func() {}}
		if err := h.waitReady(readyTimeout); err != nil {
			return nil, fmt.Errorf("MinIO at %s: %w", ep, err)
		}
		return h, nil
	}
	if path, err := exec.LookPath("minio"); err == nil {
		return startMinIOBinary(path)
	}
	return startMinIODocker()
}
