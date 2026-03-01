package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Publisher publishes sanitized campaign results to GitHub Discussions.
// After Setec storage and feedback processing, the publisher creates a
// formatted Discussion post for public visibility.
type Publisher struct {
	httpClient *http.Client
	token      string
	baseURL    string // GraphQL endpoint, defaults to "https://api.github.com/graphql"

	// repoID is the GitHub GraphQL node ID of the target repository.
	repoID string
	// categoryIDs maps Discussion category names to their GraphQL node IDs.
	categoryIDs map[string]string
	// repoOwner and repoName for REST API fallbacks.
	repoOwner string
	repoName  string
}

// NewPublisher creates a Publisher for the given repository.
func NewPublisher(token, repoOwner, repoName string) *Publisher {
	return &Publisher{
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		token:       token,
		baseURL:     "https://api.github.com/graphql",
		repoID:      "",
		categoryIDs: make(map[string]string),
		repoOwner:   repoOwner,
		repoName:    repoName,
	}
}

// Init fetches the repository ID and Discussion category IDs from GitHub.
// Must be called before Publish.
func (p *Publisher) Init(ctx context.Context) error {
	query := `query($owner: String!, $name: String!) {
  repository(owner: $owner, name: $name) {
    id
    discussionCategories(first: 25) {
      nodes {
        id
        name
      }
    }
  }
}`
	vars := map[string]string{"owner": p.repoOwner, "name": p.repoName}
	resp, err := p.graphql(ctx, query, vars)
	if err != nil {
		return fmt.Errorf("init publisher: %w", err)
	}

	var result struct {
		Data struct {
			Repository struct {
				ID                   string `json:"id"`
				DiscussionCategories struct {
					Nodes []struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"nodes"`
				} `json:"discussionCategories"`
			} `json:"repository"`
		} `json:"data"`
		Errors []graphqlError `json:"errors"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return fmt.Errorf("parse init response: %w", err)
	}
	if len(result.Errors) > 0 {
		return fmt.Errorf("graphql error: %s", result.Errors[0].Message)
	}

	p.repoID = result.Data.Repository.ID
	for _, cat := range result.Data.Repository.DiscussionCategories.Nodes {
		p.categoryIDs[cat.Name] = cat.ID
	}

	log.Printf("publisher: repo=%s/%s id=%s categories=%d",
		p.repoOwner, p.repoName, p.repoID, len(p.categoryIDs))
	return nil
}

// Publish creates a Discussion for the campaign result.
// Returns the Discussion URL on success.
func (p *Publisher) Publish(ctx context.Context, campaign *Campaign, result *CampaignResult) (string, error) {
	if p.repoID == "" {
		return "", fmt.Errorf("publisher not initialized (call Init first)")
	}

	categoryName := p.categoryForCampaign(campaign)
	categoryID, ok := p.categoryIDs[categoryName]
	if !ok {
		return "", fmt.Errorf("discussion category %q not found", categoryName)
	}

	title := p.formatTitle(campaign, result)
	body := p.formatBody(campaign, result)

	query := `mutation($repoId: ID!, $categoryId: ID!, $title: String!, $body: String!) {
  createDiscussion(input: {repositoryId: $repoId, categoryId: $categoryId, title: $title, body: $body}) {
    discussion {
      url
      number
    }
  }
}`
	vars := map[string]string{
		"repoId":     p.repoID,
		"categoryId": categoryID,
		"title":      title,
		"body":       body,
	}

	resp, err := p.graphql(ctx, query, vars)
	if err != nil {
		return "", fmt.Errorf("create discussion: %w", err)
	}

	var mutation struct {
		Data struct {
			CreateDiscussion struct {
				Discussion struct {
					URL    string `json:"url"`
					Number int    `json:"number"`
				} `json:"discussion"`
			} `json:"createDiscussion"`
		} `json:"data"`
		Errors []graphqlError `json:"errors"`
	}
	if err := json.Unmarshal(resp, &mutation); err != nil {
		return "", fmt.Errorf("parse mutation response: %w", err)
	}
	if len(mutation.Errors) > 0 {
		return "", fmt.Errorf("graphql error: %s", mutation.Errors[0].Message)
	}

	url := mutation.Data.CreateDiscussion.Discussion.URL
	log.Printf("publisher: created discussion #%d for %s: %s",
		mutation.Data.CreateDiscussion.Discussion.Number, campaign.ID, url)

	// Fire repository_dispatch to trigger README status update.
	p.fireRepositoryDispatch(ctx, campaign.ID, result.RunID)

	return url, nil
}

// categoryForCampaign determines the Discussion category based on campaign type.
func (p *Publisher) categoryForCampaign(campaign *Campaign) string {
	if strings.Contains(campaign.ID, "weekly-digest") {
		return "Weekly Digest"
	}
	if strings.Contains(campaign.ID, "security") || campaign.Agent == "hexstrike" {
		return "Security Advisories"
	}
	return "Agent Reports"
}

// formatTitle generates a Discussion title for a campaign result.
func (p *Publisher) formatTitle(campaign *Campaign, result *CampaignResult) string {
	status := result.Status
	if status == "success" {
		status = "PASS"
	} else {
		status = strings.ToUpper(status)
	}
	ts := result.FinishedAt
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		ts = t.Format("2006-01-02 15:04 UTC")
	}
	return fmt.Sprintf("[%s] %s | %s", status, campaign.Name, ts)
}

// formatBody generates the Discussion markdown body.
func (p *Publisher) formatBody(campaign *Campaign, result *CampaignResult) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("## Campaign: %s\n", campaign.Name))
	b.WriteString(fmt.Sprintf("**Run**: `%s` | **Agent**: %s", result.RunID, result.Agent))

	// Calculate duration.
	if start, errS := time.Parse(time.RFC3339, result.StartedAt); errS == nil {
		if end, errE := time.Parse(time.RFC3339, result.FinishedAt); errE == nil {
			b.WriteString(fmt.Sprintf(" | **Duration**: %s", end.Sub(start).Round(time.Second)))
		}
	}
	b.WriteString(fmt.Sprintf(" | **Tool Calls**: %d\n\n", result.ToolCalls))

	// Status badge.
	switch result.Status {
	case "success":
		b.WriteString("> **Status**: PASS\n\n")
	case "failure":
		b.WriteString(fmt.Sprintf("> **Status**: FAIL -- %s\n\n", sanitizeString(result.Error)))
	case "timeout":
		b.WriteString("> **Status**: TIMEOUT\n\n")
	default:
		b.WriteString(fmt.Sprintf("> **Status**: %s\n\n", strings.ToUpper(result.Status)))
	}

	// KPIs table.
	if len(result.KPIs) > 0 {
		b.WriteString("### KPIs\n")
		b.WriteString("| Metric | Value |\n|--------|-------|\n")
		for k, v := range result.KPIs {
			b.WriteString(fmt.Sprintf("| %s | %v |\n", k, sanitizeValue(v)))
		}
		b.WriteString("\n")
	}

	// Tool trace (collapsible).
	if len(result.ToolTrace) > 0 {
		b.WriteString(fmt.Sprintf("<details>\n<summary>%d tool calls — expand trace</summary>\n\n", len(result.ToolTrace)))
		b.WriteString("| Time | Tool | Summary |\n|------|------|---------|\n")
		for _, entry := range result.ToolTrace {
			ts := entry.Timestamp
			if t, err := time.Parse(time.RFC3339, ts); err == nil {
				ts = t.Format("15:04:05")
			}
			summary := sanitizeString(entry.Summary)
			if entry.IsError {
				summary = "**ERROR**: " + summary
			}
			b.WriteString(fmt.Sprintf("| %s | `%s` | %s |\n", ts, entry.Tool, summary))
		}
		b.WriteString("\n</details>\n\n")
	}

	// Findings summary (without sensitive details).
	if len(result.Findings) > 0 {
		b.WriteString(fmt.Sprintf("### Findings (%d)\n", len(result.Findings)))
		for _, f := range result.Findings {
			b.WriteString(fmt.Sprintf("- **[%s]** %s\n", f.Severity, sanitizeString(f.Title)))
		}
		b.WriteString("\n")
	}

	// Campaign definition link.
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("*[Campaign definition](https://github.com/%s/%s/tree/main/test/campaigns/) | ",
		p.repoOwner, p.repoName))
	b.WriteString("Generated by the RemoteJuggler agent ecosystem*\n")

	// Append rj-meta block for agent-to-agent communication.
	meta := RJMeta{
		Version:     "1",
		From:        result.Agent,
		MessageType: "report",
		CampaignID:  campaign.ID,
		Timestamp:   result.FinishedAt,
	}
	if len(result.Findings) > 0 {
		meta.Context = map[string]any{
			"finding_count": len(result.Findings),
		}
	}
	b.WriteString(FormatRJMeta(meta))

	return b.String()
}

