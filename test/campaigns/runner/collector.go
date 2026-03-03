package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strings"
	"time"
)

// Collector stores campaign results in Setec via rj-gateway MCP tool calls.
type Collector struct {
	gatewayURL string
	dispatcher *Dispatcher
}

// NewCollector creates a Collector that stores results via the given rj-gateway.
func NewCollector(gatewayURL string) *Collector {
	return &Collector{
		gatewayURL: gatewayURL,
		dispatcher: NewDispatcher(gatewayURL, "", "", ""),
	}
}

// StoreResult persists a campaign result to Setec via juggler_setec_put.
func (c *Collector) StoreResult(ctx context.Context, campaign *Campaign, result *CampaignResult) error {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}

	// Store at the campaign's configured Setec key with /latest suffix.
	key := campaign.Outputs.SetecKey + "/latest"
	_, err = c.dispatcher.callTool(ctx, "juggler_setec_put", map[string]any{
		"name":  key,
		"value": string(resultJSON),
	})
	if err != nil {
		return fmt.Errorf("setec put %s: %w", key, err)
	}

	log.Printf("campaign %s: result stored at %s", campaign.ID, key)

	// Also store a timestamped copy for history.
	historyKey := fmt.Sprintf("%s/runs/%s", campaign.Outputs.SetecKey, result.RunID)
	_, err = c.dispatcher.callTool(ctx, "juggler_setec_put", map[string]any{
		"name":  historyKey,
		"value": string(resultJSON),
	})
	if err != nil {
		log.Printf("campaign %s: history store failed (non-fatal): %v", campaign.ID, err)
	}

	return nil
}

// GetPreviousFindings loads the last campaign result from Setec and returns
// its findings. Returns nil if no previous result exists or on any error.
func (c *Collector) GetPreviousFindings(ctx context.Context, campaign *Campaign) []Finding {
	key := campaign.Outputs.SetecKey + "/latest"
	resp, err := c.dispatcher.callTool(ctx, "juggler_setec_get", map[string]any{
		"name": key,
	})
	if err != nil {
		return nil
	}

	// Parse the MCP response to get the stored JSON.
	var mcpResult struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(resp, &mcpResult); err != nil || len(mcpResult.Content) == 0 {
		return nil
	}

	// The stored value is a JSON-encoded CampaignResult.
	var prev CampaignResult
	if err := json.Unmarshal([]byte(mcpResult.Content[0].Text), &prev); err != nil {
		return nil
	}

	return prev.Findings
}

// CheckKillSwitch queries Setec for the global campaign kill switch.
// Returns true if campaigns should be halted.
func (c *Collector) CheckKillSwitch(ctx context.Context) (bool, error) {
	resp, err := c.dispatcher.callTool(ctx, "juggler_setec_get", map[string]any{
		"name": "campaigns/global-kill",
	})
	if err != nil {
		// Key not found = kill switch not set = safe to proceed.
		return false, nil
	}

	// Parse the MCP response to check the value.
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return false, fmt.Errorf("parse kill switch response: %w", err)
	}

	if len(result.Content) > 0 && result.Content[0].Text == "true" {
		return true, nil
	}
	return false, nil
}

// ClearKillSwitch sets the global kill switch to "false" in Setec.
// Used by the scheduler to auto-clear stale kill switches.
func (c *Collector) ClearKillSwitch(ctx context.Context) error {
	_, err := c.dispatcher.callTool(ctx, "juggler_setec_put", map[string]any{
		"name":  "campaigns/global-kill",
		"value": "false",
	})
	return err
}

// GetAgentMemory retrieves an agent's persistent memory from Setec.
// Returns empty string if no memory exists or on any error.
// Memory is stored at key: remotejuggler/agents/{agent}/memory
func (c *Collector) GetAgentMemory(ctx context.Context, agent string) string {
	key := fmt.Sprintf("remotejuggler/agents/%s/memory", agent)
	resp, err := c.dispatcher.callTool(ctx, "juggler_setec_get", map[string]any{
		"name": key,
	})
	if err != nil {
		return ""
	}

	var mcpResult struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(resp, &mcpResult); err != nil || len(mcpResult.Content) == 0 {
		return ""
	}

	return mcpResult.Content[0].Text
}

// StoreAgentMemory writes an agent's persistent memory to Setec.
// Memory is stored at key: remotejuggler/agents/{agent}/memory
func (c *Collector) StoreAgentMemory(ctx context.Context, agent, reflection string) error {
	key := fmt.Sprintf("remotejuggler/agents/%s/memory", agent)
	_, err := c.dispatcher.callTool(ctx, "juggler_setec_put", map[string]any{
		"name":  key,
		"value": reflection,
	})
	if err != nil {
		return fmt.Errorf("store agent memory for %s: %w", agent, err)
	}
	log.Printf("stored %d bytes of agent memory for %s", len(reflection), agent)
	return nil
}

