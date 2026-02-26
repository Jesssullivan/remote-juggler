package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"
)

// MeterRecord represents a single metered event.
type MeterRecord struct {
	Agent         string    `json:"agent"`
	CampaignID    string    `json:"campaign_id,omitempty"`
	ToolName      string    `json:"tool_name"`
	RequestBytes  int       `json:"request_bytes"`
	ResponseBytes int       `json:"response_bytes"`
	DurationMs    int64     `json:"duration_ms"`
	Timestamp     time.Time `json:"timestamp"`
	IsError       bool      `json:"is_error,omitempty"`
	InputTokens   int       `json:"input_tokens,omitempty"`
	OutputTokens  int       `json:"output_tokens,omitempty"`
	DedupeKey     string    `json:"dedupe_key,omitempty"` // Optional key for deduplication (e.g. Aperture capture_id).
}

// MeterBucket aggregates metering data per (agent, campaign_id) pair.
type MeterBucket struct {
	Agent           string    `json:"agent"`
	CampaignID      string    `json:"campaign_id,omitempty"`
	ToolCalls       int       `json:"mcp_tool_calls"`
	RequestBytes    int64     `json:"mcp_request_bytes"`
	ResponseBytes   int64     `json:"mcp_response_bytes"`
	ErrorCount      int       `json:"error_count"`
	TotalDurationMs int64     `json:"total_duration_ms"`
	InputTokens     int64     `json:"input_tokens"`
	OutputTokens    int64     `json:"output_tokens"`
	FirstSeen       time.Time `json:"first_seen"`
	LastSeen        time.Time `json:"last_seen"`
}

// MeterStore provides thread-safe MCP-layer metering with per-(agent, campaign) counters.
type MeterStore struct {
	mu      sync.RWMutex
	buckets map[string]*MeterBucket // key: "agent:campaign_id"

	// Deduplication: track recently-seen dedupe keys to avoid double-counting
	// when multiple sources (webhook + SSE) report the same event.
	seen     map[string]time.Time // dedupe_key -> first seen time
	seenSize int                  // max entries before eviction

	// flushCallback is called during Flush() to persist aggregated data.
	flushCallback func(ctx context.Context, buckets []*MeterBucket) error
}

// NewMeterStore creates a new MeterStore.
func NewMeterStore() *MeterStore {
	return &MeterStore{
		buckets:  make(map[string]*MeterBucket),
		seen:     make(map[string]time.Time),
		seenSize: 1000,
	}
}

// SetFlushCallback sets the function called during Flush() to persist data.
func (m *MeterStore) SetFlushCallback(cb func(ctx context.Context, buckets []*MeterBucket) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flushCallback = cb
}

// bucketKey generates the map key for a given agent and campaign.
func bucketKey(agent, campaignID string) string {
	if agent == "" {
		agent = "unknown"
	}
	return agent + ":" + campaignID
}

// Record adds a metering record to the store.
func (m *MeterStore) Record(rec MeterRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Deduplicate: if this record has a dedupe key we've already seen, skip it.
	if rec.DedupeKey != "" {
		if _, dup := m.seen[rec.DedupeKey]; dup {
			return
		}
		m.seen[rec.DedupeKey] = time.Now()
		// Evict oldest entries when the seen set gets too large.
		if len(m.seen) > m.seenSize {
			m.evictSeen()
		}
	}

	key := bucketKey(rec.Agent, rec.CampaignID)
	bucket, ok := m.buckets[key]
	if !ok {
		bucket = &MeterBucket{
			Agent:      rec.Agent,
			CampaignID: rec.CampaignID,
			FirstSeen:  rec.Timestamp,
		}
		m.buckets[key] = bucket
	}

	bucket.ToolCalls++
	bucket.RequestBytes += int64(rec.RequestBytes)
	bucket.ResponseBytes += int64(rec.ResponseBytes)
	bucket.TotalDurationMs += rec.DurationMs
	bucket.InputTokens += int64(rec.InputTokens)
	bucket.OutputTokens += int64(rec.OutputTokens)
	bucket.LastSeen = rec.Timestamp
	if rec.IsError {
		bucket.ErrorCount++
	}
}

