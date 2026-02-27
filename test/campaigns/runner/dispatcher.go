package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync/atomic"
	"time"
)

// Dispatcher sends MCP tool calls to agents via rj-gateway.
type Dispatcher struct {
	gatewayURL     string
	ironclawURL    string
	picoclawURL    string
	hexstrikeAIURL string
	httpClient     *http.Client
}

// DispatchResult captures the outcome of dispatching a campaign to an agent.
type DispatchResult struct {
	ToolCalls int
	KPIs      map[string]any
	ToolTrace []ToolTraceEntry
	Findings  []Finding
	Error     string
}

// NewDispatcher creates a Dispatcher targeting the given rj-gateway URL.
// Agent URLs point to adapter sidecars that bridge campaign protocol to native APIs.
func NewDispatcher(gatewayURL, ironclawURL, picoclawURL, hexstrikeAIURL string) *Dispatcher {
	return &Dispatcher{
		gatewayURL:     gatewayURL,
		ironclawURL:    ironclawURL,
		picoclawURL:    picoclawURL,
		hexstrikeAIURL: hexstrikeAIURL,
		httpClient: &http.Client{
			Timeout: 2 * time.Minute,
		},
	}
}

// Dispatch executes a campaign by routing to the appropriate agent.
// Agent names map to adapter sidecar URLs:
//   - "ironclaw"/"openclaw" → IronClaw adapter (same pod, localhost:8080)
//   - "picoclaw"            → PicoClaw adapter (K8s Service)
//   - "hexstrike-ai"/"hexstrike" → HexStrike-AI adapter (K8s Service)
//   - "gateway-direct"/default → direct MCP tool calls via rj-gateway
func (d *Dispatcher) Dispatch(ctx context.Context, campaign *Campaign, runID string) (*DispatchResult, error) {
	switch campaign.Agent {
	case "ironclaw", "openclaw":
		url := d.ironclawURL
		if url == "" {
			url = "http://localhost:8080" // adapter sidecar in same pod
		}
		return d.dispatchToAgent(ctx, campaign, runID, url)
	case "picoclaw":
		if d.picoclawURL == "" {
			return &DispatchResult{Error: "picoclaw agent URL not configured"}, nil
		}
		if err := d.checkAgentHealth(ctx, d.picoclawURL); err != nil {
			return &DispatchResult{Error: fmt.Sprintf("picoclaw agent unavailable: %v", err)}, nil
		}
		return d.dispatchToAgent(ctx, campaign, runID, d.picoclawURL)
	case "hexstrike-ai", "hexstrike":
		if d.hexstrikeAIURL == "" {
			return &DispatchResult{Error: "hexstrike-ai agent URL not configured"}, nil
		}
		if err := d.checkAgentHealth(ctx, d.hexstrikeAIURL); err != nil {
			return &DispatchResult{Error: fmt.Sprintf("hexstrike-ai agent unavailable: %v", err)}, nil
		}
		return d.dispatchToAgent(ctx, campaign, runID, d.hexstrikeAIURL)
	default:
		return d.dispatchDirect(ctx, campaign, runID)
	}
}

// checkAgentHealth verifies an agent is reachable and healthy before dispatching.
func (d *Dispatcher) checkAgentHealth(ctx context.Context, agentURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, agentURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health returned %d", resp.StatusCode)
	}
	return nil
}

// dispatchToAgent sends a campaign to an agent container for AI-powered execution.
// The agent runs asynchronously; we poll for completion.
func (d *Dispatcher) dispatchToAgent(ctx context.Context, campaign *Campaign, runID string, agentURL string) (*DispatchResult, error) {
	payload := map[string]any{
		"campaign": campaign,
		"run_id":   runID,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal campaign: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, agentURL+"/campaign", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dispatch to agent: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("agent returned %d: %s", resp.StatusCode, string(respBody))
	}

	log.Printf("campaign %s: dispatched to agent at %s", campaign.ID, agentURL)

	// Poll agent /status for completion.
	return d.pollAgentStatus(ctx, campaign, agentURL)
}

