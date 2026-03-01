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
	Campaign json.RawMessage `json:"campaign"`
	RunID    string          `json:"run_id"`
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
	Status    string         `json:"status"` // "success", "failure", "error"
	ToolCalls int            `json:"tool_calls"`
	KPIs      map[string]any `json:"kpis,omitempty"`
	ToolTrace []ToolTrace    `json:"tool_trace,omitempty"`
	Findings  []Finding      `json:"findings,omitempty"`
	Error     string         `json:"error,omitempty"`
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
func NewAdapter(agentType, agentURL, gatewayURL, authToken, skillsDir string) (*Adapter, error) {
	var backend AgentBackend
	switch agentType {
	case "ironclaw", "openclaw":
		b := NewIronclawBackend(agentURL)
		if authToken != "" {
			b.SetAuthToken(authToken)
		}
		backend = b
	case "tinyclaw":
		b := NewTinyclawBackend(agentURL, gatewayURL)
		if skillsDir != "" {
			b.SetSkillsDir(skillsDir)
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
// emit structured findings that the adapter can extract.
const findingsInstruction = `

When you complete your analysis, if you have actionable findings to report, output them as a JSON block delimited by markers:
__findings__[{"title":"...","body":"...","severity":"critical|high|medium|low","labels":["label1"],"fingerprint":"unique-key"}]__end_findings__
Each finding should have a unique fingerprint for deduplication. Omit the block entirely if there are no findings.`

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