// evictSeen removes the oldest half of the seen entries. Must be called with mu held.
func (m *MeterStore) evictSeen() {
	// Find the median timestamp and remove everything older.
	cutoff := time.Now().Add(-10 * time.Minute)
	for k, t := range m.seen {
		if t.Before(cutoff) {
			delete(m.seen, k)
		}
	}
	// If still too large (all entries are recent), remove oldest half.
	if len(m.seen) > m.seenSize {
		count := 0
		target := len(m.seen) / 2
		for k := range m.seen {
			delete(m.seen, k)
			count++
			if count >= target {
				break
			}
		}
	}
}

// Query returns aggregated metering data, optionally filtered by agent and/or campaign.
func (m *MeterStore) Query(agent, campaignID string) []*MeterBucket {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []*MeterBucket
	for _, bucket := range m.buckets {
		if agent != "" && bucket.Agent != agent {
			continue
		}
		if campaignID != "" && bucket.CampaignID != campaignID {
			continue
		}
		// Return a copy to avoid races.
		copy := *bucket
		results = append(results, &copy)
	}
	return results
}

// Snapshot returns a copy of all current buckets.
func (m *MeterStore) Snapshot() []*MeterBucket {
	return m.Query("", "")
}

// Flush persists the current buckets via the flush callback, then resets counters.
// Returns the number of buckets flushed.
func (m *MeterStore) Flush(ctx context.Context) (int, error) {
	m.mu.Lock()
	// Snapshot and reset under lock.
	old := m.buckets
	m.buckets = make(map[string]*MeterBucket)
	cb := m.flushCallback
	m.mu.Unlock()

	if len(old) == 0 {
		return 0, nil
	}

	buckets := make([]*MeterBucket, 0, len(old))
	for _, b := range old {
		buckets = append(buckets, b)
	}

	if cb != nil {
		if err := cb(ctx, buckets); err != nil {
			// Put the buckets back on failure so data isn't lost.
			m.mu.Lock()
			for _, b := range buckets {
				key := bucketKey(b.Agent, b.CampaignID)
				if existing, ok := m.buckets[key]; ok {
					// Merge with any new data that arrived during flush.
					existing.ToolCalls += b.ToolCalls
					existing.RequestBytes += b.RequestBytes
					existing.ResponseBytes += b.ResponseBytes
					existing.ErrorCount += b.ErrorCount
					existing.TotalDurationMs += b.TotalDurationMs
					existing.InputTokens += b.InputTokens
					existing.OutputTokens += b.OutputTokens
					if b.FirstSeen.Before(existing.FirstSeen) {
						existing.FirstSeen = b.FirstSeen
					}
				} else {
					m.buckets[key] = b
				}
			}
			m.mu.Unlock()
			return 0, fmt.Errorf("flush callback: %w", err)
		}
	}

	log.Printf("metering: flushed %d buckets", len(buckets))
	return len(buckets), nil
}

// StartFlushLoop starts a background goroutine that flushes the store periodically.
// Returns a cancel function to stop the loop.
func (m *MeterStore) StartFlushLoop(ctx context.Context, interval time.Duration) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				// Final flush on shutdown.
				if n, err := m.Flush(context.Background()); err != nil {
					log.Printf("metering: final flush error: %v", err)
				} else if n > 0 {
					log.Printf("metering: final flush: %d buckets", n)
				}
				return
			case <-ticker.C:
				if _, err := m.Flush(ctx); err != nil {
					log.Printf("metering: flush error: %v", err)
				}
			}
		}
	}()
	return cancel
}

// flushToSetec creates a flush callback that stores aggregated metering data to Setec.
func flushToSetec(setec *SetecClient) func(ctx context.Context, buckets []*MeterBucket) error {
	return func(ctx context.Context, buckets []*MeterBucket) error {
		if !setec.Configured() {
			return nil
		}

		data, err := json.Marshal(map[string]any{
			"buckets":    buckets,
			"flushed_at": time.Now().UTC().Format(time.RFC3339),
		})
		if err != nil {
			return fmt.Errorf("marshal metering data: %w", err)
		}

		return setec.Put(ctx, "metering/latest", string(data))
	}
}

// extractMeteringContext extracts agent and campaign_id from MCP tool call arguments.
func extractMeteringContext(args json.RawMessage) (agent, campaignID string) {
	var ctx struct {
		CampaignID string `json:"_campaign_id"`
		Agent      string `json:"_agent"`
	}
	if err := json.Unmarshal(args, &ctx); err == nil {
		agent = ctx.Agent
		campaignID = ctx.CampaignID
	}
	return
}
