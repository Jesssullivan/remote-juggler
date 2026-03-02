package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
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
