package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestDispatchAgenticToolLoop verifies the agentic dispatch loop:
// 1. Calls Anthropic API, gets tool_use response
// 2. Executes tool via gateway MCP
// 3. Sends tool_result back to API
// 4. Gets end_turn response with text
func TestDispatchAgenticToolLoop(t *testing.T) {
	var anthropicCalls atomic.Int32
	var gatewayCalls atomic.Int32

	// Mock Anthropic API: first call returns tool_use, second returns end_turn.
	anthropic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.Error(w, "not found", 404)
			return
		}
		call := anthropicCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")

		if call == 1 {
			// First call: return tool_use.
			json.NewEncoder(w).Encode(map[string]any{
				"id":   "msg_1",
				"type": "message",
				"role": "assistant",
				"content": []map[string]any{
					{
						"type": "text",
						"text": "Let me fetch that file.",
					},
					{
						"type":  "tool_use",
						"id":    "toolu_1",
						"name":  "github_fetch",
						"input": json.RawMessage(`{"owner":"tinyland-inc","repo":"remote-juggler","path":"gateway/main.go"}`),
					},
				},
				"stop_reason": "tool_use",
				"usage":       map[string]int{"input_tokens": 100, "output_tokens": 50},
			})
		} else {
			// Second call: return end_turn with findings.
			json.NewEncoder(w).Encode(map[string]any{
				"id":   "msg_2",
				"type": "message",
				"role": "assistant",
				"content": []map[string]any{
					{
						"type": "text",
						"text": "I found the main function.\n__memory__\nDate: 2026-03-02\nWhat I did: Read gateway/main.go\n__end_memory__",
					},
				},
				"stop_reason": "end_turn",
				"usage":       map[string]int{"input_tokens": 200, "output_tokens": 100},
			})
		}
	}))
	defer anthropic.Close()

	// Mock gateway MCP: serves both tools/list and tools/call.
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mcp" {
			// Parse request to distinguish tools/list from tools/call.
			var req struct {
				Method string `json:"method"`
			}
			body, _ := io.ReadAll(r.Body)
			json.Unmarshal(body, &req)

			if req.Method == "tools/list" {
				// Return tool definitions.
				json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      1,
					"result": map[string]any{
						"tools": []map[string]any{
							{
								"name":        "github_fetch",
								"description": "Fetch a file from GitHub",
								"inputSchema": json.RawMessage(`{"type":"object","properties":{"owner":{"type":"string"},"repo":{"type":"string"},"path":{"type":"string"}}}`),
							},
						},
					},
				})
			} else {
				// tools/call — actual tool execution.
				gatewayCalls.Add(1)

				// Verify X-Agent-Identity header on tool calls.
				if r.Header.Get("X-Agent-Identity") != "tinyclaw" {
					t.Errorf("expected X-Agent-Identity=tinyclaw, got %s", r.Header.Get("X-Agent-Identity"))
				}

				json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      1,
					"result": map[string]any{
						"content": []map[string]any{
							{"type": "text", "text": "package main\n\nfunc main() {\n\tlog.Println(\"starting gateway\")\n}"},
						},
					},
				})
			}
		}
	}))
	defer gateway.Close()

	b := NewTinyclawBackend("http://unused:18790", gateway.URL)
	b.SetAPIKey("test-key")
	b.SetAPIBaseURL(anthropic.URL)
	b.SetModel("claude-test")

	campaign := json.RawMessage(`{
		"id": "tc-test",
		"name": "Test Campaign",
		"description": "Test agentic dispatch",
		"process": ["Fetch the file", "Report findings"],
		"tools": ["github_fetch"],
		"targets": [{"forge":"github","org":"tinyland-inc","repo":"remote-juggler"}],
		"guardrails": {"maxDuration":"5m","readOnly":true}
	}`)

	result, err := b.Dispatch(campaign, "test-run-1")
	if err != nil {
		t.Fatalf("Dispatch error: %v", err)
	}

	if result.Status != "success" {
		t.Errorf("expected status=success, got %s (error: %s)", result.Status, result.Error)
	}
	if result.ToolCalls != 1 {
		t.Errorf("expected 1 tool call, got %d", result.ToolCalls)
	}
	if anthropicCalls.Load() != 2 {
		t.Errorf("expected 2 Anthropic API calls, got %d", anthropicCalls.Load())
	}
	if gatewayCalls.Load() != 1 {
		t.Errorf("expected 1 gateway MCP call, got %d", gatewayCalls.Load())
	}
	if result.Reflection == "" {
		t.Error("expected reflection to be extracted")
	}
	if len(result.ToolTrace) != 1 {
		t.Errorf("expected 1 tool trace entry, got %d", len(result.ToolTrace))
	}
	if result.ToolTrace[0].Tool != "github_fetch" {
		t.Errorf("expected tool=github_fetch, got %s", result.ToolTrace[0].Tool)
	}
}

