package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
)

// ApertureS3Ingester polls an S3 bucket for Aperture usage export files
// and backfills the MeterStore for batch analysis. This provides historical
// data beyond what real-time webhooks capture.
type ApertureS3Ingester struct {
	bucket    string
	region    string
	prefix    string
	endpoint  string
	accessKey string
	secretKey string
	meter     *MeterStore
	client    *http.Client
	interval  time.Duration

	// lastKey tracks the most recently processed S3 object key
	// to avoid reprocessing. Not persistent across restarts --
	// full history is in MeterStore/Setec.
	lastKey string
}

// S3Config holds S3 ingestion configuration.
type S3Config struct {
	Bucket    string `json:"aperture_s3_bucket"`
	Region    string `json:"aperture_s3_region"`
	Prefix    string `json:"aperture_s3_prefix"`
	Endpoint  string `json:"aperture_s3_endpoint"`
	AccessKey string `json:"aperture_s3_access_key"`
	SecretKey string `json:"aperture_s3_secret_key"`
}

// NewApertureS3Ingester creates an S3 ingester for Aperture usage exports.
func NewApertureS3Ingester(cfg S3Config, meter *MeterStore, client *http.Client) *ApertureS3Ingester {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &ApertureS3Ingester{
		bucket:    cfg.Bucket,
		region:    cfg.Region,
		prefix:    cfg.Prefix,
		endpoint:  cfg.Endpoint,
		accessKey: cfg.AccessKey,
		secretKey: cfg.SecretKey,
		meter:     meter,
		client:    client,
		interval:  15 * time.Minute,
	}
}

// Configured returns whether the S3 ingester has required configuration.
func (s *ApertureS3Ingester) Configured() bool {
	return s.bucket != "" && (s.region != "" || s.endpoint != "")
}

// s3URL builds the appropriate URL for an S3 operation.
// Uses path-style when a custom endpoint is configured (Civo, MinIO, etc.),
// otherwise uses AWS virtual-hosted style.
func (s *ApertureS3Ingester) s3URL(key string) string {
	if s.endpoint != "" {
		// Path-style: https://endpoint/bucket/key
		ep := strings.TrimRight(s.endpoint, "/")
		if !strings.HasPrefix(ep, "https://") && !strings.HasPrefix(ep, "http://") {
			ep = "https://" + ep
		}
		if key != "" {
			return fmt.Sprintf("%s/%s/%s", ep, s.bucket, key)
		}
		return fmt.Sprintf("%s/%s", ep, s.bucket)
	}
	// Virtual-hosted style for AWS.
	if key != "" {
		return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", s.bucket, s.region, key)
	}
	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/", s.bucket, s.region)
}

// signS3Request signs an HTTP request using AWS Signature V4.
// This is compatible with any S3-compatible store (AWS, Civo, MinIO, etc.).
func (s *ApertureS3Ingester) signS3Request(req *http.Request, payloadHash string) {
	if s.accessKey == "" || s.secretKey == "" {
		return
	}

	now := time.Now().UTC()
	datestamp := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")

	region := s.region
	if region == "" {
		region = "us-east-1"
	}

	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)

	// Host header is required for signing.
	host := req.URL.Host
	if host == "" {
		host = req.Host
	}

	// Build canonical request.
	canonicalURI := req.URL.Path
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	canonicalQueryString := req.URL.Query().Encode()

	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalHeaders := fmt.Sprintf("host:%s\nx-amz-content-sha256:%s\nx-amz-date:%s\n",
		host, payloadHash, amzDate)

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		canonicalQueryString,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	// Build string to sign.
	credentialScope := fmt.Sprintf("%s/%s/s3/aws4_request", datestamp, region)
	canonicalRequestHash := sha256Hex([]byte(canonicalRequest))
	stringToSign := fmt.Sprintf("AWS4-HMAC-SHA256\n%s\n%s\n%s",
		amzDate, credentialScope, canonicalRequestHash)

	// Calculate signing key.
	signingKey := hmacSHA256(hmacSHA256(hmacSHA256(hmacSHA256(
		[]byte("AWS4"+s.secretKey), []byte(datestamp)),
		[]byte(region)), []byte("s3")), []byte("aws4_request"))

	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	// Build authorization header.
	authHeader := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		s.accessKey, credentialScope, signedHeaders, signature)
	req.Header.Set("Authorization", authHeader)
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// Start begins the background polling loop. Returns a cancel function.
func (s *ApertureS3Ingester) Start(ctx context.Context) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		if !s.Configured() {
			log.Printf("aperture-s3: not configured, skipping")
			return
		}
		log.Printf("aperture-s3: polling s3://%s/%s every %s", s.bucket, s.prefix, s.interval)

		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		// Initial poll.
		s.poll(ctx)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.poll(ctx)
			}
		}
	}()
	return cancel
}

