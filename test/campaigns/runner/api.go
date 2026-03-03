package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// APIServer provides HTTP endpoints for campaign management.
// Runs alongside the scheduler in the campaign runner sidecar.
type APIServer struct {
	scheduler *Scheduler
	collector *Collector
	registry  map[string]*Campaign

	// WebhookSecret is the HMAC-SHA256 secret for validating incoming
	// webhook payloads. When empty, HMAC validation is skipped.
	WebhookSecret string

	// threadFetcher fetches GitHub issue threads for conversation dispatch.
	threadFetcher *ThreadFetcher

	mu          sync.Mutex
	lastResults map[string]*CampaignResult
}

// NewAPIServer creates an API server for the given scheduler and campaign registry.
func NewAPIServer(scheduler *Scheduler, registry map[string]*Campaign) *APIServer {
	return &APIServer{
		scheduler:   scheduler,
		collector:   scheduler.GetCollector(),
		registry:    registry,
		lastResults: make(map[string]*CampaignResult),
	}
}

// ListenAndServe starts the HTTP server on the given address.
func (a *APIServer) ListenAndServe(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", a.handleHealth)
	mux.HandleFunc("/trigger", a.handleTrigger)
	mux.HandleFunc("/status", a.handleStatus)
	mux.HandleFunc("/campaigns", a.handleCampaigns)
	mux.HandleFunc("/webhook", a.handleWebhook)
	mux.HandleFunc("/proof", a.handleProof)
	mux.HandleFunc("/evaluate", a.handleEvaluate)

	log.Printf("api server listening on %s", addr)
	return http.ListenAndServe(addr, mux)
}

// RecordResult stores a campaign result for the /status endpoint.
func (a *APIServer) RecordResult(result *CampaignResult) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastResults[result.CampaignID] = result
}

// handleHealth returns runner health and loaded campaign count.
func (a *APIServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":         "ok",
		"service":        "campaign-runner",
		"campaign_count": len(a.registry),
	})
}

// handleTrigger manually triggers a campaign by ID.
// POST /trigger?campaign=ID
func (a *APIServer) handleTrigger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	campaignID := r.URL.Query().Get("campaign")
	if campaignID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "missing 'campaign' query parameter"})
		return
	}

	campaign, ok := a.registry[campaignID]
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "campaign not found", "campaign_id": campaignID})
		return
	}

	// Run asynchronously so the HTTP request returns immediately.
	// Use a detached context with the campaign's max duration as timeout,
	// since the request context will be canceled when the handler returns.
	go func() {
		timeout := parseDuration(campaign.Guardrails.MaxDuration)
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		log.Printf("api: manual trigger for campaign %s (timeout=%s)", campaignID, timeout)
		if err := a.scheduler.RunCampaign(ctx, campaign); err != nil {
			log.Printf("api: campaign %s failed: %v", campaignID, err)
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{
		"status":      "accepted",
		"campaign_id": campaignID,
	})
}

// handleStatus returns campaign results.
// GET /status?campaign=ID (optional filter)
func (a *APIServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()

	campaignID := r.URL.Query().Get("campaign")

	w.Header().Set("Content-Type", "application/json")

	if campaignID != "" {
		result, ok := a.lastResults[campaignID]
		if !ok {
			json.NewEncoder(w).Encode(map[string]any{
				"campaign_id": campaignID,
				"status":      "no_runs",
			})
			return
		}
		json.NewEncoder(w).Encode(result)
		return
	}

	// Return all results.
	json.NewEncoder(w).Encode(map[string]any{
		"results": a.lastResults,
		"count":   len(a.lastResults),
	})
}

// handleCampaigns lists all loaded campaigns.
func (a *APIServer) handleCampaigns(w http.ResponseWriter, r *http.Request) {
	type campaignInfo struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Agent       string `json:"agent"`
		Schedule    string `json:"schedule,omitempty"`
		MaxDuration string `json:"max_duration"`
	}

	campaigns := make([]campaignInfo, 0, len(a.registry))
	for _, c := range a.registry {
		campaigns = append(campaigns, campaignInfo{
			ID:          c.ID,
			Name:        c.Name,
			Agent:       c.Agent,
			Schedule:    c.Trigger.Schedule,
			MaxDuration: c.Guardrails.MaxDuration,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"campaigns": campaigns,
		"count":     len(campaigns),
	})
}

