package main

import (
	"encoding/json"
	"testing"
)

// TestCampaignResultContractMatchesPython verifies the Go CampaignResult struct
// matches the Python agent's output format (agent.py:_make_result).
func TestCampaignResultContractMatchesPython(t *testing.T) {
	// Simulate Python agent output (from agent.py _make_result).
	pythonOutput := `{
		"campaign_id": "oc-dep-audit",
		"run_id": "run-abc123",
		"status": "success",
		"started_at": "2026-02-24T00:00:00Z",
		"finished_at": "2026-02-24T00:05:00Z",
		"agent": "openclaw",
		"kpis": {"repos_scanned": 10, "version_divergences": 3},
		"error": "",
		"tool_calls": 25
	}`

	var result CampaignResult
	if err := json.Unmarshal([]byte(pythonOutput), &result); err != nil {
		t.Fatalf("failed to unmarshal Python agent output into CampaignResult: %v", err)
	}

	if result.CampaignID != "oc-dep-audit" {
		t.Errorf("CampaignID = %q, want 'oc-dep-audit'", result.CampaignID)
	}
	if result.RunID != "run-abc123" {
		t.Errorf("RunID = %q, want 'run-abc123'", result.RunID)
	}
	if result.Status != "success" {
		t.Errorf("Status = %q, want 'success'", result.Status)
	}
	if result.StartedAt != "2026-02-24T00:00:00Z" {
		t.Errorf("StartedAt = %q, want '2026-02-24T00:00:00Z'", result.StartedAt)
	}
	if result.FinishedAt != "2026-02-24T00:05:00Z" {
		t.Errorf("FinishedAt = %q, want '2026-02-24T00:05:00Z'", result.FinishedAt)
	}
	if result.Agent != "openclaw" {
		t.Errorf("Agent = %q, want 'openclaw'", result.Agent)
	}
	if result.ToolCalls != 25 {
		t.Errorf("ToolCalls = %d, want 25", result.ToolCalls)
	}
	if result.KPIs["repos_scanned"] != float64(10) {
		t.Errorf("KPIs[repos_scanned] = %v, want 10", result.KPIs["repos_scanned"])
	}
}

// TestCampaignResultContractMatchesPythonError verifies error result format.
func TestCampaignResultContractMatchesPythonError(t *testing.T) {
	pythonOutput := `{
		"campaign_id": "oc-dep-audit",
		"run_id": "run-err",
		"status": "error",
		"started_at": "2026-02-24T00:00:00Z",
		"finished_at": "2026-02-24T00:00:05Z",
		"agent": "openclaw",
		"kpis": {},
		"error": "anthropic API error: rate_limit_exceeded",
		"tool_calls": 0
	}`

	var result CampaignResult
	if err := json.Unmarshal([]byte(pythonOutput), &result); err != nil {
		t.Fatalf("failed to unmarshal Python error output: %v", err)
	}

	if result.Status != "error" {
		t.Errorf("Status = %q, want 'error'", result.Status)
	}
	if result.Error != "anthropic API error: rate_limit_exceeded" {
		t.Errorf("Error = %q, want 'anthropic API error: rate_limit_exceeded'", result.Error)
	}
	if result.ToolCalls != 0 {
		t.Errorf("ToolCalls = %d, want 0", result.ToolCalls)
	}
}

// TestCampaignResultContractToolTrace verifies tool_trace field round-trips
// between Python agent output and Go struct.
func TestCampaignResultContractToolTrace(t *testing.T) {
	pythonOutput := `{
		"campaign_id": "oc-codeql-fix",
		"run_id": "run-trace",
		"status": "success",
		"started_at": "2026-02-25T06:00:00Z",
		"finished_at": "2026-02-25T06:05:00Z",
		"agent": "openclaw",
		"kpis": {"alerts_fixed": 5},
		"error": "",
		"tool_calls": 12,
		"tool_trace": [
			{"timestamp": "2026-02-25T06:00:01Z", "tool": "juggler_resolve_composite", "summary": "query=github-token, source=setec"},
			{"timestamp": "2026-02-25T06:00:03Z", "tool": "github_list_alerts", "summary": "25 open alerts"},
			{"timestamp": "2026-02-25T06:00:10Z", "tool": "github_update_file", "summary": "failed: 409 conflict", "is_error": true}
		]
	}`

	var result CampaignResult
	if err := json.Unmarshal([]byte(pythonOutput), &result); err != nil {
		t.Fatalf("failed to unmarshal Python output with tool_trace: %v", err)
	}

	if len(result.ToolTrace) != 3 {
		t.Fatalf("ToolTrace length = %d, want 3", len(result.ToolTrace))
	}
	if result.ToolTrace[0].Tool != "juggler_resolve_composite" {
		t.Errorf("ToolTrace[0].Tool = %q, want 'juggler_resolve_composite'", result.ToolTrace[0].Tool)
	}
	if result.ToolTrace[0].Summary != "query=github-token, source=setec" {
		t.Errorf("ToolTrace[0].Summary = %q", result.ToolTrace[0].Summary)
	}
	if result.ToolTrace[2].IsError != true {
		t.Error("ToolTrace[2].IsError should be true")
	}
	if result.ToolTrace[1].IsError != false {
		t.Error("ToolTrace[1].IsError should be false")
	}
}

// TestCampaignResultRoundTrip verifies Go -> JSON -> Go roundtrip.
func TestCampaignResultRoundTrip(t *testing.T) {
	original := CampaignResult{
		CampaignID: "test",
		RunID:      "run-1",
		Status:     "success",
		StartedAt:  "2026-02-24T00:00:00Z",
		FinishedAt: "2026-02-24T00:01:00Z",
		Agent:      "openclaw",
		KPIs:       map[string]any{"count": float64(42)},
		ToolCalls:  10,
		Phases: []PhaseResult{
			{Phase: 1, Agent: "openclaw", Status: "success", ToolCalls: 5},
			{Phase: 2, Agent: "gateway-direct", Status: "success", ToolCalls: 5},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded CampaignResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.CampaignID != original.CampaignID {
		t.Errorf("CampaignID mismatch: %q vs %q", decoded.CampaignID, original.CampaignID)
	}
	if decoded.ToolCalls != original.ToolCalls {
		t.Errorf("ToolCalls mismatch: %d vs %d", decoded.ToolCalls, original.ToolCalls)
	}
	if len(decoded.Phases) != 2 {
		t.Errorf("Phases count = %d, want 2", len(decoded.Phases))
	}
	if decoded.Phases[0].Agent != "openclaw" {
		t.Errorf("Phase[0].Agent = %q, want 'openclaw'", decoded.Phases[0].Agent)
	}
}
