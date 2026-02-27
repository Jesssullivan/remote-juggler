package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewAdapter_UnknownType(t *testing.T) {
	_, err := NewAdapter("unknown", "http://localhost:1234", "", "", "")
	if err == nil {
		t.Fatal("expected error for unknown agent type")
	}
}

func TestNewAdapter_ValidTypes(t *testing.T) {
	types := []string{"ironclaw", "openclaw", "picoclaw", "hexstrike-ai", "hexstrike"}
	for _, typ := range types {
		a, err := NewAdapter(typ, "http://localhost:1234", "http://gw:8080", "", "")
		if err != nil {
			t.Errorf("NewAdapter(%q) error: %v", typ, err)
		}
		if a == nil {
			t.Errorf("NewAdapter(%q) returned nil", typ)
		}
	}
}

func TestAdapter_Health(t *testing.T) {
	// IronClaw health probes /v1/chat/completions (no dedicated health endpoint).
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" && r.Method == http.MethodPost {
			json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{
					{"message": map[string]string{"role": "assistant", "content": "pong"}},
				},
			})
		} else if r.URL.Path == "/health" {
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		}
	}))
	defer agent.Close()

	adapter, err := NewAdapter("ironclaw", agent.URL, "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	adapter.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("health returned %d", w.Code)
	}

	var resp HealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.AgentType != "ironclaw" {
		t.Errorf("expected agent_type=ironclaw, got %s", resp.AgentType)
	}
	if resp.Scaffold {
		t.Error("scaffold should be false")
	}
}

func TestAdapter_StatusIdle(t *testing.T) {
	adapter, err := NewAdapter("ironclaw", "http://localhost:1234", "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	w := httptest.NewRecorder()
	adapter.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status returned %d", w.Code)
	}

	var resp StatusResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "idle" {
		t.Errorf("expected idle status, got %s", resp.Status)
	}
}

func TestAdapter_CampaignMethodNotAllowed(t *testing.T) {
	adapter, err := NewAdapter("ironclaw", "http://localhost:1234", "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/campaign", nil)
	w := httptest.NewRecorder()
	adapter.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestAdapter_CampaignInvalidBody(t *testing.T) {
	adapter, err := NewAdapter("ironclaw", "http://localhost:1234", "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/campaign", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	adapter.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestAdapter_CampaignAccepted(t *testing.T) {
	// Mock IronClaw /v1/responses endpoint.
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{
					{"message": map[string]string{"content": "pong"}},
				},
			})
		case "/v1/responses":
			json.NewEncoder(w).Encode(map[string]any{
				"id": "resp_1", "status": "completed", "output": []any{},
			})
		}
	}))
	defer agent.Close()

	adapter, err := NewAdapter("ironclaw", agent.URL, "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	campaign := `{"id":"test","name":"test campaign","process":["step1"],"tools":["juggler_status"]}`
	body := `{"campaign":` + campaign + `,"run_id":"test-run-1"}`
	req := httptest.NewRequest(http.MethodPost, "/campaign", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	adapter.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var resp CampaignResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "accepted" {
		t.Errorf("expected status=accepted, got %s", resp.Status)
	}
}

func TestAdapter_CampaignConflict(t *testing.T) {
	adapter, err := NewAdapter("ironclaw", "http://localhost:1234", "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// Force running state.
	adapter.mu.Lock()
	adapter.status = "running"
	adapter.mu.Unlock()

	campaign := `{"campaign":{"id":"test"},"run_id":"test-run-2"}`
	req := httptest.NewRequest(http.MethodPost, "/campaign", strings.NewReader(campaign))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	adapter.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
}

func TestExtractFindings(t *testing.T) {
	text := `Here is my analysis.
__findings__[{"title":"SQL injection in login","body":"The login handler concatenates user input into SQL","severity":"critical","labels":["security"],"fingerprint":"sql-inject-login-001"}]__end_findings__
That's all.`

	findings := extractFindings(text, "campaign-1", "run-1")
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	f := findings[0]
	if f.Title != "SQL injection in login" {
		t.Errorf("unexpected title: %s", f.Title)
	}
	if f.Severity != "critical" {
		t.Errorf("unexpected severity: %s", f.Severity)
	}
	if f.CampaignID != "campaign-1" {
		t.Errorf("expected campaign_id=campaign-1, got %s", f.CampaignID)
	}
	if f.RunID != "run-1" {
		t.Errorf("expected run_id=run-1, got %s", f.RunID)
	}
	if f.Fingerprint != "sql-inject-login-001" {
		t.Errorf("unexpected fingerprint: %s", f.Fingerprint)
	}
	if len(f.Labels) != 1 || f.Labels[0] != "security" {
		t.Errorf("unexpected labels: %v", f.Labels)
	}
}

func TestExtractFindingsEmpty(t *testing.T) {
	// No findings markers at all.
	findings := extractFindings("No findings here.", "c1", "r1")
	if findings != nil {
		t.Errorf("expected nil, got %d findings", len(findings))
	}
}

func TestExtractFindingsInvalid(t *testing.T) {
	// Malformed JSON between markers.
	text := "__findings__this is not json__end_findings__"
	findings := extractFindings(text, "c1", "r1")
	if findings != nil {
		t.Errorf("expected nil for invalid JSON, got %d findings", len(findings))
	}
}

func TestExtractFindingsMultiple(t *testing.T) {
	text := `__findings__[
		{"title":"A","body":"a","severity":"high","labels":[],"fingerprint":"fp-a"},
		{"title":"B","body":"b","severity":"low","labels":["docs"],"fingerprint":"fp-b"}
	]__end_findings__`

	findings := extractFindings(text, "c2", "r2")
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}
	if findings[0].Title != "A" || findings[1].Title != "B" {
		t.Errorf("unexpected titles: %s, %s", findings[0].Title, findings[1].Title)
	}
	// All should have campaign/run stamped.
	for _, f := range findings {
		if f.CampaignID != "c2" || f.RunID != "r2" {
			t.Errorf("expected campaign_id=c2, run_id=r2, got %s, %s", f.CampaignID, f.RunID)
		}
	}
}