// GetAgentSkills retrieves an agent's accumulated skills from Setec.
// Returns empty string if no skills exist or on any error.
// Skills are stored at key: remotejuggler/agents/{agent}/skills
func (c *Collector) GetAgentSkills(ctx context.Context, agent string) string {
	key := fmt.Sprintf("remotejuggler/agents/%s/skills", agent)
	resp, err := c.dispatcher.callTool(ctx, "juggler_setec_get", map[string]any{
		"name": key,
	})
	if err != nil {
		return ""
	}

	var mcpResult struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(resp, &mcpResult); err != nil || len(mcpResult.Content) == 0 {
		return ""
	}

	return mcpResult.Content[0].Text
}

// StoreAgentSkill appends a new skill to the agent's skill library in Setec.
// If a skill with the same name exists, it is replaced (upsert by ### Skill: header).
// Skills are stored at key: remotejuggler/agents/{agent}/skills
func (c *Collector) StoreAgentSkill(ctx context.Context, agent, skill string) error {
	existing := c.GetAgentSkills(ctx, agent)
	merged := upsertSkill(existing, skill)

	key := fmt.Sprintf("remotejuggler/agents/%s/skills", agent)
	_, err := c.dispatcher.callTool(ctx, "juggler_setec_put", map[string]any{
		"name":  key,
		"value": merged,
	})
	if err != nil {
		return fmt.Errorf("store agent skill for %s: %w", agent, err)
	}
	log.Printf("stored %d bytes of agent skills for %s", len(merged), agent)
	return nil
}

// upsertSkill merges a new skill into existing skills text.
// If a skill with the same "### Skill:" header exists, it is replaced.
// Otherwise, the new skill is appended.
func upsertSkill(existing, newSkill string) string {
	newSkill = strings.TrimSpace(newSkill)
	if newSkill == "" {
		return existing
	}

	newName := parseSkillName(newSkill)
	if newName == "" {
		// Can't parse skill name — append as-is.
		if existing == "" {
			return newSkill
		}
		return existing + "\n\n---\n\n" + newSkill
	}

	if existing == "" {
		return newSkill
	}

	// Parse existing into sections by "### Skill:" headers.
	sections := splitSkillSections(existing)
	replaced := false
	for i, section := range sections {
		if parseSkillName(section) == newName {
			sections[i] = newSkill
			replaced = true
			break
		}
	}

	if !replaced {
		sections = append(sections, newSkill)
	}

	return strings.Join(sections, "\n\n---\n\n")
}

// parseSkillName extracts the skill name from a "### Skill: <name>" header.
func parseSkillName(skill string) string {
	for _, line := range strings.Split(skill, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "### Skill:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "### Skill:"))
		}
	}
	return ""
}

// splitSkillSections splits accumulated skills text into individual sections
// delimited by "---" separators.
func splitSkillSections(text string) []string {
	parts := strings.Split(text, "\n\n---\n\n")
	var sections []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			sections = append(sections, p)
		}
	}
	return sections
}

// maxHistoryEntries is the rolling window size for run history.
const maxHistoryEntries = 10

// HistoryEntry is a compact record for the rolling history array.
type HistoryEntry struct {
	RunID      string  `json:"run_id"`
	Status     string  `json:"status"`
	ToolCalls  int     `json:"tool_calls"`
	TokensUsed int     `json:"tokens_used,omitempty"`
	DurationS  float64 `json:"duration_s"`
	Timestamp  string  `json:"timestamp"`
}

// StoreRunHistory appends a HistoryEntry to the rolling array at {setecKey}/history.
// Keeps only the last maxHistoryEntries entries to bound storage.
func (c *Collector) StoreRunHistory(ctx context.Context, campaign *Campaign, result *CampaignResult) error {
	key := campaign.Outputs.SetecKey + "/history"

	// Build the new entry.
	entry := HistoryEntry{
		RunID:      result.RunID,
		Status:     result.Status,
		ToolCalls:  result.ToolCalls,
		TokensUsed: result.TokensUsed,
		Timestamp:  result.FinishedAt,
	}

	// Compute duration from start/finish times.
	if result.StartedAt != "" && result.FinishedAt != "" {
		start, err1 := time.Parse(time.RFC3339, result.StartedAt)
		end, err2 := time.Parse(time.RFC3339, result.FinishedAt)
		if err1 == nil && err2 == nil {
			entry.DurationS = math.Round(end.Sub(start).Seconds()*100) / 100
		}
	}

	// Read existing history.
	existing, _ := c.GetRunHistory(ctx, campaign.Outputs.SetecKey)

	// Append and trim to rolling window.
	existing = append(existing, entry)
	if len(existing) > maxHistoryEntries {
		existing = existing[len(existing)-maxHistoryEntries:]
	}

	historyJSON, err := json.Marshal(existing)
	if err != nil {
		return fmt.Errorf("marshal history: %w", err)
	}

	_, err = c.dispatcher.callTool(ctx, "juggler_setec_put", map[string]any{
		"name":  key,
		"value": string(historyJSON),
	})
	if err != nil {
		return fmt.Errorf("setec put %s: %w", key, err)
	}

	log.Printf("campaign %s: run history stored (%d entries)", campaign.ID, len(existing))
	return nil
}

