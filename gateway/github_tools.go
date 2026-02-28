package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GitHubToolHandler implements 6 GitHub REST API tools for the gateway.
// Token resolution is lazy via tokenFunc (typically backed by Setec).
type GitHubToolHandler struct {
	httpClient *http.Client
	tokenFunc  func(ctx context.Context) (string, error)
	apiBase    string
}

// NewGitHubToolHandler creates a handler with lazy token resolution.
func NewGitHubToolHandler(tokenFunc func(ctx context.Context) (string, error)) *GitHubToolHandler {
	return &GitHubToolHandler{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		tokenFunc:  tokenFunc,
		apiBase:    "https://api.github.com",
	}
}

// github_fetch: GET /repos/{owner}/{repo}/contents/{path}?ref={ref}
func (h *GitHubToolHandler) Fetch(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
		Path  string `json:"path"`
		Ref   string `json:"ref"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}
	if a.Owner == "" || a.Repo == "" || a.Path == "" {
		return nil, fmt.Errorf("owner, repo, and path are required")
	}

	url := fmt.Sprintf("%s/repos/%s/%s/contents/%s", h.apiBase, a.Owner, a.Repo, a.Path)
	if a.Ref != "" {
		url += "?ref=" + a.Ref
	}

	resp, err := h.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return mcpTextResult(map[string]any{
			"error":  fmt.Sprintf("GitHub returned %d", resp.StatusCode),
			"detail": truncateStr(string(body), 500),
		})
	}

	var content struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
		SHA      string `json:"sha"`
		Size     int    `json:"size"`
		Path     string `json:"path"`
	}
	if err := json.Unmarshal(body, &content); err != nil {
		return mcpTextResult(map[string]any{"raw": string(body)})
	}

	decoded := content.Content
	if content.Encoding == "base64" {
		b, err := base64.StdEncoding.DecodeString(content.Content)
		if err == nil {
			decoded = string(b)
		}
	}

	return mcpTextResult(map[string]any{
		"path":    content.Path,
		"sha":     content.SHA,
		"size":    content.Size,
		"content": decoded,
	})
}

// github_list_alerts: GET /repos/{owner}/{repo}/code-scanning/alerts
func (h *GitHubToolHandler) ListAlerts(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Owner    string `json:"owner"`
		Repo     string `json:"repo"`
		State    string `json:"state"`
		Severity string `json:"severity"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}
	if a.Owner == "" || a.Repo == "" {
		return nil, fmt.Errorf("owner and repo are required")
	}

	url := fmt.Sprintf("%s/repos/%s/%s/code-scanning/alerts", h.apiBase, a.Owner, a.Repo)
	sep := "?"
	if a.State != "" {
		url += sep + "state=" + a.State
		sep = "&"
	}
	if a.Severity != "" {
		url += sep + "severity=" + a.Severity
	}

	resp, err := h.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return mcpTextResult(map[string]any{
			"error":  fmt.Sprintf("GitHub returned %d", resp.StatusCode),
			"detail": truncateStr(string(body), 500),
		})
	}

	var alerts []map[string]any
	if err := json.Unmarshal(body, &alerts); err != nil {
		return mcpTextResult(map[string]any{"raw": string(body)})
	}

	return mcpTextResult(map[string]any{
		"alerts": alerts,
		"count":  len(alerts),
	})
}

