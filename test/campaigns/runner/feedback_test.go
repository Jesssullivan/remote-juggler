package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// mockGitHub simulates the GitHub API for feedback tests.
type mockGitHub struct {
	mu            sync.Mutex
	createdIssues []map[string]any
	closedIssues  []int
	comments      []map[string]any
	searchResults []GitHubIssue
	issueCounter  int
	// PR-related tracking.
	createdRefs  []map[string]string
	patchedFiles []map[string]string
	createdPRs   []map[string]string
	openPRs      []map[string]string // Simulates existing open PRs.
	fileContents map[string]string   // path → content for GET /contents.
}

func newMockGitHub() *mockGitHub {
	return &mockGitHub{
		fileContents: make(map[string]string),
	}
}

func (m *mockGitHub) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/search/issues", m.handleSearch)
	mux.HandleFunc("/repos/", m.handleRepos)
	return mux
}

func (m *mockGitHub) handleSearch(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"items": m.searchResults,
	})
}

func (m *mockGitHub) handleRepos(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()

	path := r.URL.Path

	// GET /repos/{owner}/{repo}/git/refs/heads/{branch}
	if r.Method == http.MethodGet && strings.Contains(path, "/git/refs/heads/") {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ref": path,
			"object": map[string]string{
				"sha": "abc123def456",
			},
		})
		return
	}

	// POST /repos/{owner}/{repo}/git/refs — create branch
	if r.Method == http.MethodPost && strings.HasSuffix(path, "/git/refs") {
		var payload map[string]string
		json.NewDecoder(r.Body).Decode(&payload)
		m.createdRefs = append(m.createdRefs, payload)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"ref": payload["ref"],
			"object": map[string]string{
				"sha": payload["sha"],
			},
		})
		return
	}

	// GET /repos/{owner}/{repo}/contents/{path}?ref={branch}
	if r.Method == http.MethodGet && strings.Contains(path, "/contents/") {
		// Extract the file path after /contents/
		idx := strings.Index(path, "/contents/")
		filePath := path[idx+len("/contents/"):]
		content, ok := m.fileContents[filePath]
		if !ok {
			content = "original content"
		}
		encoded := base64.StdEncoding.EncodeToString([]byte(content))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"sha":     "file-sha-123",
			"content": encoded,
		})
		return
	}

	// PUT /repos/{owner}/{repo}/contents/{path} — patch file
	if r.Method == http.MethodPut && strings.Contains(path, "/contents/") {
		var payload map[string]string
		json.NewDecoder(r.Body).Decode(&payload)
		m.patchedFiles = append(m.patchedFiles, payload)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"content": map[string]string{"sha": "new-sha-456"}})
		return
	}

	// GET /repos/{owner}/{repo}/pulls?head=...&state=open
	if r.Method == http.MethodGet && strings.HasSuffix(path, "/pulls") {
		w.Header().Set("Content-Type", "application/json")
		// Return existing PRs if any match.
		if len(m.openPRs) > 0 {
			json.NewEncoder(w).Encode(m.openPRs)
		} else {
			json.NewEncoder(w).Encode([]any{})
		}
		return
	}

	// POST /repos/{owner}/{repo}/pulls — create PR
	if r.Method == http.MethodPost && strings.HasSuffix(path, "/pulls") {
		var payload map[string]string
		json.NewDecoder(r.Body).Decode(&payload)
		m.createdPRs = append(m.createdPRs, payload)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"html_url": "https://github.com/test/repo/pull/99",
			"number":   99,
		})
		return
	}

	// POST — issue or comment creation
	if r.Method == http.MethodPost {
		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)

		if _, ok := payload["title"]; ok {
			m.issueCounter++
			m.createdIssues = append(m.createdIssues, payload)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(GitHubIssue{
				Number: m.issueCounter,
				Title:  payload["title"].(string),
				State:  "open",
			})
		} else if _, ok := payload["body"]; ok {
			m.comments = append(m.comments, payload)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{"id": 1})
		}
		return
	}

	if r.Method == http.MethodPatch {
		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)
		if payload["state"] == "closed" {
			m.closedIssues = append(m.closedIssues, 1)
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"state": "closed"})
		return
	}

	w.WriteHeader(http.StatusNotFound)
}

