package main

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestMeterStoreRecord(t *testing.T) {
	store := NewMeterStore()
	now := time.Now()

	store.Record(MeterRecord{
		Agent:         "openclaw",
		CampaignID:    "oc-dep-audit",
		ToolName:      "juggler_setec_list",
		RequestBytes:  100,
		ResponseBytes: 500,
		DurationMs:    42,
		Timestamp:     now,
	})

	buckets := store.Query("openclaw", "oc-dep-audit")
	if len(buckets) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(buckets))
	}
	b := buckets[0]
	if b.ToolCalls != 1 {
		t.Errorf("ToolCalls = %d, want 1", b.ToolCalls)
	}
	if b.RequestBytes != 100 {
		t.Errorf("RequestBytes = %d, want 100", b.RequestBytes)
	}
	if b.ResponseBytes != 500 {
		t.Errorf("ResponseBytes = %d, want 500", b.ResponseBytes)
	}
	if b.Agent != "openclaw" {
		t.Errorf("Agent = %q, want openclaw", b.Agent)
	}
}

func TestMeterStoreMultipleRecords(t *testing.T) {
	store := NewMeterStore()
	now := time.Now()

	for i := 0; i < 5; i++ {
		store.Record(MeterRecord{
			Agent:         "openclaw",
			CampaignID:    "oc-dep-audit",
			ToolName:      "juggler_setec_list",
			RequestBytes:  100,
			ResponseBytes: 200,
			DurationMs:    10,
			Timestamp:     now.Add(time.Duration(i) * time.Second),
		})
	}

	buckets := store.Query("openclaw", "oc-dep-audit")
	if len(buckets) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(buckets))
	}
	if buckets[0].ToolCalls != 5 {
		t.Errorf("ToolCalls = %d, want 5", buckets[0].ToolCalls)
	}
	if buckets[0].RequestBytes != 500 {
		t.Errorf("RequestBytes = %d, want 500", buckets[0].RequestBytes)
	}
}

func TestMeterStoreQueryFiltering(t *testing.T) {
	store := NewMeterStore()
	now := time.Now()

	store.Record(MeterRecord{Agent: "openclaw", CampaignID: "oc-dep-audit", ToolName: "t1", Timestamp: now})
	store.Record(MeterRecord{Agent: "openclaw", CampaignID: "oc-smoketest", ToolName: "t2", Timestamp: now})
	store.Record(MeterRecord{Agent: "hexstrike", CampaignID: "hs-cred-exposure", ToolName: "t3", Timestamp: now})

	// Query all.
	all := store.Query("", "")
	if len(all) != 3 {
		t.Errorf("expected 3 buckets, got %d", len(all))
	}

	// Filter by agent.
	oc := store.Query("openclaw", "")
	if len(oc) != 2 {
		t.Errorf("expected 2 openclaw buckets, got %d", len(oc))
	}

	// Filter by campaign.
	dep := store.Query("", "oc-dep-audit")
	if len(dep) != 1 {
		t.Errorf("expected 1 oc-dep-audit bucket, got %d", len(dep))
	}

	// Filter by both.
	specific := store.Query("hexstrike", "hs-cred-exposure")
	if len(specific) != 1 {
		t.Errorf("expected 1 specific bucket, got %d", len(specific))
	}
}

func TestMeterStoreErrorCount(t *testing.T) {
	store := NewMeterStore()
	now := time.Now()

	store.Record(MeterRecord{Agent: "a", ToolName: "t", Timestamp: now, IsError: false})
	store.Record(MeterRecord{Agent: "a", ToolName: "t", Timestamp: now, IsError: true})
	store.Record(MeterRecord{Agent: "a", ToolName: "t", Timestamp: now, IsError: true})

	buckets := store.Query("a", "")
	if len(buckets) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(buckets))
	}
	if buckets[0].ErrorCount != 2 {
		t.Errorf("ErrorCount = %d, want 2", buckets[0].ErrorCount)
	}
	if buckets[0].ToolCalls != 3 {
		t.Errorf("ToolCalls = %d, want 3", buckets[0].ToolCalls)
	}
}

