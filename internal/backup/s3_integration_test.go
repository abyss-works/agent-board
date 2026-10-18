//go:build s3

package backup

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
)

// TestS3RoundTrip runs against a real S3-compatible bucket named by
// AGB_TEST_S3_ENDPOINT, AGB_TEST_S3_BUCKET, AGB_TEST_S3_ACCESS_KEY_ID and
// AGB_TEST_S3_SECRET_ACCESS_KEY. It writes and removes objects under a
// unique prefix.
func TestS3RoundTrip(t *testing.T) {
	env := func(k string) string {
		v := os.Getenv(k)
		if v == "" {
			t.Skipf("%s is not set", k)
		}
		return v
	}
	s, err := NewS3(env("AGB_TEST_S3_ENDPOINT"), env("AGB_TEST_S3_BUCKET"), env("AGB_TEST_S3_ACCESS_KEY_ID"), env("AGB_TEST_S3_SECRET_ACCESS_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// A throwaway local server starts without buckets. A real bucket already exists.
	if ok, err := s.client.BucketExists(ctx, s.bucket); err != nil {
		t.Fatalf("BucketExists: %v", err)
	} else if !ok {
		if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{Region: s.region}); err != nil {
			t.Fatalf("MakeBucket: %v", err)
		}
	}
	prefix := "agb-test-" + time.Now().UTC().Format("20060102T150405.000000000Z")

	names := []string{prefix + "/b.db.gz", prefix + "/a.db.gz"}
	for _, n := range names {
		body := []byte("snapshot " + n)
		if err := s.Put(ctx, n, bytes.NewReader(body), int64(len(body))); err != nil {
			t.Fatalf("Put %s: %v", n, err)
		}
	}
	objs, err := s.List(ctx, prefix+"/")
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 2 || objs[0].Name != names[1] || objs[1].Name != names[0] {
		t.Fatalf("List = %+v, want the two objects sorted by name", objs)
	}
	if !strings.HasPrefix(objs[0].Name, prefix) || objs[0].Size == 0 {
		t.Fatalf("unexpected object %+v", objs[0])
	}
	for _, n := range names {
		if err := s.Delete(ctx, n); err != nil {
			t.Fatalf("Delete %s: %v", n, err)
		}
	}
	if err := s.Delete(ctx, prefix+"/missing.db.gz"); err != nil {
		t.Fatalf("Delete of a missing object: %v", err)
	}
	objs, err = s.List(ctx, prefix+"/")
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 0 {
		t.Fatalf("List after delete = %+v, want none", objs)
	}
}
