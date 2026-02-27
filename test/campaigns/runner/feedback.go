package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// FeedbackHandler creates and manages GitHub issues based on campaign findings.
type FeedbackHandler struct {
	httpClient *http.Client
	token      string
	// baseURL is the GitHub API base URL. Defaults to "https://api.github.com".
	// Override in tests to point at a mock server.
	baseURL string
}

// Finding represents a single finding from a campaign run that should be
// reported as a GitHub issue.
type Finding struct {
	Title       string   `json:"title"`
	Body        string   `json:"body"`
	Severity    string   `json:"severity"` // "critical", "high", "medium", "low"
	Labels      []string `json:"labels"`
	CampaignID  string   `json:"campaign_id"`
	RunID       string   `json:"run_id"`
	Fingerprint string   `json:"fingerprint"` // Dedupe key.
}

// GitHubLabel represents a GitHub label object returned by the API.
type GitHubLabel struct {
	Name string `json:"name"`
}

// GitHubIssue represents a GitHub issue (subset of fields).
// Labels are objects ({name, id, color, ...}) in the GitHub API, not plain strings.
type GitHubIssue struct {
	Number int           `json:"number"`
	Title  string        `json:"title"`
	State  string        `json:"state"`
	Labels []GitHubLabel `json:"labels,omitempty"`
	Body   string        `json:"body"`
}

// NewFeedbackHandler creates a FeedbackHandler with the given GitHub token.
func NewFeedbackHandler(token string) *FeedbackHandler {
	return &FeedbackHandler{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		token:      token,
		baseURL:    "https://api.github.com",
	}
}

// ProcessFindings creates issues for new findings and optionally closes resolved ones.
func (f *FeedbackHandler) ProcessFindings(ctx context.Context, campaign *Campaign, findings []Finding, previousFindings []Finding) error {
	if !campaign.Feedback.CreateIssues {
		return nil
	}

	repo := campaign.Outputs.IssueRepo
	if repo == "" {
		return fmt.Errorf("no issueRepo configured for campaign %s", campaign.ID)
	}

	// Create issues for new findings.
	for _, finding := range findings {
		existing, err := f.findExistingIssue(ctx, repo, finding)
		if err != nil {
			log.Printf("feedback %s: error checking existing issue: %v", campaign.ID, err)
			continue
		}

		if existing != nil {
			log.Printf("feedback %s: issue #%d already exists for %q", campaign.ID, existing.Number, finding.Title)
			continue
		}

		labels := finding.Labels
		if len(campaign.Outputs.IssueLabels) > 0 {
			labels = append(labels, campaign.Outputs.IssueLabels...)
		}

		issue, err := f.createIssue(ctx, repo, finding.Title, finding.Body, labels)
		if err != nil {
			log.Printf("feedback %s: create issue error: %v", campaign.ID, err)
			continue
		}
		log.Printf("feedback %s: created issue #%d: %s", campaign.ID, issue.Number, finding.Title)
	}

	// Close resolved issues.
	if campaign.Feedback.CloseResolvedIssues && len(previousFindings) > 0 {
		f.closeResolvedIssues(ctx, campaign, repo, findings, previousFindings)
	}

	return nil
}

// findExistingIssue searches for an open issue matching the finding's fingerprint.
func (f *FeedbackHandler) findExistingIssue(ctx context.Context, repo string, finding Finding) (*GitHubIssue, error) {
	searchTerm := finding.Fingerprint
	if searchTerm == "" {
		searchTerm = finding.Title
	}

	url := fmt.Sprintf("%s/search/issues?q=%s+repo:%s+state:open+in:body",
		f.baseURL, searchTerm, repo)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	f.setAuth(req)

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search returned %d", resp.StatusCode)
	}

	var result struct {
		Items []GitHubIssue `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if len(result.Items) > 0 {
		return &result.Items[0], nil
	}
	return nil, nil
}

// createIssue creates a GitHub issue.
func (f *FeedbackHandler) createIssue(ctx context.Context, repo, title, body string, labels []string) (*GitHubIssue, error) {
	url := fmt.Sprintf("%s/repos/%s/issues", f.baseURL, repo)

	payload := map[string]any{
		"title":  title,
		"body":   body,
		"labels": labels,
	}
	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	f.setAuth(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("create issue returned %d: %s", resp.StatusCode, string(respBody))
	}

	var issue GitHubIssue
	if err := json.NewDecoder(resp.Body).Decode(&issue); err != nil {
		return nil, err
	}
	return &issue, nil
}

// closeIssue closes a GitHub issue with a comment.
func (f *FeedbackHandler) closeIssue(ctx context.Context, repo string, issueNumber int, comment string) error {
	// Add comment.
	commentURL := fmt.Sprintf("%s/repos/%s/issues/%d/comments", f.baseURL, repo, issueNumber)
	commentPayload, _ := json.Marshal(map[string]string{"body": comment})
	commentReq, err := http.NewRequestWithContext(ctx, http.MethodPost, commentURL, bytes.NewReader(commentPayload))
	if err != nil {
		return err
	}
	f.setAuth(commentReq)
	commentReq.Header.Set("Content-Type", "application/json")
	commentResp, err := f.httpClient.Do(commentReq)
	if err != nil {
		return fmt.Errorf("add comment: %w", err)
	}
	commentResp.Body.Close()

	// Close issue.
	closeURL := fmt.Sprintf("%s/repos/%s/issues/%d", f.baseURL, repo, issueNumber)
	closePayload, _ := json.Marshal(map[string]string{"state": "closed"})
	closeReq, err := http.NewRequestWithContext(ctx, http.MethodPatch, closeURL, bytes.NewReader(closePayload))
	if err != nil {
		return err
	}
	f.setAuth(closeReq)
	closeReq.Header.Set("Content-Type", "application/json")
	closeResp, err := f.httpClient.Do(closeReq)
	if err != nil {
		return fmt.Errorf("close issue: %w", err)
	}
	closeResp.Body.Close()

	return nil
}

// closeResolvedIssues closes issues for findings that were in the previous run
// but not in the current run (i.e., resolved).
func (f *FeedbackHandler) closeResolvedIssues(ctx context.Context, campaign *Campaign, repo string, current, previous []Finding) {
	currentFingerprints := make(map[string]bool)
	for _, finding := range current {
		fp := finding.Fingerprint
		if fp == "" {
			fp = finding.Title
		}
		currentFingerprints[fp] = true
	}

	for _, prev := range previous {
		fp := prev.Fingerprint
		if fp == "" {
			fp = prev.Title
		}
		if currentFingerprints[fp] {
			continue // Still present.
		}

		existing, err := f.findExistingIssue(ctx, repo, prev)
		if err != nil || existing == nil {
			continue
		}

		comment := fmt.Sprintf("This issue was automatically resolved. Campaign `%s` no longer reports this finding.", campaign.ID)
		if err := f.closeIssue(ctx, repo, existing.Number, comment); err != nil {
			log.Printf("feedback %s: close issue #%d error: %v", campaign.ID, existing.Number, err)
		} else {
			log.Printf("feedback %s: closed resolved issue #%d", campaign.ID, existing.Number)
		}
	}
}

// UpdateToken replaces the stored token (used for App token refresh).
func (f *FeedbackHandler) UpdateToken(token string) {
	f.token = token
}

// setAuth adds GitHub authentication headers to the request.
func (f *FeedbackHandler) setAuth(req *http.Request) {
	if f.token != "" {
		req.Header.Set("Authorization", "token "+f.token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
}
