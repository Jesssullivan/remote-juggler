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

// HexstrikeBackend translates campaign requests to HexStrike-AI's Flask REST API.
// HexStrike exposes security tools via Flask on port 8888 with endpoints:
//   - GET  /health                                   — comprehensive tool availability check
//   - POST /api/command                              — execute arbitrary security commands
//   - POST /api/intelligence/smart-scan              — AI-driven scan with tool selection
//   - POST /api/error-handling/execute-with-recovery — command execution with retry/recovery
//   - POST /api/intelligence/analyze-target          — target profiling
//   - POST /api/files/*                              — file operations
//
// Note: HexStrike also has a FastMCP server (hexstrike_mcp.py) that exposes
// tools via stdio transport, but the adapter uses the Flask REST API directly
// since it's HTTP-native and doesn't require a stdio bridge.
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

// Dispatch sends a campaign to HexStrike via its Flask REST API.
// For campaigns with targets, it uses /api/intelligence/smart-scan for
// AI-driven tool selection and parallel execution. For campaigns that
// specify individual MCP tools, it maps them to /api/command calls.
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

	// For each tool in the campaign, dispatch via the appropriate REST endpoint.
	for _, toolName := range c.Tools {
		ts := time.Now().UTC().Format(time.RFC3339)

		// Map MCP-style tool names to HexStrike REST API calls.
		result, err := b.dispatchTool(toolName, c.ID, runID, c.Targets)
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
		if s, ok := result["stdout"].(string); ok && s != "" {
			summary = truncate(s, 200)
			accumulatedOutput.WriteString(s)
			accumulatedOutput.WriteString("\n")
		} else if s, ok := result["status"].(string); ok {
			summary = s
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

// dispatchTool routes a tool name to the appropriate HexStrike REST endpoint.
func (b *HexstrikeBackend) dispatchTool(toolName, campaignID, runID string, targets []struct {
	Org  string `json:"org"`
	Repo string `json:"repo"`
}) (map[string]any, error) {
	// Build target list for context.
	var targetRepos []string
	for _, t := range targets {
		targetRepos = append(targetRepos, fmt.Sprintf("%s/%s", t.Org, t.Repo))
	}
	targetStr := strings.Join(targetRepos, ", ")

	// Route tool to appropriate endpoint.
	switch {
	case strings.HasPrefix(toolName, "juggler_") || strings.HasPrefix(toolName, "github_"):
		// These are rj-gateway or Chapel subprocess tools, not HexStrike tools.
		// HexStrike's native commands (credential_scan, tls_check, port_scan, etc.)
		// never use these prefixes. Return a no-op success so the trace records intent.
		return map[string]any{
			"status": "skipped (gateway tool)",
		}, nil

	default:
		// Execute as a command via /api/command.
		command := toolName
		if targetStr != "" {
			command = fmt.Sprintf("%s --target %s", toolName, targetStr)
		}
		return b.execCommand(command)
	}
}

// execCommand calls POST /api/command on HexStrike's Flask API.
func (b *HexstrikeBackend) execCommand(command string) (map[string]any, error) {
	payload := map[string]any{
		"command":   command,
		"use_cache": true,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, b.agentURL+"/api/command", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hexstrike command: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hexstrike returned %d: %s", resp.StatusCode, truncate(string(respBody), 500))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return map[string]any{"stdout": string(respBody)}, nil
	}

	if errMsg, ok := result["error"].(string); ok && errMsg != "" {
		return nil, fmt.Errorf("hexstrike error: %s", errMsg)
	}

	return result, nil
}
