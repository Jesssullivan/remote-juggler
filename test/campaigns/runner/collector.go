package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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
		"name": "remotejuggler/campaigns/global-kill",
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
