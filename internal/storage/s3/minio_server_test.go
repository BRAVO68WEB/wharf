package s3_test

// Server lifecycle for the MinIO test harness: starting `minio server` from a
// local binary or a container, waiting for it to accept traffic, and tearing
// the throwaway state back down.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// startMinIOBinary runs `minio server <dir>` on a free localhost port.
func startMinIOBinary(bin string) (*minioHarness, error) {
	apiPort, err := freePort()
	if err != nil {
		return nil, err
	}
	consolePort, err := freePort()
	if err != nil {
		return nil, err
	}

	dir, err := os.MkdirTemp("", "bdr-minio-data-")
	if err != nil {
		return nil, fmt.Errorf("minio data dir: %w", err)
	}

	cmd := exec.Command(bin, "server", dir,
		"--address", fmt.Sprintf("127.0.0.1:%d", apiPort),
		"--console-address", fmt.Sprintf("127.0.0.1:%d", consolePort),
	)
	cmd.Env = os.Environ()
	var logs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &logs, &logs

	if err := cmd.Start(); err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("start %s server: %w", bin, err)
	}

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	h := &minioHarness{
		endpoint: fmt.Sprintf("http://127.0.0.1:%d", apiPort),
		stop: func() {
			_ = cmd.Process.Kill()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
			}
			os.RemoveAll(dir)
		},
	}
	if err := h.waitReady(readyTimeout); err != nil {
		h.stop()
		return nil, fmt.Errorf("%s server: %w\n%s", bin, err, logs.String())
	}
	return h, nil
}

// startMinIODocker runs the MinIO container on a free localhost port.
func startMinIODocker() (*minioHarness, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, fmt.Errorf("neither a minio binary nor docker found, and %s unset", envEndpoint)
	}

	port, err := freePort()
	if err != nil {
		return nil, err
	}

	image := os.Getenv(envImage)
	if image == "" {
		image = defaultImage
	}
	name := fmt.Sprintf("bdr-minio-%d", os.Getpid())

	out, err := exec.Command("docker", "run", "--rm", "-d",
		"--name", name,
		"-e", "MINIO_ROOT_USER="+os.Getenv("AWS_ACCESS_KEY_ID"),
		"-e", "MINIO_ROOT_PASSWORD="+os.Getenv("AWS_SECRET_ACCESS_KEY"),
		"-p", fmt.Sprintf("127.0.0.1:%d:9000", port),
		image, "server", "/data",
	).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker run %s: %w: %s", image, err, out)
	}

	h := &minioHarness{
		endpoint: fmt.Sprintf("http://127.0.0.1:%d", port),
		stop: func() {
			_ = exec.Command("docker", "rm", "-f", name).Run()
		},
	}
	if err := h.waitReady(readyTimeout); err != nil {
		h.stop()
		return nil, err
	}
	return h, nil
}

// waitReady blocks until MinIO's readiness endpoint answers 200.
func (h *minioHarness) waitReady(timeout time.Duration) error {
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		resp, err := client.Get(h.endpoint + "/minio/health/ready")
		if err != nil {
			last = err
		} else {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			last = fmt.Errorf("health check returned %s", resp.Status)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("MinIO not ready within %s: %v", timeout, last)
}

// destroyBucket empties bucket and removes it. Best effort: the server is
// throwaway, so a slow teardown must not fail the run.
func destroyBucket(client *awss3.Client, bucket string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var token *string
	for {
		out, err := client.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			ContinuationToken: token,
		})
		if err != nil {
			return
		}
		if len(out.Contents) > 0 {
			ids := make([]types.ObjectIdentifier, len(out.Contents))
			for i := range out.Contents {
				ids[i] = types.ObjectIdentifier{Key: out.Contents[i].Key}
			}
			if _, err := client.DeleteObjects(ctx, &awss3.DeleteObjectsInput{
				Bucket: aws.String(bucket),
				Delete: &types.Delete{Objects: ids, Quiet: aws.Bool(true)},
			}); err != nil {
				return
			}
		}
		if out.IsTruncated == nil || !*out.IsTruncated {
			break
		}
		token = out.NextContinuationToken
	}
	_, _ = client.DeleteBucket(ctx, &awss3.DeleteBucketInput{Bucket: aws.String(bucket)})
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("find free port: %w", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// randomHex returns n random bytes as hex. crypto/rand.Read is documented to
// always succeed since Go 1.24.
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