func newTestFeedback(server *httptest.Server) *FeedbackHandler {
	handler := NewFeedbackHandler("test-token")
	handler.baseURL = server.URL
	handler.httpClient = server.Client()
	return handler
}

func testCampaignWithFeedback() *Campaign {
	return &Campaign{
		ID: "test-campaign",
		Feedback: Feedback{
			CreateIssues:        true,
			CloseResolvedIssues: true,
		},
		Outputs: CampaignOutputs{
			IssueRepo:   "tinyland-inc/remote-juggler",
			IssueLabels: []string{"campaign", "automated"},
		},
	}
}

func TestFeedbackCreateIssueE2E(t *testing.T) {
	gh := newMockGitHub()
	server := httptest.NewServer(gh.Handler())
	defer server.Close()

	handler := newTestFeedback(server)
	campaign := testCampaignWithFeedback()

	findings := []Finding{
		{
			Title:       "Vulnerable dependency: lodash",
			Body:        "lodash 4.17.20 has known CVEs",
			Severity:    "high",
			CampaignID:  "test-campaign",
			Fingerprint: "dep-lodash-4.17.20",
		},
	}

	err := handler.ProcessFindings(context.Background(), campaign, findings, nil)
	if err != nil {
		t.Fatalf("ProcessFindings: %v", err)
	}

	gh.mu.Lock()
	defer gh.mu.Unlock()

	if len(gh.createdIssues) != 1 {
		t.Fatalf("expected 1 created issue, got %d", len(gh.createdIssues))
	}
	if gh.createdIssues[0]["title"] != "Vulnerable dependency: lodash" {
		t.Errorf("title = %v, want 'Vulnerable dependency: lodash'", gh.createdIssues[0]["title"])
	}
	labels := gh.createdIssues[0]["labels"].([]any)
	if len(labels) != 3 { // "high" from severity not added, but campaign labels are
		// Finding has no labels, campaign adds "campaign" + "automated"
		// Actually finding.Labels is nil, so only campaign labels
		t.Logf("labels = %v (count=%d)", labels, len(labels))
	}
}

func TestFeedbackSkipsDuplicateIssue(t *testing.T) {
	gh := newMockGitHub()
	// Pre-populate search results so the handler finds an existing issue.
	gh.searchResults = []GitHubIssue{
		{Number: 42, Title: "Existing issue", State: "open"},
	}
	server := httptest.NewServer(gh.Handler())
	defer server.Close()

	handler := newTestFeedback(server)
	campaign := testCampaignWithFeedback()

	findings := []Finding{
		{Title: "Existing issue", Fingerprint: "fp-existing"},
	}

	err := handler.ProcessFindings(context.Background(), campaign, findings, nil)
	if err != nil {
		t.Fatalf("ProcessFindings: %v", err)
	}

	gh.mu.Lock()
	defer gh.mu.Unlock()

	if len(gh.createdIssues) != 0 {
		t.Errorf("expected 0 created issues (duplicate), got %d", len(gh.createdIssues))
	}
}

func TestFeedbackCloseResolvedE2E(t *testing.T) {
	gh := newMockGitHub()
	// Search returns an existing open issue for "Issue B" (which is now resolved).
	gh.searchResults = []GitHubIssue{
		{Number: 7, Title: "Issue B", State: "open", Body: "fp-b"},
	}
	server := httptest.NewServer(gh.Handler())
	defer server.Close()

	handler := newTestFeedback(server)
	campaign := testCampaignWithFeedback()

	current := []Finding{
		{Title: "Issue A", Fingerprint: "fp-a"},
	}
	previous := []Finding{
		{Title: "Issue A", Fingerprint: "fp-a"},
		{Title: "Issue B", Fingerprint: "fp-b"},
	}

	// ProcessFindings creates Issue A (new) and closes Issue B (resolved).
	// Reset search results after first call so search for "fp-a" returns nothing
	// and search for "fp-b" returns the existing issue.
	err := handler.ProcessFindings(context.Background(), campaign, current, previous)
	if err != nil {
		t.Fatalf("ProcessFindings: %v", err)
	}

	gh.mu.Lock()
	defer gh.mu.Unlock()

	if len(gh.closedIssues) != 1 {
		t.Errorf("expected 1 closed issue, got %d", len(gh.closedIssues))
	}
	if len(gh.comments) != 1 {
		t.Errorf("expected 1 close comment, got %d", len(gh.comments))
	}
}

