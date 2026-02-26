package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// AuditS3Exporter periodically exports audit log entries to S3 in NDJSON format.
// Reuses the S3 signing infrastructure from ApertureS3Ingester.
type AuditS3Exporter struct {
	bucket    string
	region    string
	prefix    string
	endpoint  string
	accessKey string
	secretKey string

	audit    *AuditLog
	client   *http.Client
	interval time.Duration

	// lastExport tracks the index of the last exported entry to avoid duplicates.
	lastExport int
}

// NewAuditS3Exporter creates an exporter that writes audit entries to S3.
func NewAuditS3Exporter(cfg S3Config, auditPrefix string, interval time.Duration, audit *AuditLog, client *http.Client) *AuditS3Exporter {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	if auditPrefix == "" {
		auditPrefix = "audit/"
	}
	if interval == 0 {
		interval = 5 * time.Minute
	}
	return &AuditS3Exporter{
		bucket:    cfg.Bucket,
		region:    cfg.Region,
		prefix:    auditPrefix,
		endpoint:  cfg.Endpoint,
		accessKey: cfg.AccessKey,
		secretKey: cfg.SecretKey,
		audit:     audit,
		client:    client,
		interval:  interval,
	}
}

// Configured returns whether the exporter has the required S3 configuration.
func (e *AuditS3Exporter) Configured() bool {
	return e.bucket != "" && (e.region != "" || e.endpoint != "")
}

// Start begins the periodic export loop. Returns a cancel function.
func (e *AuditS3Exporter) Start(ctx context.Context) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		if !e.Configured() {
			log.Printf("audit-s3: not configured, skipping")
			return
		}
		log.Printf("audit-s3: exporting to s3://%s/%s every %s", e.bucket, e.prefix, e.interval)

		ticker := time.NewTicker(e.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				// Final export on shutdown.
				e.export(context.Background())
				return
			case <-ticker.C:
				e.export(ctx)
			}
		}
	}()
	return cancel
}

// export drains new entries from the audit log and writes them to S3.
func (e *AuditS3Exporter) export(ctx context.Context) {
	entries := e.audit.Drain(e.lastExport)
	if len(entries) == 0 {
		return
	}

	// Build NDJSON payload.
	var buf bytes.Buffer
	for _, entry := range entries {
		line, err := json.Marshal(entry)
		if err != nil {
			log.Printf("audit-s3: marshal error: %v", err)
			continue
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}

	// Generate S3 key: audit/{date}/audit-{timestamp}.ndjson
	now := time.Now().UTC()
	key := fmt.Sprintf("%s%s/audit-%s.ndjson",
		e.prefix,
		now.Format("2006-01-02"),
		now.Format("20060102T150405Z"))

	if err := e.putObject(ctx, key, buf.Bytes()); err != nil {
		log.Printf("audit-s3: upload error: %v", err)
		return
	}

	e.lastExport += len(entries)
	log.Printf("audit-s3: exported %d entries to s3://%s/%s", len(entries), e.bucket, key)
}

// putObject uploads data to an S3 key using AWS SigV4 signing.
func (e *AuditS3Exporter) putObject(ctx context.Context, key string, data []byte) error {
	url := e.s3URL(key)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-ndjson")

	payloadHash := sha256Hex(data)
	e.signS3Request(req, payloadHash)

	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("S3 returned %d", resp.StatusCode)
	}

	return nil
}

// s3URL builds the S3 URL for a key (same logic as ApertureS3Ingester).
func (e *AuditS3Exporter) s3URL(key string) string {
	if e.endpoint != "" {
		ep := strings.TrimRight(e.endpoint, "/")
		if !strings.HasPrefix(ep, "https://") && !strings.HasPrefix(ep, "http://") {
			ep = "https://" + ep
		}
		if key != "" {
			return fmt.Sprintf("%s/%s/%s", ep, e.bucket, key)
		}
		return fmt.Sprintf("%s/%s", ep, e.bucket)
	}
	if key != "" {
		return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", e.bucket, e.region, key)
	}
	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/", e.bucket, e.region)
}

// signS3Request signs a request with AWS SigV4 (delegates to shared helpers).
func (e *AuditS3Exporter) signS3Request(req *http.Request, payloadHash string) {
	if e.accessKey == "" || e.secretKey == "" {
		return
	}

	now := time.Now().UTC()
	datestamp := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")

	region := e.region
	if region == "" {
		region = "us-east-1"
	}

	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)

	host := req.URL.Host
	if host == "" {
		host = req.Host
	}

	canonicalURI := req.URL.Path
	if canonicalURI == "" {
		canonicalURI = "/"
	}

	signedHeaders := "content-type;host;x-amz-content-sha256;x-amz-date"
	canonicalHeaders := fmt.Sprintf("content-type:%s\nhost:%s\nx-amz-content-sha256:%s\nx-amz-date:%s\n",
		req.Header.Get("Content-Type"), host, payloadHash, amzDate)

	canonicalRequest := fmt.Sprintf("%s\n%s\n\n%s\n%s\n%s",
		req.Method, canonicalURI, canonicalHeaders, signedHeaders, payloadHash)

	credentialScope := fmt.Sprintf("%s/%s/s3/aws4_request", datestamp, region)
	stringToSign := fmt.Sprintf("AWS4-HMAC-SHA256\n%s\n%s\n%s",
		amzDate, credentialScope, sha256Hex([]byte(canonicalRequest)))

	signingKey := hmacSHA256(hmacSHA256(hmacSHA256(hmacSHA256(
		[]byte("AWS4"+e.secretKey), []byte(datestamp)),
		[]byte(region)), []byte("s3")), []byte("aws4_request"))

	signature := fmt.Sprintf("%x", hmacSHA256(signingKey, []byte(stringToSign)))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		e.accessKey, credentialScope, signedHeaders, signature))
}
