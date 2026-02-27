package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// mockGateway simulates rj-gateway for E2E tests.
// It handles JSON-RPC 2.0 MCP protocol: tools/list, tools/call.
type mockGateway struct {
	mu        sync.Mutex
	toolCalls []string // Tool names called in order.
	secrets   map[string]string
	failTools map[string]bool // Tools that should return errors.
}

func newMockGateway() *mockGateway {
	return &mockGateway{
		secrets:   make(map[string]string),
		failTools: make(map[string]bool),
	}
}

func (m *mockGateway) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", m.handleMCP)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	return mux
}

func (m *mockGateway) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, _ := io.ReadAll(r.Body)
	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"params"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	switch req.Method {
	case "tools/list":
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]any{
				"tools": []map[string]any{
					{"name": "juggler_setec_list", "description": "List secrets", "inputSchema": map[string]any{"type": "object"}},
					{"name": "juggler_setec_get", "description": "Get secret", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}}},
					{"name": "juggler_setec_put", "description": "Put secret", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}, "value": map[string]any{"type": "string"}}}},
					{"name": "juggler_audit_log", "description": "Query audit", "inputSchema": map[string]any{"type": "object"}},
					{"name": "juggler_campaign_status", "description": "Campaign status", "inputSchema": map[string]any{"type": "object"}},
					{"name": "juggler_resolve_composite", "description": "Resolve secret", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}}},
				},
			},
		})

	case "tools/call":
		m.mu.Lock()
		m.toolCalls = append(m.toolCalls, req.Params.Name)
		m.mu.Unlock()

		if m.failTools[req.Params.Name] {
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error":   map[string]any{"code": -32000, "message": "tool failed"},
			})
			return
		}

		switch req.Params.Name {
		case "juggler_setec_put":
			var args struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			}
			json.Unmarshal(req.Params.Arguments, &args)
			m.mu.Lock()
			m.secrets[args.Name] = args.Value
			m.mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": fmt.Sprintf("stored %s", args.Name)}},
				},
			})

		case "juggler_setec_get":
			var args struct {
				Name string `json:"name"`
			}
			json.Unmarshal(req.Params.Arguments, &args)
			m.mu.Lock()
			val, ok := m.secrets[args.Name]
			m.mu.Unlock()
			if !ok {
				json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"error":   map[string]any{"code": -32000, "message": "not found"},
				})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": val}},
				},
			})

		default:
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": `{"result": "ok"}`}},
				},
			})
		}

	default:
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"error":   map[string]any{"code": -32601, "message": "method not found"},
		})
	}
}

func (m *mockGateway) getToolCalls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.toolCalls))
	copy(result, m.toolCalls)
	return result
}

func (m *mockGateway) getSecret(key string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.secrets[key]
	return v, ok
}

