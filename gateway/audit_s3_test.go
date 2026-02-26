package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAuditS3Exporter_Configured(t *testing.T) {
	tests := []struct {
		name   string
		cfg    S3Config
		prefix string
		want   bool
	}{
		{
			name: "fully configured with endpoint",
			cfg: S3Config{
				Bucket:   "test-bucket",
				Endpoint: "https://s3.example.com",
			},
			want: true,
		},
		{
			name: "fully configured with region",
			cfg: S3Config{
				Bucket: "test-bucket",
				Region: "us-east-1",
			},
			want: true,
		},
		{
			name: "missing bucket",
			cfg: S3Config{
				Region: "us-east-1",
			},
			want: false,
		},
		{
			name: "missing region and endpoint",
			cfg: S3Config{
				Bucket: "test-bucket",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := NewAuditS3Exporter(tt.cfg, "audit/", 5*time.Minute, NewAuditLog(), nil)
			if got := e.Configured(); got != tt.want {
				t.Errorf("Configured() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAuditS3Exporter_S3URL(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		bucket   string
		region   string
		key      string
		want     string
	}{
		{
			name:     "custom endpoint with key",
			endpoint: "https://s3.civo.com",
			bucket:   "my-bucket",
			key:      "audit/2026-02-25/audit-123.ndjson",
			want:     "https://s3.civo.com/my-bucket/audit/2026-02-25/audit-123.ndjson",
		},
		{
			name:   "AWS virtual-hosted with key",
			bucket: "my-bucket",
			region: "us-east-1",
			key:    "audit/test.ndjson",
			want:   "https://my-bucket.s3.us-east-1.amazonaws.com/audit/test.ndjson",
		},
		{
			name:     "custom endpoint without key",
			endpoint: "https://s3.civo.com/",
			bucket:   "my-bucket",
			want:     "https://s3.civo.com/my-bucket",
		},
		{
			name:     "custom endpoint without scheme",
			endpoint: "objectstore.nyc1.civo.com",
			bucket:   "fuzzy-models",
			key:      "audit/test.ndjson",
			want:     "https://objectstore.nyc1.civo.com/fuzzy-models/audit/test.ndjson",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &AuditS3Exporter{
				endpoint: tt.endpoint,
				bucket:   tt.bucket,
				region:   tt.region,
			}
			if got := e.s3URL(tt.key); got != tt.want {
				t.Errorf("s3URL(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestAuditS3Exporter_Export(t *testing.T) {
	var uploaded []byte
	var uploadKey string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			uploadKey = r.URL.Path
			body, _ := io.ReadAll(r.Body)
			uploaded = body
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer server.Close()

	audit := NewAuditLog()
	audit.Log(AuditEntry{
		Action:  "test_action",
		Query:   "test_query",
		Allowed: true,
		Caller: CallerIdentity{
			Node: "test-node",
			User: "test@example.com",
		},
	})
	audit.Log(AuditEntry{
		Action:  "another_action",
		Query:   "another_query",
		Allowed: false,
		Reason:  "denied",
	})

	cfg := S3Config{
		Bucket:   "test-bucket",
		Endpoint: server.URL,
	}
	e := NewAuditS3Exporter(cfg, "audit/", 5*time.Minute, audit, nil)

	e.export(context.Background())

	if uploaded == nil {
		t.Fatal("expected upload to S3, got none")
	}

	// Verify NDJSON format (one JSON object per line).
	lines := strings.Split(strings.TrimSpace(string(uploaded)), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 NDJSON lines, got %d", len(lines))
	}

	// Verify the key contains the audit prefix and date.
	if !strings.Contains(uploadKey, "/audit/") {
		t.Errorf("expected key to contain /audit/, got %q", uploadKey)
	}
	if !strings.HasSuffix(uploadKey, ".ndjson") {
		t.Errorf("expected key to end with .ndjson, got %q", uploadKey)
	}
}

func TestAuditS3Exporter_NoEntriesNoUpload(t *testing.T) {
	uploaded := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uploaded = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	audit := NewAuditLog()
	cfg := S3Config{
		Bucket:   "test-bucket",
		Endpoint: server.URL,
	}
	e := NewAuditS3Exporter(cfg, "audit/", 5*time.Minute, audit, nil)

	e.export(context.Background())

	if uploaded {
		t.Error("should not upload when there are no entries")
	}
}

func TestAuditS3Exporter_IncrementalDrain(t *testing.T) {
	uploadCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uploadCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	audit := NewAuditLog()
	cfg := S3Config{
		Bucket:   "test-bucket",
		Endpoint: server.URL,
	}
	e := NewAuditS3Exporter(cfg, "audit/", 5*time.Minute, audit, nil)

	// First export: 2 entries.
	audit.Log(AuditEntry{Action: "a1"})
	audit.Log(AuditEntry{Action: "a2"})
	e.export(context.Background())

	if uploadCount != 1 {
		t.Fatalf("expected 1 upload after first export, got %d", uploadCount)
	}

	// Second export: no new entries.
	e.export(context.Background())
	if uploadCount != 1 {
		t.Errorf("expected no upload when no new entries, got %d uploads", uploadCount)
	}

	// Third export: 1 new entry.
	audit.Log(AuditEntry{Action: "a3"})
	e.export(context.Background())
	if uploadCount != 2 {
		t.Errorf("expected 2 uploads after new entry, got %d", uploadCount)
	}
}
