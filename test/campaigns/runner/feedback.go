package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
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
// reported as a GitHub issue or remediation PR.
type Finding struct {
	Title            string            `json:"title"`
	Body             string            `json:"body"`
	Severity         string            `json:"severity"` // "critical", "high", "medium", "low"
	Labels           []string          `json:"labels"`
	CampaignID       string            `json:"campaign_id"`
	RunID            string            `json:"run_id"`
	Fingerprint      string            `json:"fingerprint"` // Dedupe key.
	Fixable          bool              `json:"fixable,omitempty"`
	RemediationType  string            `json:"remediation_type,omitempty"`  // "dependency_bump", "yaml_fix", "secret_rotation"
	RemediationHints map[string]string `json:"remediation_hints,omitempty"` // Keys: file, find, replace, commit_message
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

// ProcessPRFindings creates PRs for fixable findings via the GitHub API.
// Guarded by campaign.Feedback.CreatePRs && !campaign.Guardrails.ReadOnly.
func (f *FeedbackHandler) ProcessPRFindings(ctx context.Context, campaign *Campaign, findings []Finding) error {
	if !campaign.Feedback.CreatePRs || campaign.Guardrails.ReadOnly {
		return nil
	}

	repo := campaign.Outputs.IssueRepo
	if repo == "" {
		return fmt.Errorf("no issueRepo configured for campaign %s", campaign.ID)
	}

	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid issueRepo format %q (expected owner/repo)", repo)
	}
	owner, repoName := parts[0], parts[1]

	branchPrefix := campaign.Outputs.PRBranchPrefix
	if branchPrefix == "" {
		branchPrefix = "sid/fix-"
	}

	baseBranch := "main"
	if len(campaign.Targets) > 0 && campaign.Targets[0].Branch != "" {
		baseBranch = campaign.Targets[0].Branch
	}

	for _, finding := range findings {
		if !finding.Fixable || finding.RemediationHints == nil {
			continue
		}

		filePath := finding.RemediationHints["file"]
		findText := finding.RemediationHints["find"]
		replaceText := finding.RemediationHints["replace"]
		if filePath == "" || findText == "" || replaceText == "" {
			log.Printf("feedback %s: incomplete remediation hints for %q, skipping PR", campaign.ID, finding.Title)
			continue
		}

		branchName := prBranchName(branchPrefix, finding)

		// Dedup: check if a PR already exists for this branch.
		if f.prExists(ctx, owner, repoName, branchName) {
			log.Printf("feedback %s: PR already exists for branch %s", campaign.ID, branchName)
			continue
		}

		// 1. Create branch from base.
		if err := f.createBranch(ctx, owner, repoName, branchName, baseBranch); err != nil {
			log.Printf("feedback %s: create branch %s: %v", campaign.ID, branchName, err)
			continue
		}

		// 2. Patch file with find/replace.
		commitMsg := finding.RemediationHints["commit_message"]
		if commitMsg == "" {
			commitMsg = fmt.Sprintf("fix: %s", finding.Title)
		}
		if err := f.patchFile(ctx, owner, repoName, branchName, filePath, findText, replaceText, commitMsg); err != nil {
			log.Printf("feedback %s: patch %s on %s: %v", campaign.ID, filePath, branchName, err)
			continue
		}

		// 3. Create pull request.
		prTitle := fmt.Sprintf("fix: %s", finding.Title)
		prBody := buildPRBody(campaign, finding)
		prURL, err := f.createPullRequest(ctx, owner, repoName, prTitle, prBody, branchName, baseBranch)
		if err != nil {
			log.Printf("feedback %s: create PR for %s: %v", campaign.ID, branchName, err)
			continue
		}
		log.Printf("feedback %s: created PR %s for %q", campaign.ID, prURL, finding.Title)
	}

	return nil
}

// prBranchName generates a deterministic branch name from the prefix and finding fingerprint.
func prBranchName(prefix string, finding Finding) string {
	fp := finding.Fingerprint
	if fp == "" {
		fp = finding.Title
	}
	// Use first 12 chars of fingerprint for a short, unique branch suffix.
	suffix := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + 32 // lowercase
		}
		return '-'
	}, fp)
	if len(suffix) > 24 {
		suffix = suffix[:24]
	}
	return prefix + suffix
}

// prExists checks if an open PR already exists with the given head branch.
func (f *FeedbackHandler) prExists(ctx context.Context, owner, repo, branch string) bool {
	url := fmt.Sprintf("%s/repos/%s/%s/pulls?head=%s:%s&state=open",
		f.baseURL, owner, repo, owner, branch)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	f.setAuth(req)

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	var prs []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&prs); err != nil {
		return false
	}
	return len(prs) > 0
}