// WebhookPayload is a normalized representation of forge push/PR events.
type WebhookPayload struct {
	// Event type: "push", "pull_request"
	Event string `json:"event"`
	// Forge: "github", "gitlab"
	Forge string `json:"forge"`
	// Ref is the full git ref (e.g. "refs/heads/main")
	Ref string `json:"ref"`
	// Repo is "org/repo"
	Repo string `json:"repo"`
	// ChangedFiles lists file paths modified in the push or PR.
	ChangedFiles []string `json:"changed_files,omitempty"`
}

// handleWebhook accepts GitHub/GitLab push/PR payloads and triggers matching campaigns.
// POST /webhook
func (a *APIServer) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Validate HMAC if secret is configured.
	if a.WebhookSecret != "" {
		sig := r.Header.Get("X-Hub-Signature-256")
		if sig == "" {
			sig = r.Header.Get("X-Gitlab-Token")
		}
		if !validateHMAC(body, sig, a.WebhookSecret) {
			http.Error(w, "invalid signature", http.StatusForbidden)
			return
		}
	}

	// Check if this is an issue_comment event (conversation dispatch).
	ghEvent := r.Header.Get("X-GitHub-Event")
	if ghEvent == "issue_comment" {
		log.Printf("api: received issue_comment webhook")
		a.handleIssueComment(body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"triggered": []string{"conversation"},
			"count":     1,
		})
		return
	}

	var payload WebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "parse: "+err.Error(), http.StatusBadRequest)
		return
	}

	if payload.Event == "" {
		// Try to infer from GitHub event header.
		if ghEvent != "" {
			payload.Event = ghEvent
		}
	}

	if payload.Event == "" || payload.Repo == "" {
		http.Error(w, "missing event or repo", http.StatusBadRequest)
		return
	}

	triggered := a.matchWebhookToCampaigns(payload)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"triggered": triggered,
		"count":     len(triggered),
	})
}

// matchWebhookToCampaigns finds campaigns whose event triggers match the payload
// and dispatches them asynchronously.
func (a *APIServer) matchWebhookToCampaigns(payload WebhookPayload) []string {
	var triggered []string

	for id, campaign := range a.registry {
		trigger := campaign.Trigger

		// Check event type match.
		if trigger.Event != payload.Event {
			continue
		}

		// Check target repo match (any target matches).
		repoMatch := false
		for _, target := range campaign.Targets {
			targetRepo := target.Org + "/" + target.Repo
			if targetRepo == payload.Repo || target.Repo == "*" {
				repoMatch = true
				break
			}
		}
		if !repoMatch {
			continue
		}

		// Check path filters (if specified, at least one changed file must match).
		if len(trigger.PathFilters) > 0 && len(payload.ChangedFiles) > 0 {
			if !pathFiltersMatch(trigger.PathFilters, payload.ChangedFiles) {
				continue
			}
		}

		triggered = append(triggered, id)

		// Dispatch asynchronously.
		go func(c *Campaign, cID string) {
			timeout := parseDuration(c.Guardrails.MaxDuration)
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			log.Printf("api: webhook triggered campaign %s (event=%s, repo=%s)", cID, payload.Event, payload.Repo)
			if err := a.scheduler.RunCampaign(ctx, c); err != nil {
				log.Printf("api: webhook campaign %s failed: %v", cID, err)
			}
		}(campaign, id)
	}

	return triggered
}

