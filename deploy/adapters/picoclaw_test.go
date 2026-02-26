package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPicoclawBackend_Type(t *testing.T) {
	b := NewPicoclawBackend("http://localhost:18790", "")
	if b.Type() != "picoclaw" {
		t.Errorf("expected picoclaw, got %s", b.Type())
	}
}

func TestPicoclawBackend_Health(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		} else if r.URL.Path == "/health" {
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		}
	}))
	defer agent.Close()

	b := NewPicoclawBackend(agent.URL, "")
	if err := b.Health(); err != nil {
		t.Fatalf("health error: %v", err)
	}
}

func TestPicoclawBackend_HealthFallback(t *testing.T) {
	// Server only supports legacy /health, not /api/status.
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		} else {
			http.NotFound(w, r)
		}
	}))
	defer agent.Close()

	b := NewPicoclawBackend(agent.URL, "")
	if err := b.Health(); err != nil {
		t.Fatalf("health fallback error: %v", err)
	}
}

func TestPicoclawBackend_Dispatch(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/dispatch" {
			t.Errorf("expected /api/dispatch, got %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		// Verify request format.
		body, _ := io.ReadAll(r.Body)
		var req map[string]string
		json.Unmarshal(body, &req)
		if req["content"] == "" {
			t.Error("expected non-empty content")
		}
		if req["session_key"] == "" {
			t.Error("expected non-empty session_key")
		}

		json.NewEncoder(w).Encode(map[string]string{
			"content":       "scan complete, no issues found",
			"finish_reason": "stop",
		})
	}))
	defer agent.Close()

	b := NewPicoclawBackend(agent.URL, "")
	campaign := json.RawMessage(`{"id":"test","name":"test","process":["scan"],"tools":[]}`)

	result, err := b.Dispatch(campaign, "run-1")
	if err != nil {
		t.Fatalf("dispatch error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %s (error: %s)", result.Status, result.Error)
	}
}

func TestPicoclawBackend_DispatchWithFindings(t *testing.T) {
	findingsJSON := `__findings__[{"title":"Dead code in utils.go","body":"Function unused","severity":"low","labels":["cleanup"],"fingerprint":"dead-code-utils-001"}]__end_findings__`

	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"content":       "Scan complete.\n" + findingsJSON,
			"finish_reason": "stop",
		})
	}))
	defer agent.Close()

	b := NewPicoclawBackend(agent.URL, "")
	campaign := json.RawMessage(`{"id":"oc-dead-code","name":"Dead Code","process":["scan"],"tools":[]}`)

	result, err := b.Dispatch(campaign, "run-p1")
	if err != nil {
		t.Fatalf("dispatch error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %s", result.Status)
	}
	if len(result.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(result.Findings))
	}
	f := result.Findings[0]
	if f.Title != "Dead code in utils.go" {
		t.Errorf("unexpected title: %s", f.Title)
	}
	if f.CampaignID != "oc-dead-code" {
		t.Errorf("expected campaign_id=oc-dead-code, got %s", f.CampaignID)
	}
}

func TestPicoclawBackend_DispatchError(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"content":       "",
			"finish_reason": "error",
			"error":         "tool execution failed",
		})
	}))
	defer agent.Close()

	b := NewPicoclawBackend(agent.URL, "")
	campaign := json.RawMessage(`{"id":"test","name":"test","process":["scan"],"tools":[]}`)

	result, err := b.Dispatch(campaign, "run-1")
	if err != nil {
		t.Fatalf("dispatch error: %v", err)
	}
	if result.Status != "failure" {
		t.Errorf("expected failure, got %s", result.Status)
	}
	if result.Error != "tool execution failed" {
		t.Errorf("expected error message, got %s", result.Error)
	}
}

func TestPicoclawBackend_DispatchHTTPError(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer agent.Close()

	b := NewPicoclawBackend(agent.URL, "")
	campaign := json.RawMessage(`{"id":"test","name":"test","process":["scan"],"tools":[]}`)

	result, err := b.Dispatch(campaign, "run-1")
	if err != nil {
		t.Fatalf("dispatch error: %v", err)
	}
	if result.Status != "failure" {
		t.Errorf("expected failure, got %s", result.Status)
	}
}