// TestDispatchAgenticNoAPIKey verifies that without an API key,
// dispatch falls through to PicoClaw text dispatch.
func TestDispatchAgenticNoAPIKey(t *testing.T) {
	var picoClawCalled atomic.Int32

	// Mock PicoClaw.
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/dispatch" {
			picoClawCalled.Add(1)
			json.NewEncoder(w).Encode(map[string]any{
				"content":       "Analysis complete. No issues found.",
				"finish_reason": "stop",
			})
		} else if r.URL.Path == "/api/status" || r.URL.Path == "/health" {
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		}
	}))
	defer agent.Close()

	b := NewTinyclawBackend(agent.URL, "http://gateway:8080")
	// No SetAPIKey call — should use PicoClaw path.

	campaign := json.RawMessage(`{
		"id": "tc-test-nokey",
		"name": "Test No Key",
		"process": ["Do analysis"],
		"tools": ["github_fetch"]
	}`)

	result, err := b.Dispatch(campaign, "test-run-2")
	if err != nil {
		t.Fatalf("Dispatch error: %v", err)
	}

	if picoClawCalled.Load() != 1 {
		t.Errorf("expected PicoClaw to be called, got %d calls", picoClawCalled.Load())
	}
	if result.Status != "success" {
		t.Errorf("expected status=success, got %s", result.Status)
	}
}

// TestDispatchAgenticMaxIterations verifies the loop cap prevents infinite loops.
func TestDispatchAgenticMaxIterations(t *testing.T) {
	var anthropicCalls atomic.Int32

	// Mock Anthropic API: always returns tool_use (never end_turn).
	anthropic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		anthropicCalls.Add(1)
		json.NewEncoder(w).Encode(map[string]any{
			"id":   "msg_loop",
			"type": "message",
			"role": "assistant",
			"content": []map[string]any{
				{
					"type":  "tool_use",
					"id":    "toolu_loop",
					"name":  "github_fetch",
					"input": json.RawMessage(`{"owner":"test","repo":"test","path":"test.go"}`),
				},
			},
			"stop_reason": "tool_use",
			"usage":       map[string]int{"input_tokens": 10, "output_tokens": 10},
		})
	}))
	defer anthropic.Close()

	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": "file content"},
				},
			},
		})
	}))
	defer gateway.Close()

	b := NewTinyclawBackend("http://unused:18790", gateway.URL)
	b.SetAPIKey("test-key")
	b.SetAPIBaseURL(anthropic.URL)
	b.maxToolIterations = 3 // Cap at 3 iterations.

	campaign := json.RawMessage(`{
		"id": "tc-test-loop",
		"name": "Loop Test",
		"process": ["Do something"],
		"tools": ["github_fetch"]
	}`)

	result, err := b.Dispatch(campaign, "test-run-3")
	if err != nil {
		t.Fatalf("Dispatch error: %v", err)
	}

	// Should stop after 3 iterations.
	if anthropicCalls.Load() > 3 {
		t.Errorf("expected at most 3 Anthropic calls, got %d", anthropicCalls.Load())
	}
	if result.ToolCalls != 3 {
		t.Errorf("expected 3 tool calls (one per iteration), got %d", result.ToolCalls)
	}
}

