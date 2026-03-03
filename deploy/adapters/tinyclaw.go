package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TinyclawBackend translates campaign requests to TinyClaw's HTTP dispatch API.
// TinyClaw exposes POST /api/dispatch for agent task execution,
// GET /api/tools for tool listing, and GET /api/status for health/status info.
//
// When apiKey is set and a campaign declares tools, the backend bypasses PicoClaw
// and runs an agentic dispatch loop: calling the Anthropic Messages API directly
// with gateway tools, executing tool calls via rj-gateway MCP.
type TinyclawBackend struct {
	agentURL   string
	gatewayURL string
	skillsDir  string // Path to workspace/skills/ directory (optional)
	httpClient *http.Client

	// Anthropic API config for agentic dispatch (bypasses PicoClaw).
	apiKey            string
	apiBaseURL        string
	model             string
	maxToolIterations int
}

func NewTinyclawBackend(agentURL, gatewayURL string) *TinyclawBackend {
	return &TinyclawBackend{
		agentURL:   agentURL,
		gatewayURL: gatewayURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Minute, // TinyClaw has 10m write deadline on dispatch
		},
	}
}

// SetSkillsDir sets the path to the workspace skills directory.
// SKILL.md files under this directory are loaded and injected into campaign prompts.
func (b *TinyclawBackend) SetSkillsDir(dir string) {
	b.skillsDir = dir
}

// SetAPIKey configures the Anthropic API key for agentic dispatch.
// When set, campaigns with tools bypass PicoClaw and use the Anthropic Messages API directly.
func (b *TinyclawBackend) SetAPIKey(key string) {
	b.apiKey = key
}

// SetAPIBaseURL sets the Anthropic API base URL (e.g., Aperture proxy).
func (b *TinyclawBackend) SetAPIBaseURL(url string) {
	b.apiBaseURL = url
}

// SetModel sets the model ID for agentic dispatch (default: claude-sonnet-4-20250514).
func (b *TinyclawBackend) SetModel(model string) {
	b.model = model
}

// loadSkills reads all SKILL.md files from the skills directory and returns
// their content concatenated as context for the LLM prompt.
func (b *TinyclawBackend) loadSkills() string {
	if b.skillsDir == "" {
		return ""
	}

	entries, err := os.ReadDir(b.skillsDir)
	if err != nil {
		log.Printf("skills: cannot read %s: %v", b.skillsDir, err)
		return ""
	}

	var parts []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillFile := filepath.Join(b.skillsDir, e.Name(), "SKILL.md")
		data, err := os.ReadFile(skillFile)
		if err != nil {
			continue
		}
		content := strings.TrimSpace(string(data))
		if content != "" {
			parts = append(parts, content)
		}
	}

	if len(parts) == 0 {
		return ""
	}

	log.Printf("skills: loaded %d skill(s) from %s", len(parts), b.skillsDir)
	return "\n\n## Skills Reference\n\n" + strings.Join(parts, "\n\n---\n\n")
}

func (b *TinyclawBackend) Type() string { return "tinyclaw" }

func (b *TinyclawBackend) Health() error {
	resp, err := b.httpClient.Get(b.agentURL + "/api/status")
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return nil
		}
	}
	// Fall back to legacy /health endpoint.
	resp, err = b.httpClient.Get(b.agentURL + "/health")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tinyclaw health: %d", resp.StatusCode)
	}
	return nil
}

