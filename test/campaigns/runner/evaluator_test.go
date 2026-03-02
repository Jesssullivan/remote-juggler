package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSuccessRate(t *testing.T) {
	tests := []struct {
		name    string
		entries []HistoryEntry
		want    float64
	}{
		{"empty", nil, 0},
		{"all success", []HistoryEntry{
			{Status: "success"}, {Status: "success"},
		}, 1.0},
		{"all failure", []HistoryEntry{
			{Status: "failure"}, {Status: "error"},
		}, 0},
		{"mixed", []HistoryEntry{
			{Status: "success"}, {Status: "failure"}, {Status: "success"},
			{Status: "timeout"}, {Status: "success"},
		}, 0.6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := successRate(tt.entries)
			if got != tt.want {
				t.Errorf("successRate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAvgToolCalls(t *testing.T) {
	tests := []struct {
		name    string
		entries []HistoryEntry
		want    float64
	}{
		{"empty", nil, 0},
		{"single", []HistoryEntry{{ToolCalls: 5}}, 5.0},
		{"multiple", []HistoryEntry{
			{ToolCalls: 2}, {ToolCalls: 4}, {ToolCalls: 6},
		}, 4.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := avgToolCalls(tt.entries)
			if got != tt.want {
				t.Errorf("avgToolCalls() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAvgDuration(t *testing.T) {
	tests := []struct {
		name    string
		entries []HistoryEntry
		want    float64
	}{
		{"empty", nil, 0},
		{"single", []HistoryEntry{{DurationS: 10.5}}, 10.5},
		{"multiple", []HistoryEntry{
			{DurationS: 10.0}, {DurationS: 20.0}, {DurationS: 30.0},
		}, 20.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := avgDuration(tt.entries)
			if got != tt.want {
				t.Errorf("avgDuration() = %v, want %v", got, tt.want)
			}
		})
	}
}

// newTestCollector creates a Collector backed by a mock gateway for testing.
func newTestCollector(t *testing.T) (*Collector, *mockGateway) {
	t.Helper()
	gw := newMockGateway()
	srv := httptest.NewServer(gw.Handler())
	t.Cleanup(srv.Close)
	return NewCollector(srv.URL), gw
}

func TestStoreAndGetRunHistory(t *testing.T) {
	collector, _ := newTestCollector(t)
	ctx := context.Background()

	campaign := &Campaign{
		ID:      "test-campaign",
		Outputs: CampaignOutputs{SetecKey: "remotejuggler/campaigns/test"},
	}

	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		result := &CampaignResult{
			RunID:      "run-" + time.Now().Format("150405") + "-" + string(rune('a'+i)),
			Status:     "success",
			ToolCalls:  i + 1,
			TokensUsed: (i + 1) * 100,
			StartedAt:  now.Add(time.Duration(i) * time.Minute).Format(time.RFC3339),
			FinishedAt: now.Add(time.Duration(i)*time.Minute + 30*time.Second).Format(time.RFC3339),
		}
		if err := collector.StoreRunHistory(ctx, campaign, result); err != nil {
			t.Fatalf("store %d: %v", i, err)
		}
	}

	entries, err := collector.GetRunHistory(ctx, campaign.Outputs.SetecKey)
	if err != nil {
		t.Fatalf("get history: %v", err)
	}

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// Verify ordering (oldest first).
	if entries[0].ToolCalls != 1 {
		t.Errorf("first entry tool_calls = %d, want 1", entries[0].ToolCalls)
	}
	if entries[2].ToolCalls != 3 {
		t.Errorf("last entry tool_calls = %d, want 3", entries[2].ToolCalls)
	}

	// Verify duration was computed.
	if entries[0].DurationS != 30.0 {
		t.Errorf("duration = %v, want 30.0", entries[0].DurationS)
	}
}

func TestRunHistoryRollingWindow(t *testing.T) {
	collector, _ := newTestCollector(t)
	ctx := context.Background()

	campaign := &Campaign{
		ID:      "rolling-test",
		Outputs: CampaignOutputs{SetecKey: "remotejuggler/campaigns/rolling"},
	}

	now := time.Now().UTC()
	// Store 15 entries — only the last 10 should remain.
	for i := 0; i < 15; i++ {
		result := &CampaignResult{
			RunID:      "run-" + string(rune('A'+i)),
			Status:     "success",
			ToolCalls:  i + 1,
			StartedAt:  now.Add(time.Duration(i) * time.Minute).Format(time.RFC3339),
			FinishedAt: now.Add(time.Duration(i)*time.Minute + 10*time.Second).Format(time.RFC3339),
		}
		if err := collector.StoreRunHistory(ctx, campaign, result); err != nil {
			t.Fatalf("store %d: %v", i, err)
		}
	}

	entries, err := collector.GetRunHistory(ctx, campaign.Outputs.SetecKey)
	if err != nil {
		t.Fatalf("get history: %v", err)
	}

	if len(entries) != maxHistoryEntries {
		t.Fatalf("expected %d entries (rolling window), got %d", maxHistoryEntries, len(entries))
	}

	// First entry should be run 6 (index 5, 0-based), i.e. ToolCalls=6.
	if entries[0].ToolCalls != 6 {
		t.Errorf("first entry after trim: tool_calls = %d, want 6", entries[0].ToolCalls)
	}
}

func TestEvaluateImprovement(t *testing.T) {
	collector, _ := newTestCollector(t)
	ctx := context.Background()

	campaign := &Campaign{
		ID:      "eval-test",
		Outputs: CampaignOutputs{SetecKey: "remotejuggler/campaigns/eval"},
	}

	now := time.Now().UTC()
	splitTime := now.Add(5 * time.Minute)

	// 5 "before" entries: 60% success rate (3 success, 2 failure).
	for i := 0; i < 5; i++ {
		status := "success"
		if i >= 3 {
			status = "failure"
		}
		result := &CampaignResult{
			RunID:      "before-" + string(rune('a'+i)),
			Status:     status,
			ToolCalls:  2,
			StartedAt:  now.Add(time.Duration(i) * time.Minute).Format(time.RFC3339),
			FinishedAt: now.Add(time.Duration(i)*time.Minute + 20*time.Second).Format(time.RFC3339),
		}
		if err := collector.StoreRunHistory(ctx, campaign, result); err != nil {
			t.Fatalf("store before %d: %v", i, err)
		}
	}

	// 5 "after" entries: 100% success rate, more tool calls, shorter duration.
	for i := 0; i < 5; i++ {
		result := &CampaignResult{
			RunID:      "after-" + string(rune('a'+i)),
			Status:     "success",
			ToolCalls:  4,
			StartedAt:  splitTime.Add(time.Duration(i) * time.Minute).Format(time.RFC3339),
			FinishedAt: splitTime.Add(time.Duration(i)*time.Minute + 10*time.Second).Format(time.RFC3339),
		}
		if err := collector.StoreRunHistory(ctx, campaign, result); err != nil {
			t.Fatalf("store after %d: %v", i, err)
		}
	}

	eval, err := collector.EvaluateImprovement(ctx, campaign.Outputs.SetecKey, splitTime.Format(time.RFC3339))
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}

	if eval.BeforeRuns != 5 {
		t.Errorf("before_runs = %d, want 5", eval.BeforeRuns)
	}
	if eval.AfterRuns != 5 {
		t.Errorf("after_runs = %d, want 5", eval.AfterRuns)
	}

	// Success rate: 1.0 - 0.6 = 0.4.
	if eval.SuccessRateDelta != 0.4 {
		t.Errorf("success_rate_delta = %v, want 0.4", eval.SuccessRateDelta)
	}

	// Tool calls: 4.0 - 2.0 = 2.0.
	if eval.AvgToolCallsDelta != 2.0 {
		t.Errorf("avg_tool_calls_delta = %v, want 2.0", eval.AvgToolCallsDelta)
	}

	// Duration: 10.0 - 20.0 = -10.0 (improvement = shorter).
	if eval.AvgDurationDelta != -10.0 {
		t.Errorf("avg_duration_delta = %v, want -10.0", eval.AvgDurationDelta)
	}

	if !eval.Improved {
		t.Error("expected improved=true (success rate increased)")
	}
}

func TestEvaluateImprovementNoAfter(t *testing.T) {
	collector, _ := newTestCollector(t)
	ctx := context.Background()

	campaign := &Campaign{
		ID:      "eval-no-after",
		Outputs: CampaignOutputs{SetecKey: "remotejuggler/campaigns/noafter"},
	}

	now := time.Now().UTC()
	// All entries before the split point.
	for i := 0; i < 3; i++ {
		result := &CampaignResult{
			RunID:      "only-before-" + string(rune('a'+i)),
			Status:     "success",
			ToolCalls:  1,
			StartedAt:  now.Add(time.Duration(i) * time.Minute).Format(time.RFC3339),
			FinishedAt: now.Add(time.Duration(i)*time.Minute + 5*time.Second).Format(time.RFC3339),
		}
		if err := collector.StoreRunHistory(ctx, campaign, result); err != nil {
			t.Fatalf("store %d: %v", i, err)
		}
	}

	futureTime := now.Add(1 * time.Hour).Format(time.RFC3339)
	_, err := collector.EvaluateImprovement(ctx, campaign.Outputs.SetecKey, futureTime)
	if err == nil {
		t.Fatal("expected error for no entries after split, got nil")
	}
}

func TestEvaluateImprovementNoBefore(t *testing.T) {
	collector, _ := newTestCollector(t)
	ctx := context.Background()

	campaign := &Campaign{
		ID:      "eval-no-before",
		Outputs: CampaignOutputs{SetecKey: "remotejuggler/campaigns/nobefore"},
	}

	now := time.Now().UTC()
	// All entries after the split point.
	for i := 0; i < 3; i++ {
		result := &CampaignResult{
			RunID:      "only-after-" + string(rune('a'+i)),
			Status:     "success",
			ToolCalls:  1,
			StartedAt:  now.Add(time.Duration(i) * time.Minute).Format(time.RFC3339),
			FinishedAt: now.Add(time.Duration(i)*time.Minute + 5*time.Second).Format(time.RFC3339),
		}
		if err := collector.StoreRunHistory(ctx, campaign, result); err != nil {
			t.Fatalf("store %d: %v", i, err)
		}
	}

	pastTime := now.Add(-1 * time.Hour).Format(time.RFC3339)
	_, err := collector.EvaluateImprovement(ctx, campaign.Outputs.SetecKey, pastTime)
	if err == nil {
		t.Fatal("expected error for no entries before split, got nil")
	}
}

func TestHandleEvaluateMissingParams(t *testing.T) {
	api := newTestAPI()

	req := httptest.NewRequest(http.MethodGet, "/evaluate", nil)
	w := httptest.NewRecorder()
	api.handleEvaluate(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleEvaluateNotFound(t *testing.T) {
	api := newTestAPI()

	req := httptest.NewRequest(http.MethodGet, "/evaluate?campaign=nonexistent&since=2026-01-01T00:00:00Z", nil)
	w := httptest.NewRecorder()
	api.handleEvaluate(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandleEvaluateNoCollector(t *testing.T) {
	// newTestAPI creates with nil collector (scheduler has nil collector).
	api := newTestAPI()

	req := httptest.NewRequest(http.MethodGet, "/evaluate?campaign=test-campaign&since=2026-01-01T00:00:00Z", nil)
	w := httptest.NewRecorder()
	api.handleEvaluate(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestHandleEvaluateEndToEnd(t *testing.T) {
	// Set up a real collector with mock gateway.
	gw := newMockGateway()
	srv := httptest.NewServer(gw.Handler())
	t.Cleanup(srv.Close)

	collector := NewCollector(srv.URL)

	campaign := &Campaign{
		ID:    "eval-e2e",
		Name:  "Eval E2E",
		Agent: "tinyclaw",
		Outputs: CampaignOutputs{
			SetecKey: "remotejuggler/campaigns/eval-e2e",
		},
		Guardrails: Guardrails{MaxDuration: "5m"},
	}

	registry := map[string]*Campaign{"eval-e2e": campaign}
	scheduler := NewScheduler(registry, nil, collector)
	api := NewAPIServer(scheduler, registry)

	ctx := context.Background()
	now := time.Now().UTC()
	splitTime := now.Add(5 * time.Minute)

	// Store before entries.
	for i := 0; i < 3; i++ {
		result := &CampaignResult{
			RunID:      "b-" + string(rune('a'+i)),
			Status:     "failure",
			ToolCalls:  1,
			StartedAt:  now.Add(time.Duration(i) * time.Minute).Format(time.RFC3339),
			FinishedAt: now.Add(time.Duration(i)*time.Minute + 30*time.Second).Format(time.RFC3339),
		}
		if err := collector.StoreRunHistory(ctx, campaign, result); err != nil {
			t.Fatalf("store before: %v", err)
		}
	}

	// Store after entries.
	for i := 0; i < 3; i++ {
		result := &CampaignResult{
			RunID:      "a-" + string(rune('a'+i)),
			Status:     "success",
			ToolCalls:  3,
			StartedAt:  splitTime.Add(time.Duration(i) * time.Minute).Format(time.RFC3339),
			FinishedAt: splitTime.Add(time.Duration(i)*time.Minute + 15*time.Second).Format(time.RFC3339),
		}
		if err := collector.StoreRunHistory(ctx, campaign, result); err != nil {
			t.Fatalf("store after: %v", err)
		}
	}

	// Call /evaluate endpoint.
	url := "/evaluate?campaign=eval-e2e&since=" + splitTime.Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	api.handleEvaluate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var eval EvaluationResult
	if err := json.NewDecoder(w.Body).Decode(&eval); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if eval.CampaignID != "eval-e2e" {
		t.Errorf("campaign_id = %q, want eval-e2e", eval.CampaignID)
	}
	if eval.BeforeRuns != 3 {
		t.Errorf("before_runs = %d, want 3", eval.BeforeRuns)
	}
	if eval.AfterRuns != 3 {
		t.Errorf("after_runs = %d, want 3", eval.AfterRuns)
	}
	if !eval.Improved {
		t.Error("expected improved=true")
	}
	// Before: 0% success, After: 100% success → delta = 1.0
	if eval.SuccessRateDelta != 1.0 {
		t.Errorf("success_rate_delta = %v, want 1.0", eval.SuccessRateDelta)
	}
}
