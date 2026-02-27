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