// GetRunHistory retrieves the rolling history array from Setec.
func (c *Collector) GetRunHistory(ctx context.Context, setecKey string) ([]HistoryEntry, error) {
	key := setecKey + "/history"
	resp, err := c.dispatcher.callTool(ctx, "juggler_setec_get", map[string]any{
		"name": key,
	})
	if err != nil {
		return nil, fmt.Errorf("setec get %s: %w", key, err)
	}

	// Parse the MCP response to get the stored JSON.
	var mcpResult struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(resp, &mcpResult); err != nil || len(mcpResult.Content) == 0 {
		return nil, fmt.Errorf("parse history response: no content")
	}

	var entries []HistoryEntry
	if err := json.Unmarshal([]byte(mcpResult.Content[0].Text), &entries); err != nil {
		return nil, fmt.Errorf("parse history entries: %w", err)
	}

	return entries, nil
}

// EvaluationResult is the system-computed before/after comparison.
type EvaluationResult struct {
	CampaignID        string  `json:"campaign_id"`
	SplitAt           string  `json:"split_at"`
	BeforeRuns        int     `json:"before_runs"`
	AfterRuns         int     `json:"after_runs"`
	SuccessRateDelta  float64 `json:"success_rate_delta"`
	AvgToolCallsDelta float64 `json:"avg_tool_calls_delta"`
	AvgDurationDelta  float64 `json:"avg_duration_delta_s"`
	Improved          bool    `json:"improved"`
}

// EvaluateImprovement splits history at a timestamp and computes before/after deltas.
// Returns an error if there are no entries after the split point.
func (c *Collector) EvaluateImprovement(ctx context.Context, setecKey, since string) (*EvaluationResult, error) {
	entries, err := c.GetRunHistory(ctx, setecKey)
	if err != nil {
		return nil, fmt.Errorf("get history: %w", err)
	}

	splitTime, err := time.Parse(time.RFC3339, since)
	if err != nil {
		return nil, fmt.Errorf("parse since timestamp: %w", err)
	}

	var before, after []HistoryEntry
	for _, e := range entries {
		t, err := time.Parse(time.RFC3339, e.Timestamp)
		if err != nil {
			continue
		}
		if t.Before(splitTime) {
			before = append(before, e)
		} else {
			after = append(after, e)
		}
	}

	if len(after) == 0 {
		return nil, fmt.Errorf("no entries after split point %s", since)
	}
	if len(before) == 0 {
		return nil, fmt.Errorf("no entries before split point %s", since)
	}

	result := &EvaluationResult{
		SplitAt:           since,
		BeforeRuns:        len(before),
		AfterRuns:         len(after),
		SuccessRateDelta:  successRate(after) - successRate(before),
		AvgToolCallsDelta: avgToolCalls(after) - avgToolCalls(before),
		AvgDurationDelta:  avgDuration(after) - avgDuration(before),
	}
	// Improved if success rate went up, OR if success rate stayed perfect
	// and duration decreased (efficiency gain).
	result.Improved = result.SuccessRateDelta > 0 ||
		(result.SuccessRateDelta == 0 && successRate(before) == 1.0 && result.AvgDurationDelta < 0)

	return result, nil
}

// successRate computes the fraction of entries with status "success".
func successRate(entries []HistoryEntry) float64 {
	if len(entries) == 0 {
		return 0
	}
	successes := 0
	for _, e := range entries {
		if e.Status == "success" {
			successes++
		}
	}
	return float64(successes) / float64(len(entries))
}

// avgToolCalls computes the mean tool call count across entries.
func avgToolCalls(entries []HistoryEntry) float64 {
	if len(entries) == 0 {
		return 0
	}
	total := 0
	for _, e := range entries {
		total += e.ToolCalls
	}
	return float64(total) / float64(len(entries))
}

// avgDuration computes the mean duration in seconds across entries.
func avgDuration(entries []HistoryEntry) float64 {
	if len(entries) == 0 {
		return 0
	}
	total := 0.0
	for _, e := range entries {
		total += e.DurationS
	}
	return total / float64(len(entries))
}