// pollAgentStatus polls the agent's /status endpoint until the campaign completes or context expires.
func (d *Dispatcher) pollAgentStatus(ctx context.Context, campaign *Campaign, agentURL string) (*DispatchResult, error) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return &DispatchResult{Error: "context expired while waiting for agent"}, nil
		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, agentURL+"/status", nil)
			if err != nil {
				continue
			}
			resp, err := d.httpClient.Do(req)
			if err != nil {
				log.Printf("campaign %s: agent status poll error: %v", campaign.ID, err)
				continue
			}

			var status struct {
				Status     string `json:"status"`
				LastResult *struct {
					Status    string           `json:"status"`
					ToolCalls int              `json:"tool_calls"`
					KPIs      map[string]any   `json:"kpis"`
					ToolTrace []ToolTraceEntry `json:"tool_trace"`
					Findings  []Finding        `json:"findings"`
					Error     string           `json:"error"`
				} `json:"last_result"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
				resp.Body.Close()
				continue
			}
			resp.Body.Close()

			if status.Status == "running" {
				continue
			}

			// Agent finished.
			result := &DispatchResult{KPIs: make(map[string]any)}
			if status.LastResult != nil {
				result.ToolCalls = status.LastResult.ToolCalls
				result.KPIs = status.LastResult.KPIs
				result.ToolTrace = status.LastResult.ToolTrace
				result.Findings = status.LastResult.Findings
				result.Error = status.LastResult.Error
			}
			return result, nil
		}
	}
}

// dispatchDirect fires campaign tools sequentially via rj-gateway MCP.
// Used for "gateway-direct" and other non-container agents.
func (d *Dispatcher) dispatchDirect(ctx context.Context, campaign *Campaign, runID string) (*DispatchResult, error) {
	result := &DispatchResult{
		KPIs: make(map[string]any),
	}

	var toolCalls atomic.Int32

	// Execute each tool in the campaign's process via MCP.
	for _, toolName := range campaign.Tools {
		if ctx.Err() != nil {
			result.Error = fmt.Sprintf("context cancelled after %d tool calls", toolCalls.Load())
			break
		}

		resp, err := d.callTool(ctx, toolName, map[string]any{
			"_campaign_id": campaign.ID,
			"_run_id":      runID,
		})
		toolCalls.Add(1)

		if err != nil {
			log.Printf("campaign %s: tool %s error: %v", campaign.ID, toolName, err)
			// Continue with remaining tools unless context is done.
			continue
		}

		log.Printf("campaign %s: tool %s completed (response=%d bytes)",
			campaign.ID, toolName, len(resp))
	}

	result.ToolCalls = int(toolCalls.Load())
	return result, nil
}

// mcpRequest is a JSON-RPC 2.0 request for MCP tool calls.
type mcpRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  mcpToolCall `json:"params"`
}

// mcpToolCall wraps a tool name and its arguments.
type mcpToolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// mcpResponse is a JSON-RPC 2.0 response from rj-gateway.
type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

// mcpError represents a JSON-RPC error.
type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// callTool sends a single MCP tools/call request to rj-gateway.
func (d *Dispatcher) callTool(ctx context.Context, toolName string, args map[string]any) (json.RawMessage, error) {
	req := mcpRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mcpToolCall{
			Name:      toolName,
			Arguments: args,
		},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, d.gatewayURL+"/mcp", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Agent-Identity", "campaign-runner")

	resp, err := d.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gateway returned %d: %s", resp.StatusCode, string(respBody))
	}

	var mcpResp mcpResponse
	if err := json.Unmarshal(respBody, &mcpResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if mcpResp.Error != nil {
		return nil, fmt.Errorf("MCP error %d: %s", mcpResp.Error.Code, mcpResp.Error.Message)
	}

	return mcpResp.Result, nil
}