// pathFiltersMatch checks if any changed file matches any of the glob patterns.
func pathFiltersMatch(filters []string, changedFiles []string) bool {
	for _, pattern := range filters {
		for _, file := range changedFiles {
			matched, err := filepath.Match(pattern, file)
			if err == nil && matched {
				return true
			}
			// Also try matching against just the filename for simple patterns.
			if matched, err := filepath.Match(pattern, filepath.Base(file)); err == nil && matched {
				return true
			}
		}
	}
	return false
}

// ProofEntry is the raw evidence for a single campaign result.
// Designed to be unforgeable: flags suspicious patterns that indicate
// confabulated results rather than real tool-backed work.
type ProofEntry struct {
	CampaignID   string           `json:"campaign_id"`
	RunID        string           `json:"run_id"`
	Agent        string           `json:"agent"`
	Status       string           `json:"status"`
	StartedAt    string           `json:"started_at"`
	FinishedAt   string           `json:"finished_at"`
	DurationSec  float64          `json:"duration_sec"`
	ToolCalls    int              `json:"tool_calls"`
	FindingCount int              `json:"finding_count"`
	ToolTrace    []ToolTraceEntry `json:"tool_trace,omitempty"`
	Flags        []string         `json:"flags,omitempty"`
	Confabulated bool             `json:"confabulated"`
	Error        string           `json:"error,omitempty"`
}

// handleProof returns raw, unforgeable evidence for campaign results.
// GET /proof — returns all results with integrity flags.
// GET /proof?campaign=ID — returns a single campaign's proof.
//
// Flags:
//   - SUSPICIOUS: duration < 1s with tool_calls > 0 (instant completion is fake)
//   - CONFABULATED: findings exist but tool_trace is empty or all "skipped"
//   - NO_TOOL_CALLS: agent campaign with 0 tool calls (LLM ran but didn't use tools)
//   - ALL_TOOLS_SKIPPED: every tool trace entry says "skipped"
func (a *APIServer) handleProof(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()

	campaignID := r.URL.Query().Get("campaign")
	w.Header().Set("Content-Type", "application/json")

	if campaignID != "" {
		result, ok := a.lastResults[campaignID]
		if !ok {
			json.NewEncoder(w).Encode(map[string]string{
				"campaign_id": campaignID,
				"status":      "no_runs",
			})
			return
		}
		json.NewEncoder(w).Encode(buildProofEntry(result))
		return
	}

	// All results.
	entries := make([]ProofEntry, 0, len(a.lastResults))
	confabulated := 0
	suspicious := 0
	for _, result := range a.lastResults {
		entry := buildProofEntry(result)
		if entry.Confabulated {
			confabulated++
		}
		if containsFlag(entry.Flags, "SUSPICIOUS") {
			suspicious++
		}
		entries = append(entries, entry)
	}

	json.NewEncoder(w).Encode(map[string]any{
		"entries":      entries,
		"total":        len(entries),
		"confabulated": confabulated,
		"suspicious":   suspicious,
		"honest":       len(entries) - confabulated - suspicious,
	})
}

// handleEvaluate computes before/after metrics for a campaign.
// GET /evaluate?campaign=ID&since=RFC3339_TIMESTAMP
func (a *APIServer) handleEvaluate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	campaignID := r.URL.Query().Get("campaign")
	since := r.URL.Query().Get("since")

	if campaignID == "" || since == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "missing required query parameters: campaign, since",
		})
		return
	}

	// Look up campaign in registry to get setecKey.
	campaign, ok := a.registry[campaignID]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{
			"error":       "campaign not found",
			"campaign_id": campaignID,
		})
		return
	}

	if a.collector == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "collector not configured",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	result, err := a.collector.EvaluateImprovement(ctx, campaign.Outputs.SetecKey, since)
	if err != nil {
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]string{
			"error":       err.Error(),
			"campaign_id": campaignID,
		})
		return
	}

	result.CampaignID = campaignID
	json.NewEncoder(w).Encode(result)
}