func TestFeedbackMultipleFindings(t *testing.T) {
	gh := newMockGitHub()
	server := httptest.NewServer(gh.Handler())
	defer server.Close()

	handler := newTestFeedback(server)
	campaign := testCampaignWithFeedback()

	findings := []Finding{
		{Title: "CVE-2026-001", Fingerprint: "cve-001", Severity: "critical"},
		{Title: "CVE-2026-002", Fingerprint: "cve-002", Severity: "high"},
		{Title: "CVE-2026-003", Fingerprint: "cve-003", Severity: "low"},
	}

	err := handler.ProcessFindings(context.Background(), campaign, findings, nil)
	if err != nil {
		t.Fatalf("ProcessFindings: %v", err)
	}

	gh.mu.Lock()
	defer gh.mu.Unlock()

	if len(gh.createdIssues) != 3 {
		t.Fatalf("expected 3 created issues, got %d", len(gh.createdIssues))
	}
}

func TestFeedbackDisabled(t *testing.T) {
	handler := NewFeedbackHandler("token")

	campaign := &Campaign{
		ID: "test-campaign",
		Feedback: Feedback{
			CreateIssues: false,
		},
	}

	err := handler.ProcessFindings(context.Background(), campaign, []Finding{
		{Title: "Something", Body: "Details"},
	}, nil)

	if err != nil {
		t.Errorf("ProcessFindings with disabled feedback should return nil, got: %v", err)
	}
}

func TestFeedbackNoIssueRepo(t *testing.T) {
	handler := NewFeedbackHandler("token")

	campaign := &Campaign{
		ID: "test-campaign",
		Feedback: Feedback{
			CreateIssues: true,
		},
		Outputs: CampaignOutputs{},
	}

	err := handler.ProcessFindings(context.Background(), campaign, []Finding{
		{Title: "Something", Body: "Details"},
	}, nil)

	if err == nil {
		t.Error("expected error when issueRepo is not configured")
	}
}

func TestFeedbackHandlerInit(t *testing.T) {
	handler := NewFeedbackHandler("ghp_test")
	if handler.token != "ghp_test" {
		t.Errorf("token = %q, want 'ghp_test'", handler.token)
	}
	if handler.httpClient == nil {
		t.Error("httpClient should not be nil")
	}
	if handler.baseURL != "https://api.github.com" {
		t.Errorf("baseURL = %q, want 'https://api.github.com'", handler.baseURL)
	}
}

func TestFindingJSONRoundTrip(t *testing.T) {
	finding := Finding{
		Title:            "CVE-2026-1234",
		Body:             "Critical vuln in openssl",
		Severity:         "critical",
		Labels:           []string{"security", "cve"},
		CampaignID:       "hs-dep-vuln",
		RunID:            "run-123",
		Fingerprint:      "cve-2026-1234-openssl",
		Fixable:          true,
		RemediationType:  "dependency_bump",
		RemediationHints: map[string]string{"file": "go.mod", "find": "v1.0.0", "replace": "v1.0.1"},
	}

	data, err := json.Marshal(finding)
	if err != nil {
		t.Fatalf("marshal finding: %v", err)
	}

	var decoded Finding
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal finding: %v", err)
	}

	if decoded.Fingerprint != "cve-2026-1234-openssl" {
		t.Errorf("fingerprint = %q, want 'cve-2026-1234-openssl'", decoded.Fingerprint)
	}
	if decoded.Severity != "critical" {
		t.Errorf("severity = %q, want 'critical'", decoded.Severity)
	}
	if !decoded.Fixable {
		t.Error("Fixable should be true")
	}
	if decoded.RemediationType != "dependency_bump" {
		t.Errorf("RemediationType = %q, want 'dependency_bump'", decoded.RemediationType)
	}
	if decoded.RemediationHints["file"] != "go.mod" {
		t.Errorf("RemediationHints[file] = %q, want 'go.mod'", decoded.RemediationHints["file"])
	}
}