// github_get_alert: GET /repos/{owner}/{repo}/code-scanning/alerts/{number}
func (h *GitHubToolHandler) GetAlert(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Owner       string `json:"owner"`
		Repo        string `json:"repo"`
		AlertNumber int    `json:"alert_number"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}
	if a.Owner == "" || a.Repo == "" || a.AlertNumber == 0 {
		return nil, fmt.Errorf("owner, repo, and alert_number are required")
	}

	url := fmt.Sprintf("%s/repos/%s/%s/code-scanning/alerts/%d", h.apiBase, a.Owner, a.Repo, a.AlertNumber)

	resp, err := h.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return mcpTextResult(map[string]any{
			"error":  fmt.Sprintf("GitHub returned %d", resp.StatusCode),
			"detail": truncateStr(string(body), 500),
		})
	}

	var alert map[string]any
	if err := json.Unmarshal(body, &alert); err != nil {
		return mcpTextResult(map[string]any{"raw": string(body)})
	}

	return mcpTextResult(alert)
}

// github_create_branch: GET ref for base, then POST new ref.
func (h *GitHubToolHandler) CreateBranch(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Owner      string `json:"owner"`
		Repo       string `json:"repo"`
		BranchName string `json:"branch_name"`
		Base       string `json:"base"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}
	if a.Owner == "" || a.Repo == "" || a.BranchName == "" {
		return nil, fmt.Errorf("owner, repo, and branch_name are required")
	}
	if a.Base == "" {
		a.Base = "main"
	}

	// Get SHA of base branch.
	baseURL := fmt.Sprintf("%s/repos/%s/%s/git/ref/heads/%s", h.apiBase, a.Owner, a.Repo, a.Base)
	resp, err := h.doRequest(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read base ref: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return mcpTextResult(map[string]any{
			"error":  fmt.Sprintf("could not find base branch %q: %d", a.Base, resp.StatusCode),
			"detail": truncateStr(string(body), 500),
		})
	}

	var baseRef struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := json.Unmarshal(body, &baseRef); err != nil {
		return nil, fmt.Errorf("parse base ref: %w", err)
	}

	// Create new branch ref.
	createURL := fmt.Sprintf("%s/repos/%s/%s/git/refs", h.apiBase, a.Owner, a.Repo)
	payload, _ := json.Marshal(map[string]any{
		"ref": "refs/heads/" + a.BranchName,
		"sha": baseRef.Object.SHA,
	})

	createResp, err := h.doRequest(ctx, http.MethodPost, createURL, payload)
	if err != nil {
		return nil, err
	}
	defer createResp.Body.Close()

	createBody, err := io.ReadAll(createResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read create response: %w", err)
	}

	if createResp.StatusCode != http.StatusCreated {
		return mcpTextResult(map[string]any{
			"error":  fmt.Sprintf("create branch returned %d", createResp.StatusCode),
			"detail": truncateStr(string(createBody), 500),
		})
	}

	return mcpTextResult(map[string]any{
		"branch":   a.BranchName,
		"base":     a.Base,
		"base_sha": baseRef.Object.SHA,
		"created":  true,
	})
}

