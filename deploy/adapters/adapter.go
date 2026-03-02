package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// CampaignRequest is the payload sent by the campaign runner to POST /campaign.
type CampaignRequest struct {
	Campaign      json.RawMessage `json:"campaign"`
	RunID         string          `json:"run_id"`
	MemoryContext string          `json:"memory_context,omitempty"`
	SkillsContext string          `json:"skills_context,omitempty"`
}

// CampaignResponse is returned by POST /campaign (202 Accepted).
type CampaignResponse struct {
	Status string `json:"status"`
	RunID  string `json:"run_id"`
}

// StatusResponse is returned by GET /status.
type StatusResponse struct {
	Status     string      `json:"status"` // "idle", "running", "completed", "error"
	LastResult *LastResult `json:"last_result,omitempty"`
}

// Finding represents a single actionable finding from agent output.
// JSON-aligned with feedback.go:Finding in the campaign runner module.
type Finding struct {
	Title       string   `json:"title"`
	Body        string   `json:"body"`
	Severity    string   `json:"severity"` // "critical", "high", "medium", "low"
	Labels      []string `json:"labels"`
	CampaignID  string   `json:"campaign_id"`
	RunID       string   `json:"run_id"`
	Fingerprint string   `json:"fingerprint"` // Dedupe key.
}

// LastResult captures the outcome of the most recent campaign execution.
type LastResult struct {
	Status     string         `json:"status"` // "success", "failure", "error"
	ToolCalls  int            `json:"tool_calls"`
	KPIs       map[string]any `json:"kpis,omitempty"`
	ToolTrace  []ToolTrace    `json:"tool_trace,omitempty"`
	Findings   []Finding      `json:"findings,omitempty"`
	Reflection string         `json:"reflection,omitempty"` // Agent self-critique for persistent memory (Reflexion pattern).
	Skill      string         `json:"skill,omitempty"`      // Agent-proposed skill procedure (Voyager pattern).
	Error      string         `json:"error,omitempty"`
}

// ToolTrace records a single tool invocation.
type ToolTrace struct {
	Timestamp string `json:"timestamp"`
	Tool      string `json:"tool"`
	Summary   string `json:"summary"`
	IsError   bool   `json:"is_error,omitempty"`
}

// HealthResponse is returned by GET /health.
type HealthResponse struct {
	Status    string `json:"status"`
	AgentType string `json:"agent_type"`
	AgentURL  string `json:"agent_url"`
	Scaffold  bool   `json:"scaffold"`
}

// AgentBackend translates campaign requests to a specific agent's native API.
type AgentBackend interface {
	// Dispatch sends a campaign to the agent and blocks until completion.
	Dispatch(campaign json.RawMessage, runID string) (*LastResult, error)
	// Health checks if the upstream agent is reachable.
	Health() error
	// Type returns the agent type identifier.
	Type() string
}

// Adapter is the HTTP server that bridges campaign runner protocol to agent backends.
type Adapter struct {
	backend AgentBackend
	mux     *http.ServeMux

	mu         sync.Mutex
	status     string // "idle", "running", "completed", "error"
	lastResult *LastResult
}

// NewAdapter creates an Adapter for the specified agent type.
func NewAdapter(agentType, agentURL, gatewayURL, authToken, skillsDir, anthropicKey, anthropicBaseURL, anthropicModel string) (*Adapter, error) {
	var backend AgentBackend
	switch agentType {
	case "ironclaw", "openclaw":
		b := NewIronclawBackend(agentURL)
		if authToken != "" {
			b.SetAuthToken(authToken)
		}
		if skillsDir != "" {
			b.SetSkillsDir(skillsDir)
		}
		backend = b
	case "tinyclaw":
		b := NewTinyclawBackend(agentURL, gatewayURL)
		if skillsDir != "" {
			b.SetSkillsDir(skillsDir)
		}
		if anthropicKey != "" {
			b.SetAPIKey(anthropicKey)
		}
		if anthropicBaseURL != "" {
			b.SetAPIBaseURL(anthropicBaseURL)
		}
		if anthropicModel != "" {
			b.SetModel(anthropicModel)
		}
		backend = b
	case "hexstrike-ai", "hexstrike":
		backend = NewHexstrikeBackend(agentURL)
	default:
		return nil, fmt.Errorf("unknown agent type: %s", agentType)
	}

	a := &Adapter{
		backend: backend,
		mux:     http.NewServeMux(),
		status:  "idle",
	}

	a.mux.HandleFunc("/campaign", a.handleCampaign)
	a.mux.HandleFunc("/status", a.handleStatus)
	a.mux.HandleFunc("/health", a.handleHealth)

	return a, nil
}

func (a *Adapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mux.ServeHTTP(w, r)
}