// TestE2EFullSchedulerDispatchCollect tests the full scheduler -> dispatch -> collect flow.
func TestE2EFullSchedulerDispatchCollect(t *testing.T) {
	gw := newMockGateway()
	server := httptest.NewServer(gw.Handler())
	defer server.Close()

	campaign := &Campaign{
		ID:    "e2e-test",
		Name:  "E2E Test Campaign",
		Agent: "gateway-direct",
		Tools: []string{"juggler_setec_list", "juggler_audit_log", "juggler_campaign_status"},
		Guardrails: Guardrails{
			MaxDuration: "30s",
		},
		Outputs: CampaignOutputs{
			SetecKey: "remotejuggler/campaigns/e2e-test",
		},
	}

	dispatcher := NewDispatcher(server.URL, "", "", "")
	collector := NewCollector(server.URL)
	registry := map[string]*Campaign{"e2e-test": campaign}
	scheduler := NewScheduler(registry, dispatcher, collector)

	var recordedResult *CampaignResult
	scheduler.OnResult = func(r *CampaignResult) {
		recordedResult = r
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := scheduler.RunCampaign(ctx, campaign)
	if err != nil {
		t.Fatalf("RunCampaign: %v", err)
	}

	// Verify tools were called.
	calls := gw.getToolCalls()
	if len(calls) < 3 {
		t.Errorf("expected at least 3 tool calls, got %d: %v", len(calls), calls)
	}

	// Verify result was stored in Setec.
	stored, ok := gw.getSecret("remotejuggler/campaigns/e2e-test/latest")
	if !ok {
		t.Fatal("expected campaign result stored in Setec /latest")
	}
	var result CampaignResult
	if err := json.Unmarshal([]byte(stored), &result); err != nil {
		t.Fatalf("unmarshal stored result: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("stored result status = %q, want 'success'", result.Status)
	}
	if result.ToolCalls < 3 {
		t.Errorf("stored result tool_calls = %d, want >= 3", result.ToolCalls)
	}

	// Verify OnResult callback was called.
	if recordedResult == nil {
		t.Fatal("expected OnResult callback to be called")
	}
	if recordedResult.CampaignID != "e2e-test" {
		t.Errorf("recorded campaign_id = %q, want 'e2e-test'", recordedResult.CampaignID)
	}
}

// TestE2EDirectDispatch tests direct dispatch (gateway-direct agent) with sequential tool calls.
func TestE2EDirectDispatch(t *testing.T) {
	gw := newMockGateway()
	server := httptest.NewServer(gw.Handler())
	defer server.Close()

	campaign := &Campaign{
		ID:    "direct-test",
		Agent: "gateway-direct",
		Tools: []string{"juggler_setec_list", "juggler_audit_log", "juggler_resolve_composite"},
	}

	dispatcher := NewDispatcher(server.URL, "", "", "")
	ctx := context.Background()

	result, err := dispatcher.Dispatch(ctx, campaign, "run-1")
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	if result.ToolCalls != 3 {
		t.Errorf("ToolCalls = %d, want 3", result.ToolCalls)
	}

	calls := gw.getToolCalls()
	expected := []string{"juggler_setec_list", "juggler_audit_log", "juggler_resolve_composite"}
	for i, name := range expected {
		if i >= len(calls) || calls[i] != name {
			t.Errorf("call[%d] = %q, want %q", i, calls[i], name)
		}
	}
}

// TestE2EAgentDispatch tests dispatch to an agent container (mock agent).
func TestE2EAgentDispatch(t *testing.T) {
	// Mock agent that immediately completes.
	agentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/campaign":
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
		case "/status":
			json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"last_result": map[string]any{
					"status":     "success",
					"tool_calls": 7,
					"kpis":       map[string]any{"repos_scanned": 10},
					"error":      "",
				},
			})
		}
	}))
	defer agentServer.Close()

	dispatcher := &Dispatcher{
		gatewayURL: "http://unused",
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
	campaign := &Campaign{ID: "agent-test", Agent: "openclaw"}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := dispatcher.dispatchToAgent(ctx, campaign, "run-1", agentServer.URL)
	if err != nil {
		t.Fatalf("dispatchToAgent: %v", err)
	}

	if result.ToolCalls != 7 {
		t.Errorf("ToolCalls = %d, want 7", result.ToolCalls)
	}
	if result.KPIs["repos_scanned"] != float64(10) {
		t.Errorf("KPIs[repos_scanned] = %v, want 10", result.KPIs["repos_scanned"])
	}
}

// TestE2ETimeoutHandling tests that maxDuration is enforced.
func TestE2ETimeoutHandling(t *testing.T) {
	// Slow mock gateway that takes 5s per tool call.
	slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  map[string]any{"content": []map[string]any{{"type": "text", "text": "ok"}}},
		})
	}))
	defer slowServer.Close()

	campaign := &Campaign{
		ID:    "timeout-test",
		Agent: "gateway-direct",
		Tools: []string{"juggler_setec_list", "juggler_audit_log", "juggler_campaign_status"},
		Guardrails: Guardrails{
			MaxDuration: "1s",
		},
		Outputs: CampaignOutputs{SetecKey: "test"},
	}

	dispatcher := NewDispatcher(slowServer.URL, "", "", "")
	scheduler := NewScheduler(map[string]*Campaign{"timeout-test": campaign}, dispatcher, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	var result *CampaignResult
	scheduler.OnResult = func(r *CampaignResult) { result = r }

	_ = scheduler.RunCampaign(ctx, campaign)

	if result == nil {
		t.Fatal("expected result from timeout campaign")
	}
	if result.Status != "timeout" && result.Status != "failure" && result.Status != "error" {
		t.Errorf("status = %q, want timeout/failure/error", result.Status)
	}
}