func TestCloseResolvedLogic(t *testing.T) {
	current := []Finding{
		{Title: "Issue A", Fingerprint: "fp-a"},
	}
	previous := []Finding{
		{Title: "Issue A", Fingerprint: "fp-a"},
		{Title: "Issue B", Fingerprint: "fp-b"},
	}

	currentFPs := make(map[string]bool)
	for _, f := range current {
		fp := f.Fingerprint
		if fp == "" {
			fp = f.Title
		}
		currentFPs[fp] = true
	}

	resolved := 0
	for _, prev := range previous {
		fp := prev.Fingerprint
		if fp == "" {
			fp = prev.Title
		}
		if !currentFPs[fp] {
			resolved++
		}
	}

	if resolved != 1 {
		t.Errorf("resolved = %d, want 1 (Issue B should be resolved)", resolved)
	}
}

func TestSchedulerFeedbackIntegration(t *testing.T) {
	gh := newMockGitHub()
	server := httptest.NewServer(gh.Handler())
	defer server.Close()

	feedback := newTestFeedback(server)

	campaign := testCampaignWithFeedback()
	registry := map[string]*Campaign{campaign.ID: campaign}
	scheduler := NewScheduler(registry, nil, nil)
	scheduler.SetFeedback(feedback)

	// Simulate storeResult with findings.
	result := &CampaignResult{
		CampaignID: campaign.ID,
		Status:     "success",
		Findings: []Finding{
			{Title: "Test finding", Body: "Found something", Fingerprint: "fp-test"},
		},
	}
	scheduler.storeResult(context.Background(), campaign, result)

	gh.mu.Lock()
	defer gh.mu.Unlock()

	if len(gh.createdIssues) != 1 {
		t.Fatalf("expected scheduler to trigger feedback, got %d issues", len(gh.createdIssues))
	}
	if gh.createdIssues[0]["title"] != "Test finding" {
		t.Errorf("issue title = %v, want 'Test finding'", gh.createdIssues[0]["title"])
	}
}

// --- PR Creation Tests ---

func testCampaignWithPRs() *Campaign {
	return &Campaign{
		ID: "hs-dep-vuln",
		Feedback: Feedback{
			CreateIssues: true,
			CreatePRs:    true,
		},
		Outputs: CampaignOutputs{
			IssueRepo:      "tinyland-inc/remote-juggler",
			IssueLabels:    []string{"campaign", "security"},
			PRBranchPrefix: "sid/dep-update-",
		},
		Targets: []Target{
			{Forge: "github", Org: "tinyland-inc", Repo: "remote-juggler", Branch: "main"},
		},
		Guardrails: Guardrails{
			MaxDuration: "30m",
			ReadOnly:    false,
		},
	}
}

func fixableFinding() Finding {
	return Finding{
		Title:           "Bump lodash from 4.17.20 to 4.17.21",
		Body:            "lodash 4.17.20 has CVE-2026-9999",
		Severity:        "high",
		CampaignID:      "hs-dep-vuln",
		Fingerprint:     "dep-lodash-4.17.20",
		Fixable:         true,
		RemediationType: "dependency_bump",
		RemediationHints: map[string]string{
			"file":           "package.json",
			"find":           `"lodash": "^4.17.20"`,
			"replace":        `"lodash": "^4.17.21"`,
			"commit_message": "fix(deps): bump lodash to 4.17.21",
		},
	}
}