func (a *Adapter) handleCampaign(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CampaignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Inject memory and skills context into campaign JSON so backends can read it.
	if req.MemoryContext != "" || req.SkillsContext != "" {
		var campaignMap map[string]any
		if err := json.Unmarshal(req.Campaign, &campaignMap); err == nil {
			if req.MemoryContext != "" {
				campaignMap["_memory_context"] = req.MemoryContext
			}
			if req.SkillsContext != "" {
				campaignMap["_skills_context"] = req.SkillsContext
			}
			if merged, err := json.Marshal(campaignMap); err == nil {
				req.Campaign = merged
			}
		}
	}

	a.mu.Lock()
	if a.status == "running" {
		a.mu.Unlock()
		http.Error(w, "campaign already running", http.StatusConflict)
		return
	}
	a.status = "running"
	a.lastResult = nil
	a.mu.Unlock()

	// Run campaign in background.
	go func() {
		log.Printf("dispatching campaign (run_id=%s)", req.RunID)
		start := time.Now()

		result, err := a.backend.Dispatch(req.Campaign, req.RunID)
		if err != nil {
			result = &LastResult{
				Status: "error",
				Error:  err.Error(),
			}
		}

		log.Printf("campaign completed (run_id=%s, status=%s, duration=%s)",
			req.RunID, result.Status, time.Since(start).Round(time.Millisecond))

		a.mu.Lock()
		a.status = "completed"
		a.lastResult = result
		a.mu.Unlock()
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(CampaignResponse{
		Status: "accepted",
		RunID:  req.RunID,
	})
}

func (a *Adapter) handleStatus(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	resp := StatusResponse{
		Status:     a.status,
		LastResult: a.lastResult,
	}
	a.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func (a *Adapter) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := HealthResponse{
		Status:    "ok",
		AgentType: a.backend.Type(),
		Scaffold:  false,
	}

	if err := a.backend.Health(); err != nil {
		resp.Status = "degraded"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// findingsInstruction is appended to agent prompts so agents know how to
// emit structured findings, memory reflections, and skill procedures that
// the adapter can extract.
const findingsInstruction = `

When you complete your analysis, if you have actionable findings to report, output them as a JSON block delimited by markers:
__findings__[{"title":"...","body":"...","severity":"critical|high|medium|low","labels":["label1"],"fingerprint":"unique-key"}]__end_findings__
Each finding should have a unique fingerprint for deduplication. Omit the block entirely if there are no findings.

After your analysis, write a brief self-reflection for your future self between these markers:
__memory__
Date: <today>
What I did: <brief summary>
What I learned: <key insights with evidence>
Next time: <what to do differently or explore further>
__end_memory__
This reflection will be saved and provided to you on your next run so you can build on your work.

If you discovered a reusable procedure during this task, document it between these markers:
__skill__
### Skill: <name>
When to use: <trigger condition>
Steps:
1. <step 1>
2. <step 2>
Gotchas: <common failure modes>
__end_skill__
This skill will be added to your skill library and provided on future runs so you can reuse proven procedures.`

// extractReflection parses a __memory__...__end_memory__ delimited block
// from agent text output. Returns the trimmed text between markers, or ""
// if no reflection block is found.
func extractReflection(text string) string {
	const startMarker = "__memory__"
	const endMarker = "__end_memory__"

	startIdx := strings.Index(text, startMarker)
	if startIdx < 0 {
		return ""
	}
	startIdx += len(startMarker)

	endIdx := strings.Index(text[startIdx:], endMarker)
	if endIdx < 0 {
		return ""
	}

	return strings.TrimSpace(text[startIdx : startIdx+endIdx])
}

// extractSkill parses a __skill__...__end_skill__ delimited block
// from agent text output. Returns the trimmed text between markers, or ""
// if no skill block is found.
func extractSkill(text string) string {
	const startMarker = "__skill__"
	const endMarker = "__end_skill__"

	startIdx := strings.Index(text, startMarker)
	if startIdx < 0 {
		return ""
	}
	startIdx += len(startMarker)

	endIdx := strings.Index(text[startIdx:], endMarker)
	if endIdx < 0 {
		return ""
	}

	return strings.TrimSpace(text[startIdx : startIdx+endIdx])
}

// extractFindings parses a __findings__[...]__end_findings__ delimited JSON
// block from agent text output. It stamps campaign_id and run_id onto each
// finding. Returns nil if no findings block is found.
func extractFindings(text, campaignID, runID string) []Finding {
	const startMarker = "__findings__"
	const endMarker = "__end_findings__"

	startIdx := strings.Index(text, startMarker)
	if startIdx < 0 {
		return nil
	}
	startIdx += len(startMarker)

	endIdx := strings.Index(text[startIdx:], endMarker)
	if endIdx < 0 {
		return nil
	}

	jsonBlock := strings.TrimSpace(text[startIdx : startIdx+endIdx])
	if jsonBlock == "" {
		return nil
	}

	var findings []Finding
	if err := json.Unmarshal([]byte(jsonBlock), &findings); err != nil {
		log.Printf("extractFindings: failed to parse findings JSON: %v", err)
		return nil
	}

	for i := range findings {
		findings[i].CampaignID = campaignID
		findings[i].RunID = runID
	}

	return findings
}