// TestE2EToolFailure tests that a tool returning HTTP 500 doesn't crash the flow.
func TestE2EToolFailure(t *testing.T) {
	gw := newMockGateway()
	gw.failTools["juggler_audit_log"] = true
	server := httptest.NewServer(gw.Handler())
	defer server.Close()

	campaign := &Campaign{
		ID:    "fail-test",
		Agent: "gateway-direct",
		Tools: []string{"juggler_setec_list", "juggler_audit_log", "juggler_campaign_status"},
	}

	dispatcher := NewDispatcher(server.URL, "", "", "")
	ctx := context.Background()

	result, err := dispatcher.Dispatch(ctx, campaign, "run-1")
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	// All 3 tools should have been attempted (continue on error).
	if result.ToolCalls != 3 {
		t.Errorf("ToolCalls = %d, want 3 (should continue despite error)", result.ToolCalls)
	}
}

// TestE2EGatewayUnavailable tests dispatcher handling of an unreachable gateway.
func TestE2EGatewayUnavailable(t *testing.T) {
	// Get a port that's not listening.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().String()
	ln.Close()

	campaign := &Campaign{
		ID:    "unavail-test",
		Agent: "gateway-direct",
		Tools: []string{"juggler_setec_list"},
	}

	dispatcher := NewDispatcher("http://"+port, "", "", "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := dispatcher.Dispatch(ctx, campaign, "run-1")
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	// Tool call attempt should have been made (and failed).
	if result.ToolCalls != 1 {
		t.Errorf("ToolCalls = %d, want 1 (attempt counted)", result.ToolCalls)
	}
}

// TestE2EKillSwitch tests that the global kill switch halts campaign execution.
func TestE2EKillSwitch(t *testing.T) {
	gw := newMockGateway()
	// Pre-set kill switch.
	gw.secrets["campaigns/global-kill"] = "true"
	server := httptest.NewServer(gw.Handler())
	defer server.Close()

	campaign := &Campaign{
		ID:    "kill-test",
		Agent: "gateway-direct",
		Tools: []string{"juggler_setec_list"},
		Outputs: CampaignOutputs{
			SetecKey: "remotejuggler/campaigns/kill-test",
		},
	}

	dispatcher := NewDispatcher(server.URL, "", "", "")
	collector := NewCollector(server.URL)
	scheduler := NewScheduler(map[string]*Campaign{"kill-test": campaign}, dispatcher, collector)

	ctx := context.Background()
	err := scheduler.RunCampaign(ctx, campaign)

	if err == nil {
		t.Fatal("expected error from kill switch")
	}
	if err.Error() != "global kill switch active" {
		t.Errorf("error = %q, want 'global kill switch active'", err.Error())
	}

	// Verify no campaign tools were called (only setec_get for kill switch).
	calls := gw.getToolCalls()
	for _, c := range calls {
		if c != "juggler_setec_get" && c != "juggler_setec_put" {
			t.Errorf("unexpected tool call %q after kill switch", c)
		}
	}
}

// TestE2ECollectorStoresResult tests that collector writes to /latest and /runs/{runID}.
func TestE2ECollectorStoresResult(t *testing.T) {
	gw := newMockGateway()
	server := httptest.NewServer(gw.Handler())
	defer server.Close()

	collector := NewCollector(server.URL)
	campaign := &Campaign{
		ID: "collect-test",
		Outputs: CampaignOutputs{
			SetecKey: "remotejuggler/campaigns/collect-test",
		},
	}
	result := &CampaignResult{
		CampaignID: "collect-test",
		RunID:      "run-abc",
		Status:     "success",
		ToolCalls:  5,
	}

	ctx := context.Background()
	if err := collector.StoreResult(ctx, campaign, result); err != nil {
		t.Fatalf("StoreResult: %v", err)
	}

	// Check /latest.
	latest, ok := gw.getSecret("remotejuggler/campaigns/collect-test/latest")
	if !ok {
		t.Fatal("expected /latest to be stored")
	}
	var latestResult CampaignResult
	json.Unmarshal([]byte(latest), &latestResult)
	if latestResult.Status != "success" {
		t.Errorf("latest status = %q, want 'success'", latestResult.Status)
	}

	// Check /runs/{runID}.
	history, ok := gw.getSecret("remotejuggler/campaigns/collect-test/runs/run-abc")
	if !ok {
		t.Fatal("expected /runs/run-abc to be stored")
	}
	var historyResult CampaignResult
	json.Unmarshal([]byte(history), &historyResult)
	if historyResult.RunID != "run-abc" {
		t.Errorf("history run_id = %q, want 'run-abc'", historyResult.RunID)
	}
}

// TestE2EBudgetEnforcement tests that campaigns are halted when token budget is exceeded.
func TestE2EBudgetEnforcement(t *testing.T) {
	gw := newMockGateway()
	server := httptest.NewServer(gw.Handler())
	defer server.Close()

	maxTokens := 50 // Very small budget — first tool response will exceed it.
	campaign := &Campaign{
		ID:    "budget-test",
		Agent: "gateway-direct",
		Tools: []string{"juggler_setec_list", "juggler_audit_log", "juggler_setec_list"},
		Outputs: CampaignOutputs{
			SetecKey: "remotejuggler/campaigns/budget-test",
		},
		Guardrails: Guardrails{
			AIApiBudget: &AIBudget{MaxTokens: maxTokens},
		},
	}

	dispatcher := NewDispatcher(server.URL, "", "", "")
	collector := NewCollector(server.URL)
	scheduler := NewScheduler(map[string]*Campaign{"budget-test": campaign}, dispatcher, collector)

	var capturedResult *CampaignResult
	scheduler.OnResult = func(r *CampaignResult) {
		capturedResult = r
	}

	ctx := context.Background()
	err := scheduler.RunCampaign(ctx, campaign)

	// Campaign should complete (no error returned) but with budget_exceeded status.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedResult == nil {
		t.Fatal("no result captured")
	}

	if capturedResult.Status != "budget_exceeded" {
		t.Errorf("status = %q, want 'budget_exceeded'", capturedResult.Status)
	}

	// Should have executed only 1 tool (first succeeds, then budget check halts before second).
	if capturedResult.ToolCalls < 1 {
		t.Errorf("expected at least 1 tool call, got %d", capturedResult.ToolCalls)
	}
	if capturedResult.ToolCalls >= 3 {
		t.Errorf("expected fewer than 3 tool calls (budget should halt), got %d", capturedResult.ToolCalls)
	}

	if capturedResult.TokensUsed == 0 {
		t.Error("expected tokens_used > 0")
	}
}

// TestE2ENoBudgetNoCap tests that campaigns without a budget run all tools.
func TestE2ENoBudgetNoCap(t *testing.T) {
	gw := newMockGateway()
	server := httptest.NewServer(gw.Handler())
	defer server.Close()

	campaign := &Campaign{
		ID:    "no-budget-test",
		Agent: "gateway-direct",
		Tools: []string{"juggler_setec_list", "juggler_audit_log"},
		Outputs: CampaignOutputs{
			SetecKey: "remotejuggler/campaigns/no-budget-test",
		},
		Guardrails: Guardrails{}, // No budget set.
	}

	dispatcher := NewDispatcher(server.URL, "", "", "")
	result, err := dispatcher.Dispatch(context.Background(), campaign, "run-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ToolCalls != 2 {
		t.Errorf("tool_calls = %d, want 2", result.ToolCalls)
	}
	if result.Error != "" {
		t.Errorf("unexpected error: %q", result.Error)
	}
}
