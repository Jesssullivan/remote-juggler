package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// CampaignClient proxies campaign operations to the campaign-runner API.
type CampaignClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewCampaignClient creates a client for the campaign-runner API.
func NewCampaignClient(baseURL string) *CampaignClient {
	return &CampaignClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Configured returns true if a campaign runner URL is set.
func (c *CampaignClient) Configured() bool {
	return c.baseURL != ""
}

// Trigger triggers a campaign by ID via POST /trigger?campaign=ID.
func (c *CampaignClient) Trigger(ctx context.Context, campaignID string) (json.RawMessage, error) {
	url := fmt.Sprintf("%s/trigger?campaign=%s", c.baseURL, campaignID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("campaign trigger: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("campaign trigger returned %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// List lists all campaigns via GET /campaigns.
func (c *CampaignClient) List(ctx context.Context) (json.RawMessage, error) {
	url := fmt.Sprintf("%s/campaigns", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("campaign list: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("campaign list returned %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// handleCampaignTriggerTool handles the juggler_campaign_trigger MCP tool call.
func handleCampaignTriggerTool(client *CampaignClient, args json.RawMessage) (json.RawMessage, error) {
	if client == nil || !client.Configured() {
		return mcpTextResult("campaign runner not configured")
	}

	var a struct {
		CampaignID string `json:"campaign_id"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}
	if a.CampaignID == "" {
		return mcpTextResult("campaign_id is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.Trigger(ctx, a.CampaignID)
	if err != nil {
		return mcpTextResult(fmt.Sprintf("trigger failed: %v", err))
	}

	return mcpTextResult(map[string]any{"response": json.RawMessage(resp)})
}

// handleCampaignListTool handles the juggler_campaign_list MCP tool call.
func handleCampaignListTool(client *CampaignClient, args json.RawMessage) (json.RawMessage, error) {
	if client == nil || !client.Configured() {
		return mcpTextResult("campaign runner not configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.List(ctx)
	if err != nil {
		return mcpTextResult(fmt.Sprintf("list failed: %v", err))
	}

	return mcpTextResult(map[string]any{"campaigns": json.RawMessage(resp)})
}