// TestCallGatewayTool verifies the MCP JSON-RPC request format.
func TestCallGatewayTool(t *testing.T) {
	var receivedBody map[string]any

	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedBody)

		// Verify headers.
		if r.Header.Get("X-Agent-Identity") != "tinyclaw" {
			t.Errorf("expected X-Agent-Identity=tinyclaw, got %s", r.Header.Get("X-Agent-Identity"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type=application/json, got %s", r.Header.Get("Content-Type"))
		}

		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": "tool output here"},
				},
			},
		})
	}))
	defer gateway.Close()

	b := NewTinyclawBackend("http://unused:18790", gateway.URL)
	result, err := b.callGatewayTool("github_fetch", map[string]any{
		"owner": "tinyland-inc",
		"repo":  "remote-juggler",
		"path":  "gateway/main.go",
	})
	if err != nil {
		t.Fatalf("callGatewayTool error: %v", err)
	}

	if result != "tool output here" {
		t.Errorf("expected 'tool output here', got %q", result)
	}

	// Verify JSON-RPC structure.
	if receivedBody["jsonrpc"] != "2.0" {
		t.Errorf("expected jsonrpc=2.0, got %v", receivedBody["jsonrpc"])
	}
	if receivedBody["method"] != "tools/call" {
		t.Errorf("expected method=tools/call, got %v", receivedBody["method"])
	}
	params, ok := receivedBody["params"].(map[string]any)
	if !ok {
		t.Fatal("params should be a map")
	}
	if params["name"] != "github_fetch" {
		t.Errorf("expected name=github_fetch, got %v", params["name"])
	}
}

// TestCallGatewayToolMCPError verifies MCP error handling.
func TestCallGatewayToolMCPError(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"error": map[string]any{
				"code":    -32600,
				"message": "tool not found",
			},
		})
	}))
	defer gateway.Close()

	b := NewTinyclawBackend("http://unused:18790", gateway.URL)
	_, err := b.callGatewayTool("nonexistent_tool", nil)
	if err == nil {
		t.Fatal("expected error for MCP error response")
	}
	if !strings.Contains(err.Error(), "tool not found") {
		t.Errorf("expected 'tool not found' in error, got: %v", err)
	}
}

// TestFetchGatewayToolsAnthropicFormat verifies Anthropic tool format conversion.
func TestFetchGatewayToolsAnthropicFormat(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]any{
				"tools": []map[string]any{
					{
						"name":        "github_fetch",
						"description": "Fetch a file from GitHub",
						"inputSchema": json.RawMessage(`{"type":"object","properties":{"owner":{"type":"string"},"repo":{"type":"string"},"path":{"type":"string"}},"required":["owner","repo","path"]}`),
					},
					{
						"name":        "juggler_status",
						"description": "Check status",
						"inputSchema": json.RawMessage(`{"type":"object","properties":{}}`),
					},
					{
						"name":        "juggler_setec_get",
						"description": "Get a Setec value",
						"inputSchema": json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`),
					},
				},
			},
		})
	}))
	defer gateway.Close()

	// Test with filtering: only github_fetch.
	tools, err := fetchGatewayToolsAnthropic(http.DefaultClient, gateway.URL, []string{"github_fetch"})
	if err != nil {
		t.Fatalf("fetchGatewayToolsAnthropic error: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool (filtered), got %d", len(tools))
	}
	if tools[0].Name != "github_fetch" {
		t.Errorf("expected name=github_fetch, got %s", tools[0].Name)
	}
	if tools[0].Description != "Fetch a file from GitHub" {
		t.Errorf("unexpected description: %s", tools[0].Description)
	}

	// Verify input_schema is set (not nil).
	if tools[0].InputSchema == nil {
		t.Error("input_schema should not be nil")
	}

	// Verify JSON serialization uses "input_schema" not "inputSchema".
	b, _ := json.Marshal(tools[0])
	if !strings.Contains(string(b), `"input_schema"`) {
		t.Errorf("expected 'input_schema' in JSON, got: %s", string(b))
	}
	if strings.Contains(string(b), `"inputSchema"`) {
		t.Errorf("should not contain 'inputSchema' in JSON, got: %s", string(b))
	}

	// Test without filtering: all tools.
	allTools, err := fetchGatewayToolsAnthropic(http.DefaultClient, gateway.URL, nil)
	if err != nil {
		t.Fatalf("fetchGatewayToolsAnthropic (all) error: %v", err)
	}
	if len(allTools) != 3 {
		t.Fatalf("expected 3 tools (unfiltered), got %d", len(allTools))
	}
}