// Dispatch sends a campaign to TinyClaw via POST /api/dispatch.
// If an Anthropic API key is configured, dispatch uses the agentic loop
// (direct Anthropic API + gateway tool execution with ALL available tools).
// Otherwise, falls through to PicoClaw's text-only dispatch.
func (b *TinyclawBackend) Dispatch(campaign json.RawMessage, runID string) (*LastResult, error) {
	// Agentic dispatch: API key is the only requirement.
	// All gateway tools are available to the agent (no filtering).
	if b.apiKey != "" {
		log.Printf("tinyclaw: agentic dispatch (model=%s)", b.effectiveModel())
		return b.dispatchAgentic(campaign, runID)
	}

	var c struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Process     []string `json:"process"`
		Tools       []string `json:"tools"`
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
		MemoryContext string `json:"_memory_context,omitempty"`
		SkillsContext string `json:"_skills_context,omitempty"`
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

	pb.WriteString("## Process\n")
	for i, step := range c.Process {
		pb.WriteString(fmt.Sprintf("%d. %s\n", i+1, step))
	}
	pb.WriteString("\n")

	if len(c.Tools) > 0 {
		pb.WriteString("## Available Tools\n")
		for _, tool := range c.Tools {
			pb.WriteString(fmt.Sprintf("- `%s`\n", tool))
		}
		pb.WriteString("\n")
	}

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

	if c.Metrics.SuccessCriteria != "" {
		pb.WriteString(fmt.Sprintf("## Success Criteria\n%s\n\n", c.Metrics.SuccessCriteria))
	}

	// Inject workspace skills as reference context.
	pb.WriteString(b.loadSkills())

	// Dynamic skill library from previous runs (Voyager pattern).
	if c.SkillsContext != "" {
		pb.WriteString("\n\n## Your Skill Library\n")
		pb.WriteString("The following skills were learned from previous runs. Check if any are relevant to this task and follow them if so.\n\n")
		pb.WriteString(c.SkillsContext)
		pb.WriteString("\n\n")
	}

	// Persistent memory from previous runs (Reflexion pattern).
	if c.MemoryContext != "" {
		pb.WriteString("\n\n## Notes from Previous Runs\n")
		pb.WriteString("The following are your notes from previous campaign runs. Use these to avoid repeating mistakes and build on what worked.\n\n")
		pb.WriteString(c.MemoryContext)
		pb.WriteString("\n\n")
	}

	pb.WriteString(findingsInstruction)
	prompt := pb.String()

	// TinyClaw dispatch API.
	payload := map[string]string{
		"content":     prompt,
		"session_key": runID,
		"channel":     "campaign",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, b.agentURL+"/api/dispatch", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tinyclaw dispatch: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return &LastResult{
			Status: "failure",
			Error:  fmt.Sprintf("tinyclaw returned %d: %s", resp.StatusCode, string(respBody)),
		}, nil
	}

	// Parse TinyClaw dispatch response.
	var dispatchResp struct {
		Content      string `json:"content"`
		FinishReason string `json:"finish_reason"`
		Error        string `json:"error"`
	}
	if err := json.Unmarshal(respBody, &dispatchResp); err != nil {
		return &LastResult{
			Status:    "success",
			ToolCalls: 0,
		}, nil
	}

	if dispatchResp.FinishReason == "error" || dispatchResp.Error != "" {
		return &LastResult{
			Status: "failure",
			Error:  dispatchResp.Error,
		}, nil
	}

	findings := extractFindings(dispatchResp.Content, c.ID, runID)
	reflection := extractReflection(dispatchResp.Content)
	skill := extractSkill(dispatchResp.Content)

	// Estimate tool calls from response content.
	// TinyClaw's dispatch response doesn't report tool calls directly,
	// but the agent output often contains tool invocation evidence.
	toolCalls := countToolReferences(dispatchResp.Content, c.Tools)

	return &LastResult{
		Status:     "success",
		ToolCalls:  toolCalls,
		Findings:   findings,
		Reflection: reflection,
		Skill:      skill,
	}, nil
}

// effectiveModel returns the configured model or the default.
func (b *TinyclawBackend) effectiveModel() string {
	if b.model != "" {
		return b.model
	}
	return "claude-sonnet-4-20250514"
}

// effectiveMaxIterations returns the configured max iterations or the default.
func (b *TinyclawBackend) effectiveMaxIterations() int {
	if b.maxToolIterations > 0 {
		return b.maxToolIterations
	}
	return 25
}