// createBranch creates a new branch from a base branch via the GitHub Git refs API.
func (f *FeedbackHandler) createBranch(ctx context.Context, owner, repo, branch, base string) error {
	// Get the SHA of the base branch.
	refURL := fmt.Sprintf("%s/repos/%s/%s/git/refs/heads/%s", f.baseURL, owner, repo, base)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, refURL, nil)
	if err != nil {
		return err
	}
	f.setAuth(req)

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("get ref %s: %d %s", base, resp.StatusCode, string(body))
	}

	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ref); err != nil {
		return err
	}

	// Create the new ref.
	createURL := fmt.Sprintf("%s/repos/%s/%s/git/refs", f.baseURL, owner, repo)
	payload, _ := json.Marshal(map[string]string{
		"ref": "refs/heads/" + branch,
		"sha": ref.Object.SHA,
	})
	createReq, err := http.NewRequestWithContext(ctx, http.MethodPost, createURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	f.setAuth(createReq)
	createReq.Header.Set("Content-Type", "application/json")

	createResp, err := f.httpClient.Do(createReq)
	if err != nil {
		return err
	}
	defer createResp.Body.Close()

	if createResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(createResp.Body)
		return fmt.Errorf("create ref %s: %d %s", branch, createResp.StatusCode, string(body))
	}
	return nil
}

// patchFile fetches a file, applies find/replace, and commits the result.
func (f *FeedbackHandler) patchFile(ctx context.Context, owner, repo, branch, path, find, replace, message string) error {
	// Get the file content.
	getURL := fmt.Sprintf("%s/repos/%s/%s/contents/%s?ref=%s", f.baseURL, owner, repo, path, branch)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
	if err != nil {
		return err
	}
	f.setAuth(req)

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("get file %s: %d %s", path, resp.StatusCode, string(body))
	}

	var file struct {
		SHA     string `json:"sha"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&file); err != nil {
		return err
	}

	// Decode base64 content.
	content, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
	if err != nil {
		return fmt.Errorf("decode file content: %w", err)
	}

	// Apply find/replace.
	original := string(content)
	patched := strings.Replace(original, find, replace, 1)
	if patched == original {
		return fmt.Errorf("find text not found in %s", path)
	}

	// PUT the updated content.
	putURL := fmt.Sprintf("%s/repos/%s/%s/contents/%s", f.baseURL, owner, repo, path)
	payload, _ := json.Marshal(map[string]string{
		"message": message,
		"content": base64.StdEncoding.EncodeToString([]byte(patched)),
		"sha":     file.SHA,
		"branch":  branch,
	})
	putReq, err := http.NewRequestWithContext(ctx, http.MethodPut, putURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	f.setAuth(putReq)
	putReq.Header.Set("Content-Type", "application/json")

	putResp, err := f.httpClient.Do(putReq)
	if err != nil {
		return err
	}
	defer putResp.Body.Close()

	if putResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(putResp.Body)
		return fmt.Errorf("put file %s: %d %s", path, putResp.StatusCode, string(body))
	}
	return nil
}

// createPullRequest creates a PR and returns the HTML URL.
func (f *FeedbackHandler) createPullRequest(ctx context.Context, owner, repo, title, body, head, base string) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/pulls", f.baseURL, owner, repo)
	payload, _ := json.Marshal(map[string]string{
		"title": title,
		"body":  body,
		"head":  head,
		"base":  base,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	f.setAuth(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create PR: %d %s", resp.StatusCode, string(respBody))
	}

	var pr struct {
		HTMLURL string `json:"html_url"`
		Number  int    `json:"number"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return "", err
	}
	return pr.HTMLURL, nil
}

// buildPRBody constructs the PR description from campaign and finding info.
func buildPRBody(campaign *Campaign, finding Finding) string {
	if campaign.Outputs.PRBodyTemplate != "" {
		body := campaign.Outputs.PRBodyTemplate
		body = strings.ReplaceAll(body, "{{title}}", finding.Title)
		body = strings.ReplaceAll(body, "{{severity}}", finding.Severity)
		body = strings.ReplaceAll(body, "{{campaign}}", campaign.ID)
		body = strings.ReplaceAll(body, "{{fingerprint}}", finding.Fingerprint)
		return body
	}

	var b strings.Builder
	b.WriteString("## Automated Remediation\n\n")
	fmt.Fprintf(&b, "**Campaign**: `%s`\n", campaign.ID)
	fmt.Fprintf(&b, "**Severity**: %s\n", finding.Severity)
	if finding.RemediationType != "" {
		fmt.Fprintf(&b, "**Type**: %s\n", finding.RemediationType)
	}
	fmt.Fprintf(&b, "**Fingerprint**: `%s`\n\n", finding.Fingerprint)
	if finding.Body != "" {
		b.WriteString("### Details\n\n")
		b.WriteString(finding.Body)
		b.WriteString("\n\n")
	}
	b.WriteString("---\n*Created automatically by campaign runner*\n")
	return b.String()
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
