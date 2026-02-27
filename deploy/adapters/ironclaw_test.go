package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIronclawBackend_Type(t *testing.T) {
	b := NewIronclawBackend("http://localhost:18789")
	if b.Type() != "ironclaw" {
		t.Errorf("expected ironclaw, got %s", b.Type())
	}
}

func TestIronclawBackend_Health(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" && r.Method == http.MethodPost {
			json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{
					{"message": map[string]string{"role": "assistant", "content": "pong"}},
				},
			})
		}
	}))
	defer agent.Close()

	b := NewIronclawBackend(agent.URL)
	if err := b.Health(); err != nil {
		t.Fatalf("health error: %v", err)
	}
}

func TestIronclawBackend_HealthUnreachable(t *testing.T) {
	b := NewIronclawBackend("http://localhost:1")
	if err := b.Health(); err == nil {
		t.Fatal("expected error for unreachable agent")
	}
}

func TestIronclawBackend_HealthAcceptsUnauthorized(t *testing.T) {
	// 401 means the server is up but auth is wrong — still healthy.
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer agent.Close()

	b := NewIronclawBackend(agent.URL)
	if err := b.Health(); err != nil {
		t.Fatalf("expected healthy on 401, got: %v", err)
	}
}

func TestIronclawBackend_Dispatch(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/responses" {
			json.NewEncoder(w).Encode(map[string]any{
				"id":     "resp_123",
				"status": "completed",
				"output": []map[string]any{
					{
						"type":      "function_call",
						"id":        "call_1",
						"name":      "juggler_status",
						"arguments": `{"query":"health"}`,
					},
					{
						"type":      "function_call",
						"id":        "call_2",
						"name":      "juggler_keys_list",
						"arguments": `{}`,
					},
					{
						"type": "message",
						"role": "assistant",
						"content": []map[string]any{
							{"type": "output_text", "text": "analysis complete"},
						},
					},
				},
				"usage": map[string]int{
					"input_tokens":  100,
					"output_tokens": 50,
					"total_tokens":  150,
				},
			})
		}
	}))
	defer agent.Close()

	b := NewIronclawBackend(agent.URL)
	campaign := json.RawMessage(`{"id":"test","name":"test","process":["check health"],"tools":["juggler_status"]}`)

	result, err := b.Dispatch(campaign, "run-1")
	if err != nil {
		t.Fatalf("dispatch error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %s", result.Status)
	}
	if result.ToolCalls != 2 {
		t.Errorf("expected 2 tool calls, got %d", result.ToolCalls)
	}
}

func TestIronclawBackend_DispatchError(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer agent.Close()

	b := NewIronclawBackend(agent.URL)
	campaign := json.RawMessage(`{"id":"test","name":"test","process":["fail"],"tools":[]}`)

	result, err := b.Dispatch(campaign, "run-1")
	if err != nil {
		t.Fatalf("dispatch should not return error: %v", err)
	}
	if result.Status != "failure" {
		t.Errorf("expected failure, got %s", result.Status)
	}
}

func TestIronclawBackend_DispatchWithFindings(t *testing.T) {
	findingsJSON := `__findings__[{"title":"XSS in template","body":"Unescaped output","severity":"high","labels":["security"],"fingerprint":"xss-tpl-001"}]__end_findings__`

	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/responses" {
			json.NewEncoder(w).Encode(map[string]any{
				"id":     "resp_f1",
				"status": "completed",
				"output": []map[string]any{
					{
						"type":    "message",
						"role":    "assistant",
						"content": "Analysis complete.\n" + findingsJSON,
					},
				},
			})
		}
	}))
	defer agent.Close()

	b := NewIronclawBackend(agent.URL)
	campaign := json.RawMessage(`{"id":"oc-codeql-fix","name":"CodeQL Fix","process":["scan"],"tools":[]}`)

	result, err := b.Dispatch(campaign, "run-f1")
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
	if f.Title != "XSS in template" {
		t.Errorf("unexpected title: %s", f.Title)
	}
	if f.CampaignID != "oc-codeql-fix" {
		t.Errorf("expected campaign_id=oc-codeql-fix, got %s", f.CampaignID)
	}
	if f.RunID != "run-f1" {
		t.Errorf("expected run_id=run-f1, got %s", f.RunID)
	}
}

func TestIronclawBackend_DispatchAuth(t *testing.T) {
	var gotAuth string
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(map[string]any{
			"id": "resp_1", "status": "completed", "output": []any{},
		})
	}))
	defer agent.Close()

	b := NewIronclawBackend(agent.URL)
	b.SetAuthToken("test-token-123")
	campaign := json.RawMessage(`{"id":"test","name":"test","process":["ping"],"tools":[]}`)

	_, err := b.Dispatch(campaign, "run-1")
	if err != nil {
		t.Fatalf("dispatch error: %v", err)
	}
	if gotAuth != "Bearer test-token-123" {
		t.Errorf("expected Bearer auth header, got %q", gotAuth)
	}
}

func TestIronclawBackend_DispatchEnrichedPrompt(t *testing.T) {
	var capturedBody []byte
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		json.NewEncoder(w).Encode(map[string]any{
			"id": "resp_1", "status": "completed", "output": []any{},
		})
	}))
	defer agent.Close()

	b := NewIronclawBackend(agent.URL)
	campaign := json.RawMessage(`{
		"id":"oc-dep-audit",
		"name":"Cross-Repo Dependency Audit",
		"description":"Audits dependency manifests across all repos",
		"process":["Fetch manifests","Parse dependencies","Find divergences"],
		"tools":["github_fetch","juggler_setec_put"],
		"targets":[{"forge":"github","org":"tinyland-inc","repo":"remote-juggler","branch":"main"}],
		"guardrails":{"maxDuration":"30m","readOnly":true},
		"metrics":{"successCriteria":"All repos scanned","kpis":["repos_scanned","divergences"]},
		"outputs":{"setecKey":"remotejuggler/campaigns/oc-dep-audit"}
	}`)

	_, err := b.Dispatch(campaign, "run-enriched-1")
	if err != nil {
		t.Fatalf("dispatch error: %v", err)
	}

	// Parse the sent payload to extract the prompt.
	var payload map[string]any
	json.Unmarshal(capturedBody, &payload)
	input := payload["input"].([]any)
	msg := input[0].(map[string]any)
	content := msg["content"].(string)

	checks := []string{
		"Cross-Repo Dependency Audit",
		"Audits dependency manifests",
		"tinyland-inc/remote-juggler",
		"rj-tool",
		"github_fetch",
		"Read-Only",
		"All repos scanned",
		"repos_scanned",
		"remotejuggler/campaigns/oc-dep-audit",
	}
	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("prompt missing expected content: %q", check)
		}
	}
}
