package backup

import "testing"

func TestNewS3RejectsBadEndpoints(t *testing.T) {
	for _, endpoint := range []string{
		"",
		"example.r2.cloudflarestorage.com",
		"ftp://example.r2.cloudflarestorage.com",
		"https://",
		"https://example.r2.cloudflarestorage.com/bucket",
		"https://example.r2.cloudflarestorage.com?x=1",
	} {
		if _, err := NewS3(endpoint, "agb", "key", "secret"); err == nil {
			t.Errorf("NewS3(%q) succeeded, want an error", endpoint)
		}
	}
}

func TestNewS3AcceptsEndpoints(t *testing.T) {
	cases := []struct {
		endpoint string
		region   string
	}{
		{"https://0123abcd.r2.cloudflarestorage.com", "auto"},
		{"https://0123abcd.r2.cloudflarestorage.com/", "auto"},
		{"http://127.0.0.1:9000", "us-east-1"},
	}
	for _, c := range cases {
		s, err := NewS3(c.endpoint, "agb", "key", "secret")
		if err != nil {
			t.Fatalf("NewS3(%q): %v", c.endpoint, err)
		}
		if s.region != c.region {
			t.Errorf("NewS3(%q) region = %q, want %q", c.endpoint, s.region, c.region)
		}
	}
}

func TestNewS3RequiresBucketAndCredentials(t *testing.T) {
	if _, err := NewS3("https://a.r2.cloudflarestorage.com", "", "key", "secret"); err == nil {
		t.Error("empty bucket accepted")
	}
	if _, err := NewS3("https://a.r2.cloudflarestorage.com", "agb", "", "secret"); err == nil {
		t.Error("empty access key accepted")
	}
	if _, err := NewS3("https://a.r2.cloudflarestorage.com", "agb", "key", ""); err == nil {
		t.Error("empty secret accepted")
	}
}
