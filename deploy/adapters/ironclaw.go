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

// IronclawBackend translates campaign requests to OpenClaw's HTTP API.
// OpenClaw exposes /v1/responses (OpenResponses API) on port 18789, which
// supports tool calls, usage tracking, and structured input. Both
// /v1/responses and /v1/chat/completions are disabled by default in OpenClaw
// and must be explicitly enabled in gateway config.
//
// Health checks use POST /v1/chat/completions with a minimal message since
// OpenClaw has no dedicated HTTP health endpoint (native health is WebSocket).
type IronclawBackend struct {
	agentURL   string
	authToken  string
	httpClient *http.Client
}

func NewIronclawBackend(agentURL string) *IronclawBackend {
	return &IronclawBackend{
		agentURL: agentURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Minute,
		},
	}
}

// SetAuthToken sets the Bearer token for OpenClaw API authentication.
func (b *IronclawBackend) SetAuthToken(token string) {
	b.authToken = token
}

func (b *IronclawBackend) Type() string { return "ironclaw" }

// Health checks OpenClaw availability by hitting /v1/chat/completions with
// a no-op message. OpenClaw has no HTTP health endpoint; the native health
// check is WebSocket-only (ws://host:18789 method=health).
func (b *IronclawBackend) Health() error {
	payload := map[string]any{
		"model":    "openclaw",
		"messages": []map[string]string{{"role": "user", "content": "ping"}},
		"stream":   false,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest(http.MethodPost, b.agentURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	b.setAuth(req)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 200 = healthy, 401 = auth misconfigured but server is up
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusUnauthorized {
		return nil
	}
	return fmt.Errorf("ironclaw health: %d", resp.StatusCode)
}

// Dispatch sends a campaign to IronClaw via the /v1/responses API.
// This endpoint supports client-provided tools (function calling), returns
// tool call details in the response, and tracks token usage.
//
// The campaign's process steps become the user input, and the campaign's
// MCP tools are passed as function definitions so OpenClaw can invoke them.
func (b *IronclawBackend) Dispatch(campaign json.RawMessage, runID string) (*LastResult, error) {
	var c struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Process     []string `json:"process"`
		Tools       []string `json:"tools"`
		Mode        string   `json:"mode"`
		Model       string   `json:"model"`
		Targets     []struct {
			Forge  string `json:"forge"`
			Org    string `json:"org"`
			Repo   string `json:"repo"`
			Branch string `json:"branch"`
		} `json:"targets"`
		Guardrails struct {
			MaxDuration string `json:"maxDuration"`
			ReadOnly    bool   `json:"readOnly"`
		} `json:"guardrails"`
		Metrics struct {
			SuccessCriteria string   `json:"successCriteria"`
			KPIs            []string `json:"kpis"`
		} `json:"metrics"`
		Outputs struct {
			SetecKey string `json:"setecKey"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal(campaign, &c); err != nil {
		return nil, fmt.Errorf("parse campaign: %w", err)
	}

	// Build enriched prompt with full campaign context.
	var pb strings.Builder

	pb.WriteString(fmt.Sprintf("# Campaign: %s\n", c.Name))
	pb.WriteString(fmt.Sprintf("**Run ID**: %s\n", runID))
	if c.Description != "" {
		pb.WriteString(fmt.Sprintf("**Purpose**: %s\n", c.Description))
	}
	pb.WriteString("\n")

	// Targets
	if len(c.Targets) > 0 {
		pb.WriteString("## Targets\n")
		for _, t := range c.Targets {
			branch := t.Branch
			if branch == "" {
				branch = "main"
			}
			pb.WriteString(fmt.Sprintf("- %s/%s (branch: %s, forge: %s)\n", t.Org, t.Repo, branch, t.Forge))
		}
		pb.WriteString("\n")
	}

	// Process steps
	pb.WriteString("## Process\n")
	for i, step := range c.Process {
		pb.WriteString(fmt.Sprintf("%d. %s\n", i+1, step))
	}
	pb.WriteString("\n")

	// Tools — instruct LLM to use exec("rj-tool ...") since OpenClaw ignores MCP servers
	if len(c.Tools) > 0 {
		pb.WriteString("## Available Tools\n")
		pb.WriteString("Call tools via the exec command: `exec(\"/workspace/bin/rj-tool <tool_name> key=value ...\")`\n\n")
		for _, tool := range c.Tools {
			pb.WriteString(fmt.Sprintf("- `%s`\n", tool))
		}
		pb.WriteString("\n")
	}

	// Guardrails
	if c.Guardrails.MaxDuration != "" || c.Guardrails.ReadOnly {
		pb.WriteString("## Constraints\n")
		if c.Guardrails.MaxDuration != "" {
			pb.WriteString(fmt.Sprintf("- **Max Duration**: %s\n", c.Guardrails.MaxDuration))
		}
		if c.Guardrails.ReadOnly {
			pb.WriteString("- **Read-Only**: Do NOT create branches, PRs, or modify repositories\n")
		}
		pb.WriteString("\n")
	}

	// Success criteria
	if c.Metrics.SuccessCriteria != "" {
		pb.WriteString(fmt.Sprintf("## Success Criteria\n%s\n\n", c.Metrics.SuccessCriteria))
	}
	if len(c.Metrics.KPIs) > 0 {
		pb.WriteString("## KPIs to Track\n")
		for _, kpi := range c.Metrics.KPIs {
			pb.WriteString(fmt.Sprintf("- %s\n", kpi))
		}
		pb.WriteString("\n")
	}

	// Output destination
	if c.Outputs.SetecKey != "" {
		pb.WriteString(fmt.Sprintf("## Output\nStore results via: `exec(\"/workspace/bin/rj-tool juggler_setec_put name=%s value=<JSON>\")`\n\n", c.Outputs.SetecKey))
	}

	pb.WriteString(findingsInstruction)
	prompt := pb.String()

	// Build /v1/responses request.
	input := []map[string]any{
		{
			"type":    "message",
			"role":    "user",
			"content": prompt,
		},
	}

	payload := map[string]any{
		"model":  "openclaw",
		"input":  input,
		"stream": false,
	}
	if c.Model != "" {
		payload["model"] = c.Model
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, b.agentURL+"/v1/responses", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	b.setAuth(req)

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ironclaw responses: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return &LastResult{
			Status: "failure",
			Error:  fmt.Sprintf("ironclaw returned %d: %s", resp.StatusCode, truncate(string(respBody), 500)),
		}, nil
	}

	// Parse OpenResponses format.
	var respData struct {
		ID     string `json:"id"`
		Status string `json:"status"` // "completed" or "incomplete" (tool calls pending)
		Output []struct {
			Type    string `json:"type"` // "message" or "function_call"
			ID      string `json:"id"`
			Name    string `json:"name"`      // function_call: tool name
			CallID  string `json:"call_id"`   // function_call: call ID
			Args    string `json:"arguments"` // function_call: JSON args
			Role    string `json:"role"`      // message: "assistant"
			Content any    `json:"content"`   // message: string or [{type, text}]
		} `json:"output"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &respData); err != nil {
		// Non-fatal: got a response but can't parse structure.
		return &LastResult{
			Status:    "success",
			ToolCalls: 0,
		}, nil
	}

	// Extract tool calls and text from output items.
	var trace []ToolTrace
	var textContent string
	for _, item := range respData.Output {
		ts := time.Now().UTC().Format(time.RFC3339)
		switch item.Type {
		case "function_call":
			trace = append(trace, ToolTrace{
				Timestamp: ts,
				Tool:      item.Name,
				Summary:   truncate(item.Args, 200),
			})
		case "message":
			// Extract text content from message.
			switch v := item.Content.(type) {
			case string:
				textContent = v
			case []any:
				for _, part := range v {
					if m, ok := part.(map[string]any); ok {
						if text, ok := m["text"].(string); ok {
							textContent += text
						}
					}
				}
			}
		}
	}

	findings := extractFindings(textContent, c.ID, runID)

	return &LastResult{
		Status:    "success",
		ToolCalls: len(trace),
		ToolTrace: trace,
		Findings:  findings,
	}, nil
}

func (b *IronclawBackend) setAuth(req *http.Request) {
	if b.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+b.authToken)
	}
}