// github_update_file: GET file SHA, then PUT with new content.
func (h *GitHubToolHandler) UpdateFile(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Owner   string `json:"owner"`
		Repo    string `json:"repo"`
		Path    string `json:"path"`
		Content string `json:"content"`
		Message string `json:"message"`
		Branch  string `json:"branch"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}
	if a.Owner == "" || a.Repo == "" || a.Path == "" || a.Content == "" || a.Message == "" || a.Branch == "" {
		return nil, fmt.Errorf("owner, repo, path, content, message, and branch are required")
	}

	// Get current file SHA (required for update).
	getURL := fmt.Sprintf("%s/repos/%s/%s/contents/%s?ref=%s", h.apiBase, a.Owner, a.Repo, a.Path, a.Branch)
	resp, err := h.doRequest(ctx, http.MethodGet, getURL, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	var fileSHA string
	if resp.StatusCode == http.StatusOK {
		var file struct {
			SHA string `json:"sha"`
		}
		json.Unmarshal(body, &file)
		fileSHA = file.SHA
	}
	// If 404, this is a new file creation — no SHA needed.

	// PUT the file content.
	putURL := fmt.Sprintf("%s/repos/%s/%s/contents/%s", h.apiBase, a.Owner, a.Repo, a.Path)
	payload := map[string]any{
		"message": a.Message,
		"content": base64.StdEncoding.EncodeToString([]byte(a.Content)),
		"branch":  a.Branch,
	}
	if fileSHA != "" {
		payload["sha"] = fileSHA
	}
	payloadBytes, _ := json.Marshal(payload)

	putResp, err := h.doRequest(ctx, http.MethodPut, putURL, payloadBytes)
	if err != nil {
		return nil, err
	}
	defer putResp.Body.Close()

	putBody, err := io.ReadAll(putResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read put response: %w", err)
	}

	if putResp.StatusCode != http.StatusOK && putResp.StatusCode != http.StatusCreated {
		return mcpTextResult(map[string]any{
			"error":  fmt.Sprintf("update file returned %d", putResp.StatusCode),
			"detail": truncateStr(string(putBody), 500),
		})
	}

	var result struct {
		Content struct {
			SHA  string `json:"sha"`
			Path string `json:"path"`
		} `json:"content"`
	}
	json.Unmarshal(putBody, &result)

	return mcpTextResult(map[string]any{
		"path":    result.Content.Path,
		"sha":     result.Content.SHA,
		"branch":  a.Branch,
		"updated": true,
	})
}

// github_create_pr: POST /repos/{owner}/{repo}/pulls
func (h *GitHubToolHandler) CreatePR(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
		Title string `json:"title"`
		Body  string `json:"body"`
		Head  string `json:"head"`
		Base  string `json:"base"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}
	if a.Owner == "" || a.Repo == "" || a.Title == "" || a.Head == "" {
		return nil, fmt.Errorf("owner, repo, title, and head are required")
	}
	if a.Base == "" {
		a.Base = "main"
	}

	url := fmt.Sprintf("%s/repos/%s/%s/pulls", h.apiBase, a.Owner, a.Repo)
	payload, _ := json.Marshal(map[string]any{
		"title": a.Title,
		"body":  a.Body,
		"head":  a.Head,
		"base":  a.Base,
	})

	resp, err := h.doRequest(ctx, http.MethodPost, url, payload)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusCreated {
		return mcpTextResult(map[string]any{
			"error":  fmt.Sprintf("create PR returned %d", resp.StatusCode),
			"detail": truncateStr(string(body), 500),
		})
	}

	var pr struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		State   string `json:"state"`
	}
	if err := json.Unmarshal(body, &pr); err != nil {
		return mcpTextResult(map[string]any{"raw": string(body)})
	}

	return mcpTextResult(map[string]any{
		"number":   pr.Number,
		"html_url": pr.HTMLURL,
		"title":    pr.Title,
		"state":    pr.State,
		"created":  true,
	})
}