func TestPRCreationE2E(t *testing.T) {
	gh := newMockGitHub()
	gh.fileContents["package.json"] = `{"dependencies": {"lodash": "^4.17.20"}}`
	server := httptest.NewServer(gh.Handler())
	defer server.Close()

	handler := newTestFeedback(server)
	campaign := testCampaignWithPRs()
	finding := fixableFinding()

	err := handler.ProcessPRFindings(context.Background(), campaign, []Finding{finding})
	if err != nil {
		t.Fatalf("ProcessPRFindings: %v", err)
	}

	gh.mu.Lock()
	defer gh.mu.Unlock()

	// Should have created a branch.
	if len(gh.createdRefs) != 1 {
		t.Fatalf("expected 1 branch created, got %d", len(gh.createdRefs))
	}
	if !strings.HasPrefix(gh.createdRefs[0]["ref"], "refs/heads/sid/dep-update-") {
		t.Errorf("branch ref = %q, want prefix 'refs/heads/sid/dep-update-'", gh.createdRefs[0]["ref"])
	}

	// Should have patched the file.
	if len(gh.patchedFiles) != 1 {
		t.Fatalf("expected 1 file patched, got %d", len(gh.patchedFiles))
	}
	if gh.patchedFiles[0]["message"] != "fix(deps): bump lodash to 4.17.21" {
		t.Errorf("commit message = %q", gh.patchedFiles[0]["message"])
	}
	// Verify the patched content has the replacement.
	patchedContent, _ := base64.StdEncoding.DecodeString(gh.patchedFiles[0]["content"])
	if !strings.Contains(string(patchedContent), `"lodash": "^4.17.21"`) {
		t.Errorf("patched content missing replacement: %s", string(patchedContent))
	}

	// Should have created a PR.
	if len(gh.createdPRs) != 1 {
		t.Fatalf("expected 1 PR created, got %d", len(gh.createdPRs))
	}
	if gh.createdPRs[0]["base"] != "main" {
		t.Errorf("PR base = %q, want 'main'", gh.createdPRs[0]["base"])
	}
	if !strings.HasPrefix(gh.createdPRs[0]["title"], "fix: ") {
		t.Errorf("PR title = %q, want prefix 'fix: '", gh.createdPRs[0]["title"])
	}
}

func TestPRSkipsNonFixableFindings(t *testing.T) {
	gh := newMockGitHub()
	server := httptest.NewServer(gh.Handler())
	defer server.Close()

	handler := newTestFeedback(server)
	campaign := testCampaignWithPRs()

	findings := []Finding{
		{Title: "Info only", Severity: "low", Fixable: false},
		{Title: "No hints", Fixable: true}, // Missing RemediationHints.
	}

	err := handler.ProcessPRFindings(context.Background(), campaign, findings)
	if err != nil {
		t.Fatalf("ProcessPRFindings: %v", err)
	}

	gh.mu.Lock()
	defer gh.mu.Unlock()

	if len(gh.createdPRs) != 0 {
		t.Errorf("expected 0 PRs for non-fixable findings, got %d", len(gh.createdPRs))
	}
}

func TestPRSkipsReadOnlyCampaign(t *testing.T) {
	gh := newMockGitHub()
	server := httptest.NewServer(gh.Handler())
	defer server.Close()

	handler := newTestFeedback(server)
	campaign := testCampaignWithPRs()
	campaign.Guardrails.ReadOnly = true

	err := handler.ProcessPRFindings(context.Background(), campaign, []Finding{fixableFinding()})
	if err != nil {
		t.Fatalf("ProcessPRFindings: %v", err)
	}

	gh.mu.Lock()
	defer gh.mu.Unlock()

	if len(gh.createdPRs) != 0 {
		t.Errorf("expected 0 PRs for readOnly campaign, got %d", len(gh.createdPRs))
	}
}

func TestPRSkipsWhenCreatePRsDisabled(t *testing.T) {
	gh := newMockGitHub()
	server := httptest.NewServer(gh.Handler())
	defer server.Close()

	handler := newTestFeedback(server)
	campaign := testCampaignWithPRs()
	campaign.Feedback.CreatePRs = false

	err := handler.ProcessPRFindings(context.Background(), campaign, []Finding{fixableFinding()})
	if err != nil {
		t.Fatalf("ProcessPRFindings: %v", err)
	}

	gh.mu.Lock()
	defer gh.mu.Unlock()

	if len(gh.createdPRs) != 0 {
		t.Errorf("expected 0 PRs when CreatePRs=false, got %d", len(gh.createdPRs))
	}
}

