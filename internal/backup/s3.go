package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3 is an ObjectStore backed by a bucket of an S3-compatible service such
// as Cloudflare R2.
type S3 struct {
	client *minio.Client
	bucket string
	region string
}

// NewS3 returns a store for bucket at endpoint, which is a scheme and host
// such as https://<account>.r2.cloudflarestorage.com. It does not contact
// the service.
func NewS3(endpoint, bucket, accessKeyID, secretAccessKey string) (*S3, error) {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return nil, fmt.Errorf("backup endpoint must be http(s)://host[:port], got %q", endpoint)
	}
	if bucket == "" {
		return nil, errors.New("backup bucket is empty")
	}
	if accessKeyID == "" || secretAccessKey == "" {
		return nil, errors.New("backup access key ID and secret access key are required")
	}
	// R2 signs requests for the region "auto". Other S3-compatible servers,
	// such as a local MinIO, use the default region.
	region := "us-east-1"
	if strings.HasSuffix(u.Hostname(), ".r2.cloudflarestorage.com") {
		region = "auto"
	}
	client, err := minio.New(u.Host, &minio.Options{
		Creds:        credentials.NewStaticV4(accessKeyID, secretAccessKey, ""),
		Secure:       u.Scheme == "https",
		Region:       region,
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		return nil, fmt.Errorf("backup store: %w", err)
	}
	return &S3{client: client, bucket: bucket, region: region}, nil
}

// Put uploads the reader's size bytes as name.
func (s *S3) Put(ctx context.Context, name string, r io.Reader, size int64) error {
	_, err := s.client.PutObject(ctx, s.bucket, name, r, size, minio.PutObjectOptions{ContentType: "application/gzip"})
	return err
}

// List returns the objects whose name starts with prefix, sorted by name.
func (s *S3) List(ctx context.Context, prefix string) ([]Object, error) {
	var out []Object
	for o := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if o.Err != nil {
			return nil, o.Err
		}
		out = append(out, Object{Name: o.Key, Size: o.Size})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Delete removes an object. Deleting a missing object is not an error.
func (s *S3) Delete(ctx context.Context, name string) error {
	return s.client.RemoveObject(ctx, s.bucket, name, minio.RemoveObjectOptions{})
}