// --- Anthropic Messages API types ---

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []AnthropicTool    `json:"tools,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []anthropicContentBlock
}

type anthropicContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// For tool_result blocks.
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
}

type anthropicResponse struct {
	ID         string                  `json:"id"`
	Type       string                  `json:"type"`
	Role       string                  `json:"role"`
	Content    []anthropicContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// dispatchAgentic runs a campaign via the Anthropic Messages API with gateway tool execution.
func (b *TinyclawBackend) dispatchAgentic(campaign json.RawMessage, runID string) (*LastResult, error) {
	var c struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Process     []string `json:"process"`
		Tools       []string `json:"tools"`
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
		MemoryContext string `json:"_memory_context,omitempty"`
		SkillsContext string `json:"_skills_context,omitempty"`
	}
	if err := json.Unmarshal(campaign, &c); err != nil {
		return nil, fmt.Errorf("parse campaign: %w", err)
	}

	// Build system prompt with agent identity and capabilities.
	var pb strings.Builder
	pb.WriteString("# You are TinyClaw\n")
	pb.WriteString("An autonomous agent that maintains the RemoteJuggler project.\n")
	pb.WriteString("RemoteJuggler is a git identity management tool — it helps developers ")
	pb.WriteString("manage multiple git identities (SSH keys, GPG signing, provider credentials) ")
	pb.WriteString("across GitHub, GitLab, and Bitbucket.\n\n")
	pb.WriteString("## Your Capabilities\n")
	pb.WriteString("- **Web Research**: Search the web (`web_search`) and fetch documentation/papers/changelogs (`web_fetch`)\n")
	pb.WriteString("- **GitHub Operations**: Read files, create branches, patch files, create PRs and issues, manage discussions\n")
	pb.WriteString("- **Identity Management**: Use RemoteJuggler's own tools (e.g. `juggler_resolve_composite`, `juggler_audit_log`)\n")
	pb.WriteString("- **Secret Management**: Read/write to Setec secret store (`juggler_setec_get/put/list`)\n")
	pb.WriteString("- **Campaign Orchestration**: Trigger and monitor other agent campaigns\n\n")
	pb.WriteString("## Attribution\n")
	pb.WriteString("You are rj-agent-bot (GitHub App ID 2945224). Your commits and PRs are attributed to you.\n\n")

	pb.WriteString(fmt.Sprintf("## Current Task: %s\n", c.Name))
	pb.WriteString(fmt.Sprintf("**Run ID**: %s\n", runID))
	pb.WriteString("\n")

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

	pb.WriteString(b.loadSkills())

	if c.SkillsContext != "" {
		pb.WriteString("\n\n## Your Skill Library\n")
		pb.WriteString("The following skills were learned from previous runs. Check if any are relevant.\n\n")
		pb.WriteString(c.SkillsContext)
		pb.WriteString("\n\n")
	}

	if c.MemoryContext != "" {
		pb.WriteString("\n\n## Notes from Previous Runs\n")
		pb.WriteString(c.MemoryContext)
		pb.WriteString("\n\n")
	}

	systemPrompt := pb.String()

	// Build user message: goal-based, not step-scripted.
	var userMsg strings.Builder
	if c.Description != "" {
		userMsg.WriteString("## Task\n")
		userMsg.WriteString(c.Description)
		userMsg.WriteString("\n\n")
	}
	if len(c.Process) > 0 {
		userMsg.WriteString("## Context & Guidance\n")
		for _, hint := range c.Process {
			userMsg.WriteString("- " + hint + "\n")
		}
		userMsg.WriteString("\n")
	}
	userMsg.WriteString("Use whatever tools you need to accomplish this task. ")
	userMsg.WriteString("You have access to web search, GitHub operations, identity management, and secret storage.\n")
	if c.Metrics.SuccessCriteria != "" {
		userMsg.WriteString(fmt.Sprintf("\n## Success Criteria\n%s\n", c.Metrics.SuccessCriteria))
	}
	userMsg.WriteString(findingsInstruction)

	// Fetch ALL gateway tools in Anthropic format (no filtering).
	// The agent decides which tools to use based on the task context.
	tools, err := fetchGatewayToolsAnthropic(b.httpClient, b.gatewayURL, nil)
	if err != nil {
		log.Printf("tinyclaw agentic: failed to fetch gateway tools: %v", err)
		// Proceed without tools — the LLM will respond with text only.
		tools = nil
	} else {
		log.Printf("tinyclaw agentic: loaded %d tools from gateway (all available)", len(tools))
	}

	// Select model: campaign override > adapter config > default.
	model := b.effectiveModel()
	if c.Model != "" {
		model = c.Model
	}

	// Initialize messages.
	messages := []anthropicMessage{
		{Role: "user", Content: userMsg.String()},
	}

	var allText strings.Builder
	var toolTrace []ToolTrace
	totalToolCalls := 0
	totalTokens := 0

	// Agentic loop.
	maxIter := b.effectiveMaxIterations()
	for iter := 0; iter < maxIter; iter++ {
		apiReq := anthropicRequest{
			Model:     model,
			MaxTokens: 4096,
			System:    systemPrompt,
			Messages:  messages,
			Tools:     tools,
		}

		result, err := b.callAnthropicAPI(apiReq)
		if err != nil {
			return &LastResult{
				Status:    "error",
				Error:     fmt.Sprintf("anthropic API: %v", err),
				ToolCalls: totalToolCalls,
				ToolTrace: toolTrace,
			}, nil
		}

		totalTokens += result.Usage.InputTokens + result.Usage.OutputTokens

		// Process response content blocks.
		var toolUseBlocks []anthropicContentBlock
		var responseBlocks []anthropicContentBlock

		for _, block := range result.Content {
			responseBlocks = append(responseBlocks, block)
			switch block.Type {
			case "text":
				allText.WriteString(block.Text)
				allText.WriteString("\n")
			case "tool_use":
				toolUseBlocks = append(toolUseBlocks, block)
			}
		}

		// Append assistant response to messages (must include all content blocks).
		messages = append(messages, anthropicMessage{
			Role:    "assistant",
			Content: responseBlocks,
		})

		// If no tool calls or stop_reason is end_turn, we're done.
		if len(toolUseBlocks) == 0 || result.StopReason == "end_turn" {
			break
		}

		// Execute tool calls and build tool_result messages.
		var toolResults []anthropicContentBlock
		for _, tu := range toolUseBlocks {
			totalToolCalls++
			log.Printf("tinyclaw agentic: tool_use %s (id=%s)", tu.Name, tu.ID)

			toolTrace = append(toolTrace, ToolTrace{
				Timestamp: time.Now().UTC().Format(time.RFC3339),
				Tool:      tu.Name,
				Summary:   fmt.Sprintf("Called %s", tu.Name),
			})

			// Parse tool input.
			var input map[string]any
			if len(tu.Input) > 0 {
				json.Unmarshal(tu.Input, &input)
			}

			// Execute via gateway MCP.
			toolResult, err := b.callGatewayTool(tu.Name, input)
			if err != nil {
				log.Printf("tinyclaw agentic: tool %s error: %v", tu.Name, err)
				toolTrace[len(toolTrace)-1].IsError = true
				toolTrace[len(toolTrace)-1].Summary = fmt.Sprintf("Error: %v", err)
				toolResults = append(toolResults, anthropicContentBlock{
					Type:      "tool_result",
					ToolUseID: tu.ID,
					Content:   fmt.Sprintf("Error: %v", err),
				})
			} else {
				toolResults = append(toolResults, anthropicContentBlock{
					Type:      "tool_result",
					ToolUseID: tu.ID,
					Content:   toolResult,
				})
			}
		}

		// Append tool results as user message.
		messages = append(messages, anthropicMessage{
			Role:    "user",
			Content: toolResults,
		})
	}

	// Extract structured output from accumulated text.
	text := allText.String()
	findings := extractFindings(text, c.ID, runID)
	reflection := extractReflection(text)
	skill := extractSkill(text)

	status := "success"
	if totalToolCalls == 0 && len(findings) > 0 {
		status = "failure" // Confabulation guard: findings without tool calls.
	}

	return &LastResult{
		Status:     status,
		ToolCalls:  totalToolCalls,
		Findings:   findings,
		Reflection: reflection,
		Skill:      skill,
		ToolTrace:  toolTrace,
		KPIs: map[string]any{
			"tokens_used":    totalTokens,
			"tool_calls":     totalToolCalls,
			"iterations":     len(messages) / 2,
			"findings_count": len(findings),
		},
	}, nil
}

// callAnthropicAPI sends a request to the Anthropic Messages API.
func (b *TinyclawBackend) callAnthropicAPI(apiReq anthropicRequest) (*anthropicResponse, error) {
	body, err := json.Marshal(apiReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	baseURL := b.apiBaseURL
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}

	req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", b.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, truncate(string(respBody), 500))
	}

	var apiResp anthropicResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if apiResp.Error != nil {
		return nil, fmt.Errorf("API error (%s): %s", apiResp.Error.Type, apiResp.Error.Message)
	}

	return &apiResp, nil
}

// callGatewayTool executes a single tool call via rj-gateway MCP (JSON-RPC 2.0).
func (b *TinyclawBackend) callGatewayTool(toolName string, args map[string]any) (string, error) {
	mcpReq := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Params  struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments,omitempty"`
		} `json:"params"`
	}{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
	}
	mcpReq.Params.Name = toolName
	mcpReq.Params.Arguments = args

	body, err := json.Marshal(mcpReq)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, b.gatewayURL+"/mcp", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Identity", "tinyclaw")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gateway returned %d: %s", resp.StatusCode, truncate(string(respBody), 500))
	}

	// Parse MCP response and extract text content.
	var mcpResp struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(respBody, &mcpResp); err != nil {
		return string(respBody), nil // Return raw if can't parse.
	}

	if mcpResp.Error != nil {
		return "", fmt.Errorf("MCP error %d: %s", mcpResp.Error.Code, mcpResp.Error.Message)
	}

	// Concatenate text content blocks.
	var texts []string
	for _, c := range mcpResp.Result.Content {
		if c.Text != "" {
			texts = append(texts, c.Text)
		}
	}
	if len(texts) > 0 {
		return strings.Join(texts, "\n"), nil
	}

	return string(respBody), nil
}

// countToolReferences estimates the number of tool invocations by scanning
// agent output for tool name references. This is a heuristic — TinyClaw's
// dispatch API doesn't return structured tool call data.
func countToolReferences(content string, campaignTools []string) int {
	count := 0
	lower := strings.ToLower(content)
	for _, tool := range campaignTools {
		if strings.Contains(lower, strings.ToLower(tool)) {
			count++
		}
	}
	return count
}
