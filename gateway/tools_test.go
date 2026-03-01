package main

import (
	"encoding/json"
	"testing"
)

func TestGatewayToolsCount(t *testing.T) {
	tools := gatewayTools()
	if len(tools) != 23 {
		t.Errorf("gatewayTools() returned %d tools, want 23", len(tools))
	}
}

func TestGatewayToolNames(t *testing.T) {
	expected := map[string]bool{
		"juggler_resolve_composite": true,
		"juggler_setec_list":        true,
		"juggler_setec_get":         true,
		"juggler_setec_put":         true,
		"juggler_audit_log":         true,
		"juggler_campaign_status":   true,
		"juggler_aperture_usage":    true,
		"github_fetch":              true,
		"github_list_alerts":        true,
		"github_get_alert":          true,
		"github_create_branch":      true,
		"github_update_file":        true,
		"github_patch_file":         true,
		"github_create_pr":          true,
		"github_create_issue":       true,
		"juggler_request_secret":    true,
		"juggler_campaign_trigger":  true,
		"juggler_campaign_list":     true,
		"github_discussion_list":    true,
		"github_discussion_get":     true,
		"github_discussion_search":  true,
		"github_discussion_reply":   true,
		"github_discussion_label":   true,
	}

	tools := gatewayTools()
	for _, raw := range tools {
		var tool struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &tool); err != nil {
			t.Fatalf("failed to unmarshal tool: %v", err)
		}
		if !expected[tool.Name] {
			t.Errorf("unexpected tool name: %q", tool.Name)
		}
		delete(expected, tool.Name)
	}

	for name := range expected {
		t.Errorf("missing expected tool: %q", name)
	}
}

func TestGatewayToolsHaveSchemas(t *testing.T) {
	tools := gatewayTools()
	for _, raw := range tools {
		var tool struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		}
		if err := json.Unmarshal(raw, &tool); err != nil {
			t.Fatalf("failed to unmarshal tool: %v", err)
		}
		if tool.Name == "" {
			t.Error("tool has empty name")
		}
		if tool.Description == "" {
			t.Errorf("tool %q has empty description", tool.Name)
		}
		if tool.InputSchema == nil {
			t.Errorf("tool %q has nil inputSchema", tool.Name)
		}
		if tool.InputSchema["type"] != "object" {
			t.Errorf("tool %q inputSchema type = %v, want 'object'", tool.Name, tool.InputSchema["type"])
		}
	}
}

func TestInjectGatewayTools(t *testing.T) {
	// Simulate a Chapel tools/list response with 2 tools.
	chapelResp := `{
		"jsonrpc": "2.0",
		"id": 1,
		"result": {
			"tools": [
				{"name": "juggler_list", "description": "List identities", "inputSchema": {"type": "object"}},
				{"name": "juggler_switch", "description": "Switch identity", "inputSchema": {"type": "object"}}
			]
		}
	}`

	modified := injectGatewayTools([]byte(chapelResp))

	var msg struct {
		Result struct {
			Tools []json.RawMessage `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(modified, &msg); err != nil {
		t.Fatalf("failed to unmarshal modified response: %v", err)
	}

	// Should have 2 Chapel tools + 23 gateway tools = 25.
	if len(msg.Result.Tools) != 25 {
		t.Errorf("injected response has %d tools, want 25", len(msg.Result.Tools))
	}
}

func TestInjectGatewayToolsInvalidJSON(t *testing.T) {
	// Should return input unchanged on parse failure.
	input := []byte(`{invalid json`)
	output := injectGatewayTools(input)
	if string(output) != string(input) {
		t.Error("expected invalid JSON to be passed through unchanged")
	}
}

func TestGatewayOnlyToolsList(t *testing.T) {
	id := json.RawMessage(`42`)
	resp := gatewayOnlyToolsList(id)

	var msg struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  struct {
			Tools []json.RawMessage `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &msg); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if msg.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want 2.0", msg.JSONRPC)
	}
	if msg.ID != 42 {
		t.Errorf("id = %d, want 42", msg.ID)
	}
	// Should have 23 gateway tools (no Chapel tools).
	if len(msg.Result.Tools) != 23 {
		t.Errorf("gateway-only response has %d tools, want 23", len(msg.Result.Tools))
	}
}

func TestIsToolsListResponse(t *testing.T) {
	tests := []struct {
		name string
		data string
		want bool
	}{
		{
			name: "valid tools/list",
			data: `{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"test"}]}}`,
			want: true,
		},
		{
			name: "empty tools array",
			data: `{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`,
			want: true,
		},
		{
			name: "non-tools response",
			data: `{"jsonrpc":"2.0","id":1,"result":{"content":[{"text":"hi"}]}}`,
			want: false,
		},
		{
			name: "error response",
			data: `{"jsonrpc":"2.0","id":1,"error":{"code":-1,"message":"fail"}}`,
			want: false,
		},
		{
			name: "invalid JSON",
			data: `{garbage`,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isToolsListResponse([]byte(tt.data))
			if got != tt.want {
				t.Errorf("isToolsListResponse() = %v, want %v", got, tt.want)
			}
		})
	}
}
