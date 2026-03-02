package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestAPI() *APIServer {
	registry := map[string]*Campaign{
		"test-campaign": {
			ID:    "test-campaign",
			Name:  "Test Campaign",
			Agent: "gateway-direct",
			Trigger: CampaignTrigger{
				Schedule: "0 * * * *",
			},
			Guardrails: Guardrails{
				MaxDuration: "5m",
			},
		},
		"another-campaign": {
			ID:    "another-campaign",
			Name:  "Another Campaign",
			Agent: "openclaw",
			Guardrails: Guardrails{
				MaxDuration: "10m",
			},
		},
	}
	scheduler := NewScheduler(registry, nil, nil)
	return NewAPIServer(scheduler, registry)
}

func TestAPIHealth(t *testing.T) {
	api := newTestAPI()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	api.handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", body["status"])
	}
	if body["service"] != "campaign-runner" {
		t.Errorf("expected service=campaign-runner, got %v", body["service"])
	}
	// campaign_count is float64 from JSON
	if count, ok := body["campaign_count"].(float64); !ok || count != 2 {
		t.Errorf("expected campaign_count=2, got %v", body["campaign_count"])
	}
}

func TestAPICampaigns(t *testing.T) {
	api := newTestAPI()
	req := httptest.NewRequest(http.MethodGet, "/campaigns", nil)
	w := httptest.NewRecorder()

	api.handleCampaigns(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body struct {
		Campaigns []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Agent       string `json:"agent"`
			MaxDuration string `json:"max_duration"`
		} `json:"campaigns"`
		Count int `json:"count"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.Count != 2 {
		t.Errorf("expected count=2, got %d", body.Count)
	}
	if len(body.Campaigns) != 2 {
		t.Errorf("expected 2 campaigns, got %d", len(body.Campaigns))
	}
}

func TestAPITriggerMissingParam(t *testing.T) {
	api := newTestAPI()
	req := httptest.NewRequest(http.MethodPost, "/trigger", nil)
	w := httptest.NewRecorder()

	api.handleTrigger(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestAPITriggerNotFound(t *testing.T) {
	api := newTestAPI()
	req := httptest.NewRequest(http.MethodPost, "/trigger?campaign=nonexistent", nil)
	w := httptest.NewRecorder()

	api.handleTrigger(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestAPITriggerMethodNotAllowed(t *testing.T) {
	api := newTestAPI()
	req := httptest.NewRequest(http.MethodGet, "/trigger?campaign=test-campaign", nil)
	w := httptest.NewRecorder()

	api.handleTrigger(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestAPITriggerAccepted(t *testing.T) {
	api := newTestAPI()
	req := httptest.NewRequest(http.MethodPost, "/trigger?campaign=test-campaign", nil)
	w := httptest.NewRecorder()

	api.handleTrigger(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", w.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "accepted" {
		t.Errorf("expected status=accepted, got %v", body["status"])
	}
	if body["campaign_id"] != "test-campaign" {
		t.Errorf("expected campaign_id=test-campaign, got %v", body["campaign_id"])
	}
}

func TestAPIStatusNoRuns(t *testing.T) {
	api := newTestAPI()
	req := httptest.NewRequest(http.MethodGet, "/status?campaign=test-campaign", nil)
	w := httptest.NewRecorder()

	api.handleStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "no_runs" {
		t.Errorf("expected status=no_runs, got %v", body["status"])
	}
}

func TestAPIStatusWithResult(t *testing.T) {
	api := newTestAPI()

	// Record a result.
	api.RecordResult(&CampaignResult{
		CampaignID: "test-campaign",
		RunID:      "test-run-1",
		Status:     "success",
		ToolCalls:  3,
	})

	req := httptest.NewRequest(http.MethodGet, "/status?campaign=test-campaign", nil)
	w := httptest.NewRecorder()

	api.handleStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result CampaignResult
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected status=success, got %v", result.Status)
	}
	if result.ToolCalls != 3 {
		t.Errorf("expected tool_calls=3, got %d", result.ToolCalls)
	}
}

func TestAPIStatusAll(t *testing.T) {
	api := newTestAPI()

	api.RecordResult(&CampaignResult{
		CampaignID: "test-campaign",
		RunID:      "run-1",
		Status:     "success",
	})
	api.RecordResult(&CampaignResult{
		CampaignID: "another-campaign",
		RunID:      "run-2",
		Status:     "failure",
	})

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	w := httptest.NewRecorder()

	api.handleStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body struct {
		Results map[string]*CampaignResult `json:"results"`
		Count   int                        `json:"count"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Count != 2 {
		t.Errorf("expected count=2, got %d", body.Count)
	}
	if body.Results["test-campaign"].Status != "success" {
		t.Errorf("expected test-campaign=success, got %v", body.Results["test-campaign"].Status)
	}
	if body.Results["another-campaign"].Status != "failure" {
		t.Errorf("expected another-campaign=failure, got %v", body.Results["another-campaign"].Status)
	}
}

func TestProofEndpointEmpty(t *testing.T) {
	api := newTestAPI()
	req := httptest.NewRequest(http.MethodGet, "/proof", nil)
	w := httptest.NewRecorder()
	api.handleProof(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body struct {
		Total        int `json:"total"`
		Confabulated int `json:"confabulated"`
		Suspicious   int `json:"suspicious"`
		Honest       int `json:"honest"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Total != 0 {
		t.Errorf("expected 0 entries, got %d", body.Total)
	}
}

func TestProofEndpointDetectsConfabulation(t *testing.T) {
	api := newTestAPI()

	// Record a confabulated result: IronClaw produces findings with 0 tool calls.
	api.RecordResult(&CampaignResult{
		CampaignID: "oc-handoff-response",
		RunID:      "test-run-1",
		Agent:      "ironclaw",
		Status:     "success",
		StartedAt:  "2026-03-02T14:57:21Z",
		FinishedAt: "2026-03-02T14:59:12Z",
		ToolCalls:  0,
		Findings: []Finding{
			{Title: "Command Injection in Remote.chpl", Severity: "critical"},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/proof", nil)
	w := httptest.NewRecorder()
	api.handleProof(w, req)

	var body struct {
		Total        int          `json:"total"`
		Confabulated int          `json:"confabulated"`
		Entries      []ProofEntry `json:"entries"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Confabulated != 1 {
		t.Errorf("expected 1 confabulated, got %d", body.Confabulated)
	}
	if !body.Entries[0].Confabulated {
		t.Error("expected entry to be marked confabulated")
	}
	if !containsFlag(body.Entries[0].Flags, "CONFABULATED") {
		t.Errorf("expected CONFABULATED flag, got %v", body.Entries[0].Flags)
	}
	if !containsFlag(body.Entries[0].Flags, "NO_TOOL_CALLS") {
		t.Errorf("expected NO_TOOL_CALLS flag, got %v", body.Entries[0].Flags)
	}
}

func TestProofEndpointDetectsAllSkipped(t *testing.T) {
	api := newTestAPI()

	// HexStrike: all tools skipped.
	api.RecordResult(&CampaignResult{
		CampaignID: "hs-handoff-response",
		RunID:      "test-run-2",
		Agent:      "hexstrike-ai",
		Status:     "success",
		StartedAt:  "2026-03-02T14:59:17Z",
		FinishedAt: "2026-03-02T14:59:17Z",
		ToolCalls:  6,
		ToolTrace: []ToolTraceEntry{
			{Tool: "github_fetch", Summary: "skipped (gateway tool)"},
			{Tool: "github_list_alerts", Summary: "skipped (gateway tool)"},
		},
		Findings: []Finding{
			{Title: "Some finding", Severity: "high"},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/proof?campaign=hs-handoff-response", nil)
	w := httptest.NewRecorder()
	api.handleProof(w, req)

	var entry ProofEntry
	if err := json.NewDecoder(w.Body).Decode(&entry); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !entry.Confabulated {
		t.Error("expected confabulated (all tools skipped)")
	}
	if !containsFlag(entry.Flags, "ALL_TOOLS_SKIPPED") {
		t.Errorf("expected ALL_TOOLS_SKIPPED flag, got %v", entry.Flags)
	}
	if !containsFlag(entry.Flags, "SUSPICIOUS") {
		t.Errorf("expected SUSPICIOUS flag (0s duration), got %v", entry.Flags)
	}
}

func TestProofEndpointHonestResult(t *testing.T) {
	api := newTestAPI()

	// Gateway-direct: real tool calls, no confabulation.
	api.RecordResult(&CampaignResult{
		CampaignID: "cc-gateway-health",
		RunID:      "test-run-3",
		Agent:      "gateway-direct",
		Status:     "success",
		StartedAt:  "2026-03-01T02:15:18Z",
		FinishedAt: "2026-03-01T02:15:19Z",
		ToolCalls:  5,
	})

	req := httptest.NewRequest(http.MethodGet, "/proof?campaign=cc-gateway-health", nil)
	w := httptest.NewRecorder()
	api.handleProof(w, req)

	var entry ProofEntry
	if err := json.NewDecoder(w.Body).Decode(&entry); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if entry.Confabulated {
		t.Error("gateway-direct should not be confabulated")
	}
	if len(entry.Flags) != 0 {
		t.Errorf("expected no flags, got %v", entry.Flags)
	}
}

func TestProofEndpointCampaignNotFound(t *testing.T) {
	api := newTestAPI()
	req := httptest.NewRequest(http.MethodGet, "/proof?campaign=nonexistent", nil)
	w := httptest.NewRecorder()
	api.handleProof(w, req)

	var body map[string]string
	json.NewDecoder(w.Body).Decode(&body)
	if body["status"] != "no_runs" {
		t.Errorf("expected no_runs, got %v", body["status"])
	}
}

func TestCheckConfabulation(t *testing.T) {
	tests := []struct {
		name     string
		result   CampaignResult
		expected bool
	}{
		{
			name: "no findings = not confabulated",
			result: CampaignResult{
				Agent:    "ironclaw",
				Findings: nil,
			},
			expected: false,
		},
		{
			name: "gateway-direct exempt",
			result: CampaignResult{
				Agent:     "gateway-direct",
				ToolCalls: 0,
				Findings:  []Finding{{Title: "test"}},
			},
			expected: false,
		},
		{
			name: "findings with 0 tool calls = confabulated",
			result: CampaignResult{
				Agent:     "ironclaw",
				ToolCalls: 0,
				Findings:  []Finding{{Title: "test"}},
			},
			expected: true,
		},
		{
			name: "all tools skipped = confabulated",
			result: CampaignResult{
				Agent:     "hexstrike-ai",
				ToolCalls: 3,
				Findings:  []Finding{{Title: "test"}},
				ToolTrace: []ToolTraceEntry{
					{Summary: "skipped (gateway tool)"},
					{Summary: "Skipped (gateway tool)"},
				},
			},
			expected: true,
		},
		{
			name: "real tool calls with findings = not confabulated",
			result: CampaignResult{
				Agent:     "ironclaw",
				ToolCalls: 3,
				Findings:  []Finding{{Title: "test"}},
				ToolTrace: []ToolTraceEntry{
					{Tool: "github_fetch", Summary: "200 OK, 1234 bytes"},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CheckConfabulation(&tt.result)
			if got != tt.expected {
				t.Errorf("CheckConfabulation() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestAPIIntEnvOrDefault(t *testing.T) {
	// Default when not set.
	if got := intEnvOrDefault("CAMPAIGN_TEST_INT_NONEXISTENT", 42); got != 42 {
		t.Errorf("expected 42, got %d", got)
	}

	// Set valid int.
	t.Setenv("CAMPAIGN_TEST_INT_VAR", "8081")
	if got := intEnvOrDefault("CAMPAIGN_TEST_INT_VAR", 42); got != 8081 {
		t.Errorf("expected 8081, got %d", got)
	}

	// Set invalid int returns default.
	t.Setenv("CAMPAIGN_TEST_INT_BAD", "notanumber")
	if got := intEnvOrDefault("CAMPAIGN_TEST_INT_BAD", 42); got != 42 {
		t.Errorf("expected 42, got %d", got)
	}
}
