// Package s3 implements storage.Storage on any S3-compatible object store
// (AWS S3, MinIO, Cloudflare R2, Ceph RGW) using aws-sdk-go-v2. Object keys
// are the digest strings the caller supplies.
package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"

	"github.com/user/better-docker-registry/internal/storage"
)

// Compile-time proof that Store honours the contract the registry expects.
var _ storage.Storage = (*Store)(nil)

// Config configures an S3-compatible blob store.
type Config struct {
	// Endpoint is the service base URL ("http://localhost:9000"). Empty uses
	// the AWS S3 endpoint for Region.
	Endpoint string

	// Region is the signing region. Self-hosted stores accept any non-empty
	// value, but the SDK always requires one.
	Region string

	// Bucket is the bucket blobs live in. It must already exist; the driver
	// never creates or deletes buckets.
	Bucket string

	// ForcePathStyle uses path-style addressing (https://endpoint/bucket/key)
	// instead of virtual-host style. MinIO and most self-hosted stores need it.
	ForcePathStyle bool

	// AccessKey and SecretKey optionally supply static credentials. When both
	// are empty the AWS SDK default credential chain applies (environment,
	// shared config, IMDS). Both must be set together.
	AccessKey string
	SecretKey string
}

// Store is a storage.Storage backed by an S3-compatible bucket. It is safe for
// concurrent use: the AWS client it wraps is.
type Store struct {
	client *awss3.Client
	bucket string
}

// New builds a Store for cfg. It resolves credentials but does no I/O.
func New(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("s3: bucket is required")
	}
	if cfg.Region == "" {
		return nil, errors.New("s3: region is required")
	}
	if (cfg.AccessKey == "") != (cfg.SecretKey == "") {
		return nil, errors.New("s3: access key and secret key must be set together")
	}

	// WithBaseEndpoint ignores empty values, so pointing at AWS S3 needs no
	// special case.
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithBaseEndpoint(cfg.Endpoint),
	}
	if cfg.AccessKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
		))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("s3: load aws config: %w", err)
	}

	client := awss3.NewFromConfig(awsCfg, func(o *awss3.Options) {
		o.UsePathStyle = cfg.ForcePathStyle
	})
	return &Store{client: client, bucket: cfg.Bucket}, nil
}

// Get streams the blob out of the bucket; the caller closes the reader.
func (s *Store) Get(ctx context.Context, digest string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(digest),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, errNotFound("get", digest)
		}
		return nil, errBackend("get", digest, err)
	}
	return out.Body, nil
}

// Put stores the content of r under digest and returns the bytes consumed.
//
// The stream is spooled to a temporary file rather than held in memory, for
// two reasons. SigV4 over plain HTTP signs the whole payload, so the SDK
// refuses a non-seekable PutObject body — and MinIO is routinely served over
// plain HTTP. And spooling lets the digest be checked before anything is
// written to the bucket, the strongest reading of "a failed Put must not leave
// a readable blob behind".
func (s *Store) Put(ctx context.Context, digest string, r io.Reader) (int64, error) {
	tmp, err := os.CreateTemp("", "bdr-put-*")
	if err != nil {
		return 0, fmt.Errorf("s3: put %s: temp file: %w", digest, err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	h, err := storage.NewDigestHasher(digest)
	if err != nil {
		return 0, fmt.Errorf("s3: put %s: %w", digest, storage.ErrBlobInvalid)
	}
	n, err := io.Copy(io.MultiWriter(tmp, h), r)
	if err != nil {
		return n, fmt.Errorf("s3: put %s: read body: %w", digest, err)
	}

	if got := h.Got(); got != digest {
		return n, fmt.Errorf("s3: put %s: %w (content hashes to %s)", digest, storage.ErrBlobInvalid, got)
	}

	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return n, fmt.Errorf("s3: put %s: rewind temp file: %w", digest, err)
	}
	if _, err := s.client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(digest),
		Body:          tmp,
		ContentLength: aws.Int64(n),
	}); err != nil {
		return n, errBackend("put", digest, err)
	}
	return n, nil
}

// Exists reports whether the bucket holds digest.
func (s *Store) Exists(ctx context.Context, digest string) (bool, error) {
	_, err := s.client.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(digest),
	})
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, errBackend("stat", digest, err)
	}
	return true, nil
}

// Delete removes digest. S3's DeleteObject is idempotent while this contract
// is not, so presence is checked first to tell "gone" from "never existed".
func (s *Store) Delete(ctx context.Context, digest string) error {
	ok, err := s.Exists(ctx, digest)
	if err != nil {
		return err
	}
	if !ok {
		return errNotFound("delete", digest)
	}
	if _, err := s.client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(digest),
	}); err != nil {
		return errBackend("delete", digest, err)
	}
	return nil
}

// Size returns the stored byte length of digest.
func (s *Store) Size(ctx context.Context, digest string) (int64, error) {
	out, err := s.client.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(digest),
	})
	if err != nil {
		if isNotFound(err) {
			return 0, errNotFound("stat", digest)
		}
		return 0, errBackend("stat", digest, err)
	}
	return aws.ToInt64(out.ContentLength), nil
}

// List walks every key in the bucket, paging through ListObjectsV2 with the
// ContinuationToken. It holds no lock while fn runs, so fn may call back into
// the same Store.
func (s *Store) List(ctx context.Context, fn func(digest string) error) error {
	var token *string
	for {
		out, err := s.client.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{
			Bucket:            aws.String(s.bucket),
			ContinuationToken: token,
		})
		if err != nil {
			return errBackend("list", s.bucket, err)
		}
		for i := range out.Contents {
			if err := fn(aws.ToString(out.Contents[i].Key)); err != nil {
				// Contract: a callback error stops iteration and is returned
				// exactly as the callback produced it.
				return err
			}
		}
		if !aws.ToBool(out.IsTruncated) {
			return nil
		}
		if out.NextContinuationToken == nil {
			// A truncated page without a token would loop forever.
			return errBackend("list", s.bucket,
				errors.New("truncated listing without a continuation token"))
		}
		token = out.NextContinuationToken
	}
}

// errNotFound wraps storage.ErrBlobNotFound for an absent object key.
func errNotFound(op, key string) error {
	return fmt.Errorf("s3: %s %s: %w", op, key, storage.ErrBlobNotFound)
}

// errBackend wraps storage.ErrStorageUnavailable around an S3 API failure. The
// cause is wrapped too, so cancellation and deadline errors still match
// context.Canceled / context.DeadlineExceeded for callers that care.
func errBackend(op, key string, cause error) error {
	return fmt.Errorf("s3: %s %s: %w (%w)", op, key, cause, storage.ErrStorageUnavailable)
}

// isNotFound reports whether err means "this key does not exist". Stores
// disagree on the code — AWS sends NoSuchKey for GetObject and NotFound for
// HeadObject, MinIO sends NoSuchKey for both — so both are matched.
func isNotFound(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound":
			return true
		}
	}
	return false
}