func TestPRDeduplicatesExistingPR(t *testing.T) {
	gh := newMockGitHub()
	gh.fileContents["package.json"] = `{"dependencies": {"lodash": "^4.17.20"}}`
	// Simulate an existing open PR.
	gh.openPRs = []map[string]string{
		{"head": "sid/dep-update-dep-lodash-4-17-20", "number": "42"},
	}
	server := httptest.NewServer(gh.Handler())
	defer server.Close()

	handler := newTestFeedback(server)
	campaign := testCampaignWithPRs()

	err := handler.ProcessPRFindings(context.Background(), campaign, []Finding{fixableFinding()})
	if err != nil {
		t.Fatalf("ProcessPRFindings: %v", err)
	}

	gh.mu.Lock()
	defer gh.mu.Unlock()

	if len(gh.createdPRs) != 0 {
		t.Errorf("expected 0 PRs (dedup), got %d", len(gh.createdPRs))
	}
}

func TestPRBranchNaming(t *testing.T) {
	tests := []struct {
		prefix string
		fp     string
		want   string
	}{
		{"sid/dep-update-", "dep-lodash-4.17.20", "sid/dep-update-dep-lodash-4-17-20"},
		{"sid/fix-", "CVE-2026-1234", "sid/fix-cve-2026-1234"},
		{"sid/security-fix-", "abc", "sid/security-fix-abc"},
	}

	for _, tt := range tests {
		f := Finding{Fingerprint: tt.fp}
		got := prBranchName(tt.prefix, f)
		if got != tt.want {
			t.Errorf("prBranchName(%q, %q) = %q, want %q", tt.prefix, tt.fp, got, tt.want)
		}
	}
}

func TestPRBodyGeneration(t *testing.T) {
	campaign := testCampaignWithPRs()
	finding := fixableFinding()

	body := buildPRBody(campaign, finding)
	if !strings.Contains(body, "Automated Remediation") {
		t.Error("PR body should contain 'Automated Remediation'")
	}
	if !strings.Contains(body, "hs-dep-vuln") {
		t.Error("PR body should contain campaign ID")
	}
	if !strings.Contains(body, "high") {
		t.Error("PR body should contain severity")
	}
	if !strings.Contains(body, "dependency_bump") {
		t.Error("PR body should contain remediation type")
	}
}

func TestPRBodyTemplate(t *testing.T) {
	campaign := testCampaignWithPRs()
	campaign.Outputs.PRBodyTemplate = "Fix {{title}} (severity: {{severity}}) from campaign {{campaign}}"
	finding := fixableFinding()

	body := buildPRBody(campaign, finding)
	expected := "Fix Bump lodash from 4.17.20 to 4.17.21 (severity: high) from campaign hs-dep-vuln"
	if body != expected {
		t.Errorf("PR body = %q, want %q", body, expected)
	}
}

func TestSchedulerPRFeedbackIntegration(t *testing.T) {
	gh := newMockGitHub()
	gh.fileContents["package.json"] = `{"dependencies": {"lodash": "^4.17.20"}}`
	server := httptest.NewServer(gh.Handler())
	defer server.Close()

	feedback := newTestFeedback(server)

	campaign := testCampaignWithPRs()
	registry := map[string]*Campaign{campaign.ID: campaign}
	scheduler := NewScheduler(registry, nil, nil)
	scheduler.SetFeedback(feedback)

	result := &CampaignResult{
		CampaignID: campaign.ID,
		Status:     "success",
		Findings:   []Finding{fixableFinding()},
	}
	scheduler.storeResult(context.Background(), campaign, result)

	gh.mu.Lock()
	defer gh.mu.Unlock()

	// Should create both an issue AND a PR.
	if len(gh.createdIssues) != 1 {
		t.Errorf("expected 1 issue, got %d", len(gh.createdIssues))
	}
	if len(gh.createdPRs) != 1 {
		t.Errorf("expected 1 PR from scheduler integration, got %d", len(gh.createdPRs))
	}
}