func TestMeterStoreConcurrent(t *testing.T) {
	store := NewMeterStore()
	now := time.Now()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store.Record(MeterRecord{
				Agent:        "agent",
				ToolName:     "tool",
				Timestamp:    now,
				RequestBytes: 10,
			})
		}(i)
	}
	wg.Wait()

	buckets := store.Query("agent", "")
	if len(buckets) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(buckets))
	}
	if buckets[0].ToolCalls != 100 {
		t.Errorf("ToolCalls = %d, want 100", buckets[0].ToolCalls)
	}
	if buckets[0].RequestBytes != 1000 {
		t.Errorf("RequestBytes = %d, want 1000", buckets[0].RequestBytes)
	}
}

func TestMeterStoreFlush(t *testing.T) {
	store := NewMeterStore()
	now := time.Now()

	var flushed []*MeterBucket
	store.SetFlushCallback(func(ctx context.Context, buckets []*MeterBucket) error {
		flushed = buckets
		return nil
	})

	store.Record(MeterRecord{Agent: "a", CampaignID: "c1", ToolName: "t", Timestamp: now})
	store.Record(MeterRecord{Agent: "b", CampaignID: "c2", ToolName: "t", Timestamp: now})

	n, err := store.Flush(context.Background())
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if n != 2 {
		t.Errorf("flushed %d, want 2", n)
	}
	if len(flushed) != 2 {
		t.Errorf("callback received %d buckets, want 2", len(flushed))
	}

	// After flush, store should be empty.
	remaining := store.Query("", "")
	if len(remaining) != 0 {
		t.Errorf("expected 0 buckets after flush, got %d", len(remaining))
	}
}

func TestMeterStoreFlushEmpty(t *testing.T) {
	store := NewMeterStore()
	n, err := store.Flush(context.Background())
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if n != 0 {
		t.Errorf("flushed %d, want 0", n)
	}
}

func TestMeterStoreFlushErrorRestoresBuckets(t *testing.T) {
	store := NewMeterStore()
	now := time.Now()

	store.SetFlushCallback(func(ctx context.Context, buckets []*MeterBucket) error {
		return context.DeadlineExceeded
	})

	store.Record(MeterRecord{Agent: "a", ToolName: "t", Timestamp: now, RequestBytes: 100})

	_, err := store.Flush(context.Background())
	if err == nil {
		t.Fatal("expected error from flush")
	}

	// Data should be restored.
	buckets := store.Query("a", "")
	if len(buckets) != 1 {
		t.Fatalf("expected 1 bucket after failed flush, got %d", len(buckets))
	}
	if buckets[0].RequestBytes != 100 {
		t.Errorf("RequestBytes = %d, want 100 (data should be restored)", buckets[0].RequestBytes)
	}
}

func TestMeterStoreSnapshot(t *testing.T) {
	store := NewMeterStore()
	now := time.Now()

	store.Record(MeterRecord{Agent: "a", CampaignID: "c1", ToolName: "t", Timestamp: now})
	store.Record(MeterRecord{Agent: "b", CampaignID: "c2", ToolName: "t", Timestamp: now})

	snap := store.Snapshot()
	if len(snap) != 2 {
		t.Errorf("expected 2 buckets in snapshot, got %d", len(snap))
	}

	// Modifying snapshot shouldn't affect store.
	snap[0].ToolCalls = 999
	buckets := store.Query(snap[0].Agent, snap[0].CampaignID)
	if buckets[0].ToolCalls == 999 {
		t.Error("snapshot modification leaked into store")
	}
}

func TestExtractMeteringContext(t *testing.T) {
	args := json.RawMessage(`{"_campaign_id": "oc-dep-audit", "_agent": "openclaw", "query": "test"}`)
	agent, campaignID := extractMeteringContext(args)
	if agent != "openclaw" {
		t.Errorf("agent = %q, want openclaw", agent)
	}
	if campaignID != "oc-dep-audit" {
		t.Errorf("campaignID = %q, want oc-dep-audit", campaignID)
	}
}

func TestExtractMeteringContextMissing(t *testing.T) {
	args := json.RawMessage(`{"query": "test"}`)
	agent, campaignID := extractMeteringContext(args)
	if agent != "" {
		t.Errorf("agent = %q, want empty", agent)
	}
	if campaignID != "" {
		t.Errorf("campaignID = %q, want empty", campaignID)
	}
}