// github_create_issue: POST /repos/{owner}/{repo}/issues
func (h *GitHubToolHandler) CreateIssue(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Owner  string   `json:"owner"`
		Repo   string   `json:"repo"`
		Title  string   `json:"title"`
		Body   string   `json:"body"`
		Labels []string `json:"labels"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}
	if a.Owner == "" || a.Repo == "" || a.Title == "" {
		return nil, fmt.Errorf("owner, repo, and title are required")
	}

	url := fmt.Sprintf("%s/repos/%s/%s/issues", h.apiBase, a.Owner, a.Repo)
	payload, _ := json.Marshal(map[string]any{
		"title":  a.Title,
		"body":   a.Body,
		"labels": a.Labels,
	})

	resp, err := h.doRequest(ctx, http.MethodPost, url, payload)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusCreated {
		return mcpTextResult(map[string]any{
			"error":  fmt.Sprintf("create issue returned %d", resp.StatusCode),
			"detail": truncateStr(string(body), 500),
		})
	}

	var issue struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		State   string `json:"state"`
	}
	if err := json.Unmarshal(body, &issue); err != nil {
		return mcpTextResult(map[string]any{"raw": string(body)})
	}

	return mcpTextResult(map[string]any{
		"number":   issue.Number,
		"html_url": issue.HTMLURL,
		"title":    issue.Title,
		"state":    issue.State,
		"created":  true,
	})
}

// github_patch_file: GET file content, apply find/replace, then PUT updated content.
// Unlike github_update_file which replaces the entire file, this tool makes targeted
// edits by finding old_content and replacing it with new_content.
func (h *GitHubToolHandler) PatchFile(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Owner      string `json:"owner"`
		Repo       string `json:"repo"`
		Path       string `json:"path"`
		OldContent string `json:"old_content"`
		NewContent string `json:"new_content"`
		Message    string `json:"message"`
		Branch     string `json:"branch"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}
	if a.Owner == "" || a.Repo == "" || a.Path == "" || a.OldContent == "" || a.Message == "" || a.Branch == "" {
		return nil, fmt.Errorf("owner, repo, path, old_content, message, and branch are required")
	}

	// Fetch current file content.
	getURL := fmt.Sprintf("%s/repos/%s/%s/contents/%s?ref=%s", h.apiBase, a.Owner, a.Repo, a.Path, a.Branch)
	resp, err := h.doRequest(ctx, http.MethodGet, getURL, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return mcpTextResult(map[string]any{
			"error":  fmt.Sprintf("file not found: GitHub returned %d", resp.StatusCode),
			"detail": truncateStr(string(body), 500),
		})
	}

	var file struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
		SHA      string `json:"sha"`
	}
	if err := json.Unmarshal(body, &file); err != nil {
		return nil, fmt.Errorf("parse file response: %w", err)
	}

	// Decode content.
	currentContent := file.Content
	if file.Encoding == "base64" {
		decoded, err := base64.StdEncoding.DecodeString(file.Content)
		if err != nil {
			return nil, fmt.Errorf("decode base64: %w", err)
		}
		currentContent = string(decoded)
	}

	// Count occurrences and apply patch.
	count := strings.Count(currentContent, a.OldContent)
	if count == 0 {
		return mcpTextResult(map[string]any{
			"error":       "old_content not found in file",
			"path":        a.Path,
			"branch":      a.Branch,
			"file_length": len(currentContent),
		})
	}

	// Replace first occurrence only (safer than replace-all).
	patched := strings.Replace(currentContent, a.OldContent, a.NewContent, 1)

	// PUT the patched content.
	putURL := fmt.Sprintf("%s/repos/%s/%s/contents/%s", h.apiBase, a.Owner, a.Repo, a.Path)
	payload := map[string]any{
		"message": a.Message,
		"content": base64.StdEncoding.EncodeToString([]byte(patched)),
		"branch":  a.Branch,
		"sha":     file.SHA,
	}
	payloadBytes, _ := json.Marshal(payload)

	putResp, err := h.doRequest(ctx, http.MethodPut, putURL, payloadBytes)
	if err != nil {
		return nil, err
	}
	defer putResp.Body.Close()

	putBody, err := io.ReadAll(putResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read put response: %w", err)
	}

	if putResp.StatusCode != http.StatusOK && putResp.StatusCode != http.StatusCreated {
		return mcpTextResult(map[string]any{
			"error":  fmt.Sprintf("patch file returned %d", putResp.StatusCode),
			"detail": truncateStr(string(putBody), 500),
		})
	}

	var result struct {
		Content struct {
			SHA  string `json:"sha"`
			Path string `json:"path"`
		} `json:"content"`
	}
	json.Unmarshal(putBody, &result)

	return mcpTextResult(map[string]any{
		"path":              result.Content.Path,
		"sha":               result.Content.SHA,
		"branch":            a.Branch,
		"patched":           true,
		"occurrences_found": count,
		"replaced":          1,
	})
}

// RequestSecret creates a secret-request issue on the remote-juggler repo.
func (h *GitHubToolHandler) RequestSecret(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Name    string `json:"name"`
		Reason  string `json:"reason"`
		Urgency string `json:"urgency"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}
	if a.Name == "" || a.Reason == "" {
		return nil, fmt.Errorf("name and reason are required")
	}
	if a.Urgency == "" {
		a.Urgency = "medium"
	}

	title := fmt.Sprintf("Secret Request: %s", a.Name)
	body := fmt.Sprintf("## Secret Request\n\n**Name:** `%s`\n**Reason:** %s\n**Urgency:** %s\n\n---\n*Automated request from agent via `juggler_request_secret` tool.*", a.Name, a.Reason, a.Urgency)

	issueArgs, _ := json.Marshal(map[string]any{
		"owner":  "tinyland-inc",
		"repo":   "remote-juggler",
		"title":  title,
		"body":   body,
		"labels": []string{"secret-request", a.Urgency},
	})

	return h.CreateIssue(ctx, issueArgs)
}

// doRequest performs an authenticated GitHub API request.
func (h *GitHubToolHandler) doRequest(ctx context.Context, method, url string, body []byte) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	token, err := h.tokenFunc(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve GitHub token: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return h.httpClient.Do(req)
}

// truncateStr truncates a string to maxLen, appending "..." if truncated.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