// buildProofEntry analyzes a CampaignResult and flags integrity issues.
func buildProofEntry(r *CampaignResult) ProofEntry {
	entry := ProofEntry{
		CampaignID:   r.CampaignID,
		RunID:        r.RunID,
		Agent:        r.Agent,
		Status:       r.Status,
		StartedAt:    r.StartedAt,
		FinishedAt:   r.FinishedAt,
		ToolCalls:    r.ToolCalls,
		FindingCount: len(r.Findings),
		Error:        r.Error,
	}

	// Convert tool trace.
	entry.ToolTrace = r.ToolTrace

	// Calculate duration.
	if r.StartedAt != "" && r.FinishedAt != "" {
		start, err1 := time.Parse(time.RFC3339, r.StartedAt)
		end, err2 := time.Parse(time.RFC3339, r.FinishedAt)
		if err1 == nil && err2 == nil {
			entry.DurationSec = end.Sub(start).Seconds()
		}
	}

	// Flag: SUSPICIOUS — instant completion with claimed tool calls.
	if entry.DurationSec < 1.0 && entry.DurationSec >= 0 && r.ToolCalls > 0 {
		entry.Flags = append(entry.Flags, "SUSPICIOUS")
	}

	// Flag: NO_TOOL_CALLS — agent-based campaign with 0 tool calls.
	// Gateway-direct campaigns are expected to have tool calls from the dispatcher.
	if r.Agent != "gateway-direct" && r.Agent != "" && r.ToolCalls == 0 && r.Status == "success" {
		entry.Flags = append(entry.Flags, "NO_TOOL_CALLS")
	}

	// Flag: ALL_TOOLS_SKIPPED — every tool trace entry is "skipped".
	if len(r.ToolTrace) > 0 {
		allSkipped := true
		for _, t := range r.ToolTrace {
			if !strings.Contains(strings.ToLower(t.Summary), "skipped") {
				allSkipped = false
				break
			}
		}
		if allSkipped {
			entry.Flags = append(entry.Flags, "ALL_TOOLS_SKIPPED")
		}
	}

	// Confabulation check: findings exist but no tool evidence.
	entry.Confabulated = CheckConfabulation(r)
	if entry.Confabulated {
		entry.Flags = append(entry.Flags, "CONFABULATED")
	}

	return entry
}

func containsFlag(flags []string, flag string) bool {
	for _, f := range flags {
		if f == flag {
			return true
		}
	}
	return false
}

// CheckConfabulation returns true if a campaign result contains findings
// that are not backed by tool evidence. A finding about file X is confabulated
// unless the tool trace shows a tool call that accessed that file.
//
// Rules:
//   - Findings with 0 tool calls from an agent = CONFABULATED
//   - Findings where all tool trace entries are "skipped" = CONFABULATED
//   - Gateway-direct campaigns are exempt (tools called by dispatcher, not agent)
func CheckConfabulation(r *CampaignResult) bool {
	if len(r.Findings) == 0 {
		return false // No findings = nothing to confabulate.
	}

	// Gateway-direct campaigns call tools via the dispatcher, not the agent.
	if r.Agent == "gateway-direct" {
		return false
	}

	// No tool calls but has findings = confabulated.
	if r.ToolCalls == 0 {
		return true
	}

	// All tools skipped = confabulated.
	if len(r.ToolTrace) > 0 {
		allSkipped := true
		for _, t := range r.ToolTrace {
			if !strings.Contains(strings.ToLower(t.Summary), "skipped") {
				allSkipped = false
				break
			}
		}
		if allSkipped {
			return true
		}
	}

	return false
}

// validateHMAC validates a GitHub-style HMAC-SHA256 signature.
// GitHub sends "sha256=<hex>", GitLab sends the raw token.
func validateHMAC(body []byte, signature, secret string) bool {
	if signature == "" {
		return false
	}

	// GitHub format: "sha256=<hex>"
	if strings.HasPrefix(signature, "sha256=") {
		sigHex := strings.TrimPrefix(signature, "sha256=")
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		expected := hex.EncodeToString(mac.Sum(nil))
		return hmac.Equal([]byte(sigHex), []byte(expected))
	}

	// GitLab format: raw token comparison.
	return signature == secret
}