func TestExtractMeteringContextInvalid(t *testing.T) {
	args := json.RawMessage(`{invalid`)
	agent, campaignID := extractMeteringContext(args)
	if agent != "" {
		t.Errorf("agent = %q, want empty", agent)
	}
	if campaignID != "" {
		t.Errorf("campaignID = %q, want empty", campaignID)
	}
}

func TestMeterStoreTokenAggregation(t *testing.T) {
	store := NewMeterStore()
	now := time.Now()

	// Simulate webhook/S3 ingested LLM calls with token counts.
	store.Record(MeterRecord{
		Agent:        "openclaw",
		CampaignID:   "oc-smoketest",
		ToolName:     "llm:claude-sonnet-4-20250514",
		InputTokens:  1200,
		OutputTokens: 350,
		Timestamp:    now,
	})
	store.Record(MeterRecord{
		Agent:        "openclaw",
		CampaignID:   "oc-smoketest",
		ToolName:     "llm:claude-sonnet-4-20250514",
		InputTokens:  800,
		OutputTokens: 200,
		Timestamp:    now.Add(time.Second),
	})

	buckets := store.Query("openclaw", "oc-smoketest")
	if len(buckets) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(buckets))
	}
	b := buckets[0]
	if b.InputTokens != 2000 {
		t.Errorf("InputTokens = %d, want 2000", b.InputTokens)
	}
	if b.OutputTokens != 550 {
		t.Errorf("OutputTokens = %d, want 550", b.OutputTokens)
	}
	if b.ToolCalls != 2 {
		t.Errorf("ToolCalls = %d, want 2", b.ToolCalls)
	}
}

func TestBucketKeyDefault(t *testing.T) {
	key := bucketKey("", "campaign")
	if key != "unknown:campaign" {
		t.Errorf("bucketKey = %q, want 'unknown:campaign'", key)
	}
}

func TestMeterStoreDeduplication(t *testing.T) {
	store := NewMeterStore()
	now := time.Now()

	// Record same event twice with the same dedupe key.
	for i := 0; i < 2; i++ {
		store.Record(MeterRecord{
			Agent:        "agent",
			ToolName:     "llm:claude-haiku-4-5-20251001",
			InputTokens:  100,
			OutputTokens: 50,
			Timestamp:    now,
			DedupeKey:    "capture-abc-123",
		})
	}

	buckets := store.Query("agent", "")
	if len(buckets) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(buckets))
	}
	// Should count only once due to dedup.
	if buckets[0].ToolCalls != 1 {
		t.Errorf("ToolCalls = %d, want 1 (dedup should prevent double-count)", buckets[0].ToolCalls)
	}
	if buckets[0].InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100", buckets[0].InputTokens)
	}
}

func TestMeterStoreDeduplicationDifferentKeys(t *testing.T) {
	store := NewMeterStore()
	now := time.Now()

	store.Record(MeterRecord{
		Agent: "agent", ToolName: "llm:haiku", InputTokens: 100,
		Timestamp: now, DedupeKey: "capture-1",
	})
	store.Record(MeterRecord{
		Agent: "agent", ToolName: "llm:haiku", InputTokens: 200,
		Timestamp: now, DedupeKey: "capture-2",
	})

	buckets := store.Query("agent", "")
	if len(buckets) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(buckets))
	}
	if buckets[0].ToolCalls != 2 {
		t.Errorf("ToolCalls = %d, want 2 (different keys should both count)", buckets[0].ToolCalls)
	}
	if buckets[0].InputTokens != 300 {
		t.Errorf("InputTokens = %d, want 300", buckets[0].InputTokens)
	}
}

func TestMeterStoreNoDedupeKeyAlwaysRecords(t *testing.T) {
	store := NewMeterStore()
	now := time.Now()

	// Records without DedupeKey should always be counted.
	for i := 0; i < 3; i++ {
		store.Record(MeterRecord{
			Agent: "agent", ToolName: "mcp:tool", InputTokens: 10,
			Timestamp: now,
		})
	}

	buckets := store.Query("agent", "")
	if len(buckets) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(buckets))
	}
	if buckets[0].ToolCalls != 3 {
		t.Errorf("ToolCalls = %d, want 3 (no dedupe key = always record)", buckets[0].ToolCalls)
	}
}