// fireRepositoryDispatch triggers a repository_dispatch event to update the README.
func (p *Publisher) fireRepositoryDispatch(ctx context.Context, campaignID, runID string) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/dispatches", p.repoOwner, p.repoName)
	payload := map[string]any{
		"event_type": "agent-status-update",
		"client_payload": map[string]string{
			"campaign_id": campaignID,
			"run_id":      runID,
		},
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("publisher: dispatch error: %v", err)
		return
	}
	p.setAuth(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		log.Printf("publisher: dispatch error: %v", err)
		return
	}
	resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		log.Printf("publisher: fired repository_dispatch for %s", campaignID)
	} else {
		log.Printf("publisher: dispatch returned %d", resp.StatusCode)
	}
}

// graphql executes a GraphQL query against the GitHub API.
func (p *Publisher) graphql(ctx context.Context, query string, variables any) (json.RawMessage, error) {
	payload := map[string]any{
		"query":     query,
		"variables": variables,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	p.setAuth(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("graphql returned %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

type graphqlError struct {
	Message string `json:"message"`
}

// UpdateToken replaces the stored token (used for App token refresh).
func (p *Publisher) UpdateToken(token string) {
	p.token = token
}

// setAuth adds GitHub authentication headers.
func (p *Publisher) setAuth(req *http.Request) {
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
}

// --- Sanitization ---

// secretPatterns matches common secret prefixes that should never appear in public output.
var secretPatterns = regexp.MustCompile(`(?i)(ghp_|ghs_|gho_|ghu_|github_pat_|sk-|sk-ant-|AKIA[A-Z0-9]|-----BEGIN)`)

// internalURLPattern matches internal K8s and tailnet hostnames.
var internalURLPattern = regexp.MustCompile(`[a-zA-Z0-9._-]+\.svc\.cluster\.local[:\d]*|[a-zA-Z0-9._-]+\.ts\.net[:\d]*|[a-zA-Z0-9._-]+\.taila[a-f0-9]+\.ts\.net[:\d]*`)

// sanitizeString removes secrets and internal URLs from a string.
func sanitizeString(s string) string {
	s = secretPatterns.ReplaceAllString(s, "[REDACTED]")
	s = internalURLPattern.ReplaceAllString(s, "[internal]")
	return s
}

// sanitizeValue sanitizes a KPI value, checking for high-entropy strings.
func sanitizeValue(v any) any {
	switch val := v.(type) {
	case string:
		if shannonEntropy(val) > 4.5 && len(val) > 8 {
			return "[REDACTED]"
		}
		return sanitizeString(val)
	case float64, int, int64, bool:
		return v
	default:
		s := fmt.Sprintf("%v", v)
		return sanitizeString(s)
	}
}

// shannonEntropy calculates the Shannon entropy (bits per character) of a string.
func shannonEntropy(s string) float64 {
	if len(s) == 0 {
		return 0
	}
	freq := make(map[rune]float64)
	for _, c := range s {
		freq[c]++
	}
	length := float64(len([]rune(s)))
	var entropy float64
	for _, count := range freq {
		p := count / length
		if p > 0 {
			entropy -= p * math.Log2(p)
		}
	}
	return entropy
}