// ApertureS3Record represents a single record from an Aperture S3 export file.
type ApertureS3Record struct {
	Timestamp    time.Time `json:"timestamp"`
	Agent        string    `json:"agent"`
	CampaignID   string    `json:"campaign_id"`
	Model        string    `json:"model"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	DurationMs   int64     `json:"duration_ms"`
	Error        string    `json:"error"`
}

// poll fetches new export files from S3 and ingests them into the MeterStore.
func (s *ApertureS3Ingester) poll(ctx context.Context) {
	listURL := s.s3URL("")

	// Add list-type=2 query parameters.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		log.Printf("aperture-s3: list request error: %v", err)
		return
	}
	q := req.URL.Query()
	q.Set("list-type", "2")
	if s.prefix != "" {
		q.Set("prefix", s.prefix)
	}
	if s.lastKey != "" {
		q.Set("start-after", s.lastKey)
	}
	req.URL.RawQuery = s3CanonicalQueryString(q)

	emptyHash := sha256Hex([]byte{})
	s.signS3Request(req, emptyHash)

	resp, err := s.client.Do(req)
	if err != nil {
		log.Printf("aperture-s3: list error: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("aperture-s3: list returned %d: %s", resp.StatusCode, string(body))
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("aperture-s3: read list response: %v", err)
		return
	}

	keys := parseS3Keys(body)
	for _, key := range keys {
		if err := s.ingestKey(ctx, key); err != nil {
			log.Printf("aperture-s3: ingest %s: %v", key, err)
			continue
		}
		s.lastKey = key
	}

	if len(keys) > 0 {
		log.Printf("aperture-s3: ingested %d export files", len(keys))
	}
}

// s3CanonicalQueryString encodes query parameters in canonical (sorted) order
// as required by AWS SigV4.
func s3CanonicalQueryString(q map[string][]string) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		for _, v := range q[k] {
			parts = append(parts, fmt.Sprintf("%s=%s", k, v))
		}
	}
	return strings.Join(parts, "&")
}

// ingestKey downloads and processes a single S3 export file.
// Supports both NDJSON (newline-delimited JSON) and JSON arrays.
func (s *ApertureS3Ingester) ingestKey(ctx context.Context, key string) error {
	getURL := s.s3URL(key)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	emptyHash := sha256Hex([]byte{})
	s.signS3Request(req, emptyHash)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("S3 returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	records, err := parseS3Records(body)
	if err != nil {
		return err
	}

	for _, rec := range records {
		// Build dedup key matching SSE ingester format to prevent
		// double-counting when the same event appears in both streams.
		dedupeKey := fmt.Sprintf("%s:%s:%d:%d:%d",
			rec.Model, rec.Agent, rec.InputTokens, rec.OutputTokens, rec.Timestamp.Unix())
		s.meter.Record(MeterRecord{
			Agent:        rec.Agent,
			CampaignID:   rec.CampaignID,
			ToolName:     fmt.Sprintf("llm:%s", rec.Model),
			DurationMs:   rec.DurationMs,
			Timestamp:    rec.Timestamp,
			IsError:      rec.Error != "",
			InputTokens:  rec.InputTokens,
			OutputTokens: rec.OutputTokens,
			DedupeKey:    dedupeKey,
		})
	}

	return nil
}

// parseS3Records parses export file contents as either NDJSON or a JSON array.
func parseS3Records(body []byte) ([]ApertureS3Record, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, nil
	}

	// If it starts with '[', try JSON array.
	if trimmed[0] == '[' {
		var records []ApertureS3Record
		if err := json.Unmarshal(trimmed, &records); err != nil {
			return nil, fmt.Errorf("parse JSON array: %w", err)
		}
		return records, nil
	}

	// Otherwise treat as NDJSON (newline-delimited JSON).
	var records []ApertureS3Record
	scanner := bufio.NewScanner(bytes.NewReader(trimmed))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec ApertureS3Record
		if err := json.Unmarshal(line, &rec); err != nil {
			return nil, fmt.Errorf("parse NDJSON line %d: %w", lineNum, err)
		}
		records = append(records, rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan NDJSON: %w", err)
	}
	return records, nil
}

// listBucketResult represents the AWS S3 ListBucketResult XML response.
type listBucketResult struct {
	XMLName  xml.Name   `xml:"ListBucketResult"`
	Contents []s3Object `xml:"Contents"`
}

type s3Object struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
	Size         int64  `xml:"Size"`
}

// parseS3Keys extracts object keys from an S3 ListBucketResult XML response.
func parseS3Keys(body []byte) []string {
	var result listBucketResult
	if err := xml.Unmarshal(body, &result); err != nil {
		log.Printf("aperture-s3: parse list XML: %v", err)
		return nil
	}
	keys := make([]string, 0, len(result.Contents))
	for _, obj := range result.Contents {
		if obj.Key != "" {
			keys = append(keys, obj.Key)
		}
	}
	return keys
}
