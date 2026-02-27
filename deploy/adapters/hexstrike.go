package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HexstrikeBackend translates campaign requests to HexStrike-AI's MCP server.
// HexStrike v2 exposes 42 security tools via a Go gateway + OCaml MCP server:
//   - POST /mcp       — JSON-RPC MCP endpoint (tools/call, tools/list)
//   - GET  /health    — gateway health check
//   - GET  /metrics   — Prometheus metrics
//
// The gateway wraps the F*-verified OCaml MCP binary (hexstrike-mcp) and
// provides policy enforcement, credential brokering, and Aperture metering.
type HexstrikeBackend struct {
	agentURL   string
	httpClient *http.Client
}

func NewHexstrikeBackend(agentURL string) *HexstrikeBackend {
	return &HexstrikeBackend{
		agentURL: agentURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Minute,
		},
	}
}

func (b *HexstrikeBackend) Type() string { return "hexstrike-ai" }

func (b *HexstrikeBackend) Health() error {
	resp, err := b.httpClient.Get(b.agentURL + "/health")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("hexstrike health: %d", resp.StatusCode)
	}
	return nil
}

// Dispatch sends a campaign to HexStrike via its MCP server.
// For each tool in the campaign, it sends a tools/call JSON-RPC request
// to POST /mcp on the HexStrike gateway.
func (b *HexstrikeBackend) Dispatch(campaign json.RawMessage, runID string) (*LastResult, error) {
	var c struct {
		ID      string   `json:"id"`
		Name    string   `json:"name"`
		Process []string `json:"process"`
		Tools   []string `json:"tools"`
		Targets []struct {
			Org  string `json:"org"`
			Repo string `json:"repo"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(campaign, &c); err != nil {
		return nil, fmt.Errorf("parse campaign: %w", err)
	}

	var trace []ToolTrace
	var lastErr string
	var accumulatedOutput strings.Builder

	// Build target list for tool arguments.
	var targetRepos []string
	for _, t := range c.Targets {
		targetRepos = append(targetRepos, fmt.Sprintf("%s/%s", t.Org, t.Repo))
	}

	// For each tool in the campaign, dispatch via MCP.
	for _, toolName := range c.Tools {
		ts := time.Now().UTC().Format(time.RFC3339)

		// Skip gateway/Chapel tools — those are for rj-gateway, not HexStrike.
		if strings.HasPrefix(toolName, "juggler_") || strings.HasPrefix(toolName, "github_") {
			trace = append(trace, ToolTrace{
				Timestamp: ts,
				Tool:      toolName,
				Summary:   "skipped (gateway tool)",
			})
			continue
		}

		// Build MCP tool arguments.
		// OCaml tools expect "target" (singular, string) not "targets" (plural, array).
		args := map[string]any{}
		if len(targetRepos) > 0 {
			args["target"] = targetRepos[0]
		}

		result, err := b.callMCPTool(toolName, args)
		if err != nil {
			trace = append(trace, ToolTrace{
				Timestamp: ts,
				Tool:      toolName,
				Summary:   fmt.Sprintf("error: %v", err),
				IsError:   true,
			})
			lastErr = err.Error()
			continue
		}

		summary := "ok"
		if result != "" {
			summary = truncate(result, 200)
			accumulatedOutput.WriteString(result)
			accumulatedOutput.WriteString("\n")
		}

		trace = append(trace, ToolTrace{
			Timestamp: ts,
			Tool:      toolName,
			Summary:   summary,
		})
	}

	status := "success"
	if lastErr != "" {
		status = "failure"
	}

	findings := extractFindings(accumulatedOutput.String(), c.ID, runID)

	return &LastResult{
		Status:    status,
		ToolCalls: len(trace),
		ToolTrace: trace,
		Findings:  findings,
		Error:     lastErr,
	}, nil
}

// callMCPTool sends a tools/call JSON-RPC request to the HexStrike MCP server.
func (b *HexstrikeBackend) callMCPTool(toolName string, args map[string]any) (string, error) {
	// Build MCP JSON-RPC request.
	payload := map[string]any{
		"method": "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": args,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, b.agentURL+"/mcp", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Identify as the HexStrike agent for the policy engine.
	// The gateway uses Tailscale-User-Login header for caller identity.
	// We use the agent identity (not campaign-runner) because the adapter
	// is a sidecar in the same pod — it acts on behalf of the agent.
	req.Header.Set("Tailscale-User-Login", "hexstrike-ai-agent@fuzzy-dev")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("hexstrike mcp: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("hexstrike returned %d: %s", resp.StatusCode, truncate(string(respBody), 500))
	}

	// Parse MCP response: {"result": ..., "error": "..."}
	var mcpResp struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err := json.Unmarshal(respBody, &mcpResp); err != nil {
		// Not JSON — return raw output.
		return string(respBody), nil
	}

	if mcpResp.Error != "" {
		return "", fmt.Errorf("hexstrike error: %s", mcpResp.Error)
	}

	// Extract text content from MCP result.
	// MCP tools/call returns: {"content": [{"type": "text", "text": "..."}]}
	var toolResult struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(mcpResp.Result, &toolResult); err != nil {
		// Not structured MCP result — return raw JSON.
		return string(mcpResp.Result), nil
	}

	var texts []string
	for _, c := range toolResult.Content {
		if c.Type == "text" && c.Text != "" {
			texts = append(texts, c.Text)
		}
	}
	if len(texts) > 0 {
		return strings.Join(texts, "\n"), nil
	}

	return string(mcpResp.Result), nil
}
