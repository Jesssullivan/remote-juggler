package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// fetchGatewayTools retrieves the MCP tool definitions from rj-gateway
// and converts them to OpenAI-compatible function tool format for TinyClaw.
// TinyClaw lacks native MCP support (upstream issue #290), so this proxy
// bridges the gap by registering gateway tools in TinyClaw's format.
func fetchGatewayTools(client *http.Client, gatewayURL string) ([]map[string]any, error) {
	// Request tool list from gateway via MCP JSON-RPC.
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, gatewayURL+"/mcp", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gateway tools/list: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	var mcpResp struct {
		Result struct {
			Tools []struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &mcpResp); err != nil {
		return nil, fmt.Errorf("parse tools: %w", err)
	}

	// Convert MCP tools to OpenAI function-calling format.
	var tools []map[string]any
	for _, t := range mcpResp.Result.Tools {
		tool := map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
			},
		}
		if len(t.InputSchema) > 0 {
			var schema map[string]any
			if err := json.Unmarshal(t.InputSchema, &schema); err == nil {
				tool["function"].(map[string]any)["parameters"] = schema
			}
		}
		tools = append(tools, tool)
	}

	return tools, nil
}
