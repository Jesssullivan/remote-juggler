package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSanitizeString_SecretPatterns(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"token is ghp_abc123def456", "token is [REDACTED]abc123def456"},
		{"key: sk-ant-api03-xyz", "key: [REDACTED]ant-api03-xyz"},
		{"AKIAIOSFODNN7EXAMPLE", "[REDACTED]OSFODNN7EXAMPLE"},
		{"-----BEGIN RSA PRIVATE KEY-----", "[REDACTED] RSA PRIVATE KEY-----"},
		{"safe string with no secrets", "safe string with no secrets"},
		{"ghs_token123 and gho_other456", "[REDACTED]token123 and [REDACTED]other456"},
		{"github_pat_11ABC secret", "[REDACTED]11ABC secret"},
	}

	for _, tt := range tests {
		got := sanitizeString(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeString(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSanitizeString_InternalURLs(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{
			"rj-gateway.fuzzy-dev.svc.cluster.local:8080",
			"[internal]",
		},
		{
			"setec.taila4c78d.ts.net",
			"[internal]",
		},
		{
			"http://rj-gateway.fuzzy-dev.svc.cluster.local:8080/mcp",
			"http://[internal]/mcp",
		},
		{
			"public.example.com stays",
			"public.example.com stays",
		},
	}

	for _, tt := range tests {
		got := sanitizeString(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeString(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestShannonEntropy(t *testing.T) {
	tests := []struct {
		input   string
		minBits float64
		maxBits float64
	}{
		{"aaaa", 0, 0.1},               // Low entropy
		{"abcdefghijklmnop", 3.5, 4.5}, // Medium entropy
		{"aB3$kL9!xZ2@mN7#", 4.0, 5.0}, // High entropy (secret-like)
		{"", 0, 0},                     // Empty
		{"ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ012345", 4.0, 6.0}, // Token-like
	}

	for _, tt := range tests {
		got := shannonEntropy(tt.input)
		if got < tt.minBits || got > tt.maxBits {
			t.Errorf("shannonEntropy(%q) = %.2f, want [%.1f, %.1f]",
				tt.input, got, tt.minBits, tt.maxBits)
		}
	}
}

func TestSanitizeValue_HighEntropy(t *testing.T) {
	// A high-entropy string (>4.5 bits/char, >8 chars) should be redacted.
	// This simulates a token or API key with high character diversity.
	highEntropy := "aB3kL9xZ2mN7pQ4rS6tU8vW0yA1cE5fG"
	result := sanitizeValue(highEntropy)
	if result != "[REDACTED]" {
		e := shannonEntropy(highEntropy)
		t.Errorf("expected high-entropy string (%.2f bits) to be redacted, got %v", e, result)
	}

	// Numeric values should pass through.
	if sanitizeValue(42.0) != 42.0 {
		t.Error("numeric value should pass through")
	}
	if sanitizeValue(true) != true {
		t.Error("boolean value should pass through")
	}

	// Short strings should not be entropy-filtered.
	if sanitizeValue("ok") != "ok" {
		t.Error("short string should pass through")
	}
}

func TestFormatTitle(t *testing.T) {
	p := &Publisher{}
	campaign := &Campaign{Name: "Gateway Health Check"}
	result := &CampaignResult{
		Status:     "success",
		FinishedAt: "2026-02-25T06:00:00Z",
	}

	title := p.formatTitle(campaign, result)
	if !strings.Contains(title, "[PASS]") {
		t.Errorf("expected [PASS] in title, got %q", title)
	}
	if !strings.Contains(title, "Gateway Health Check") {
		t.Errorf("expected campaign name in title, got %q", title)
	}
	if !strings.Contains(title, "2026-02-25") {
		t.Errorf("expected date in title, got %q", title)
	}
}

func TestFormatBody(t *testing.T) {
	p := &Publisher{repoOwner: "tinyland-inc", repoName: "remote-juggler"}
	campaign := &Campaign{
		ID:   "oc-dep-audit",
		Name: "Cross-Repo Dependency Audit",
	}
	result := &CampaignResult{
		RunID:      "oc-dep-audit-1740456000",
		Agent:      "openclaw",
		Status:     "success",
		StartedAt:  "2026-02-25T06:00:00Z",
		FinishedAt: "2026-02-25T06:02:34Z",
		ToolCalls:  24,
		KPIs: map[string]any{
			"repos_scanned":       10.0,
			"version_divergences": 7.0,
		},
	}

	body := p.formatBody(campaign, result)

	checks := []string{
		"## Campaign: Cross-Repo Dependency Audit",
		"**Run**: `oc-dep-audit-1740456000`",
		"**Agent**: openclaw",
		"**Tool Calls**: 24",
		"PASS",
		"### KPIs",
		"repos_scanned",
	}
	for _, check := range checks {
		if !strings.Contains(body, check) {
			t.Errorf("expected body to contain %q", check)
		}
	}

	// Ensure no internal URLs leak.
	if strings.Contains(body, ".svc.cluster.local") {
		t.Error("body should not contain internal URLs")
	}
}

func TestFormatBody_SanitizesSecrets(t *testing.T) {
	p := &Publisher{repoOwner: "test", repoName: "test"}
	campaign := &Campaign{ID: "test", Name: "Test"}
	result := &CampaignResult{
		RunID:      "test-1",
		Agent:      "test",
		Status:     "failure",
		Error:      "auth failed with ghp_secret123 at rj-gateway.fuzzy-dev.svc.cluster.local:8080",
		StartedAt:  "2026-01-01T00:00:00Z",
		FinishedAt: "2026-01-01T00:01:00Z",
	}

	body := p.formatBody(campaign, result)

	if strings.Contains(body, "ghp_") {
		t.Error("body should not contain ghp_ token prefix")
	}
	if strings.Contains(body, ".svc.cluster.local") {
		t.Error("body should not contain internal URLs")
	}
}

func TestFormatBody_ToolTrace(t *testing.T) {
	p := &Publisher{repoOwner: "tinyland-inc", repoName: "remote-juggler"}
	campaign := &Campaign{
		ID:   "oc-codeql-fix",
		Name: "CodeQL Alert Auto-Fix",
	}
	result := &CampaignResult{
		RunID:      "oc-codeql-fix-1740456000",
		Agent:      "openclaw",
		Status:     "success",
		StartedAt:  "2026-02-25T06:00:00Z",
		FinishedAt: "2026-02-25T06:05:00Z",
		ToolCalls:  5,
		KPIs:       map[string]any{"alerts_fixed": 3.0},
		ToolTrace: []ToolTraceEntry{
			{Timestamp: "2026-02-25T06:00:01Z", Tool: "juggler_resolve_composite", Summary: "query=github-token, source=setec"},
			{Timestamp: "2026-02-25T06:00:03Z", Tool: "github_list_alerts", Summary: "25 open alerts"},
			{Timestamp: "2026-02-25T06:00:10Z", Tool: "github_update_file", Summary: "commit fix", IsError: false},
			{Timestamp: "2026-02-25T06:00:15Z", Tool: "github_create_pr", Summary: "failed: rate limit", IsError: true},
		},
	}

	body := p.formatBody(campaign, result)

	checks := []string{
		"<details>",
		"4 tool calls",
		"expand trace",
		"| Time | Tool | Summary |",
		"`juggler_resolve_composite`",
		"`github_list_alerts`",
		"25 open alerts",
		"**ERROR**: failed: rate limit",
		"</details>",
	}
	for _, check := range checks {
		if !strings.Contains(body, check) {
			t.Errorf("expected body to contain %q", check)
		}
	}
}

func TestFormatBody_NoToolTrace(t *testing.T) {
	p := &Publisher{repoOwner: "test", repoName: "test"}
	campaign := &Campaign{ID: "test", Name: "Test"}
	result := &CampaignResult{
		RunID:      "test-1",
		Agent:      "test",
		Status:     "success",
		StartedAt:  "2026-01-01T00:00:00Z",
		FinishedAt: "2026-01-01T00:01:00Z",
	}

	body := p.formatBody(campaign, result)

	if strings.Contains(body, "<details>") {
		t.Error("body should not contain tool trace details when ToolTrace is empty")
	}
}

func TestFormatBody_ToolTraceSanitizesSecrets(t *testing.T) {
	p := &Publisher{repoOwner: "test", repoName: "test"}
	campaign := &Campaign{ID: "test", Name: "Test"}
	result := &CampaignResult{
		RunID:      "test-1",
		Agent:      "test",
		Status:     "success",
		StartedAt:  "2026-01-01T00:00:00Z",
		FinishedAt: "2026-01-01T00:01:00Z",
		ToolTrace: []ToolTraceEntry{
			{Timestamp: "2026-01-01T00:00:01Z", Tool: "juggler_resolve_composite", Summary: "resolved ghp_secret123 at rj-gateway.fuzzy-dev.svc.cluster.local:8080"},
		},
	}

	body := p.formatBody(campaign, result)

	if strings.Contains(body, "ghp_") {
		t.Error("tool trace should not contain ghp_ token prefix")
	}
	if strings.Contains(body, ".svc.cluster.local") {
		t.Error("tool trace should not contain internal URLs")
	}
}

func TestCategoryForCampaign(t *testing.T) {
	p := &Publisher{}

	tests := []struct {
		campaign *Campaign
		want     string
	}{
		{&Campaign{ID: "oc-weekly-digest", Agent: "openclaw"}, "Weekly Digest"},
		{&Campaign{ID: "hs-cred-exposure", Agent: "hexstrike"}, "Security Advisories"},
		{&Campaign{ID: "oc-dep-audit", Agent: "openclaw"}, "Agent Reports"},
		{&Campaign{ID: "cc-gateway-health", Agent: "gateway-direct"}, "Agent Reports"},
	}

	for _, tt := range tests {
		got := p.categoryForCampaign(tt.campaign)
		if got != tt.want {
			t.Errorf("categoryForCampaign(%s) = %q, want %q", tt.campaign.ID, got, tt.want)
		}
	}
}

func TestPublisherInit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"repository": map[string]any{
					"id": "R_test123",
					"discussionCategories": map[string]any{
						"nodes": []map[string]string{
							{"id": "DC_reports", "name": "Agent Reports"},
							{"id": "DC_digest", "name": "Weekly Digest"},
							{"id": "DC_security", "name": "Security Advisories"},
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	p := NewPublisher("test-token", "test-owner", "test-repo")
	p.baseURL = server.URL

	if err := p.Init(context.Background()); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if p.repoID != "R_test123" {
		t.Errorf("repoID = %q, want R_test123", p.repoID)
	}
	if len(p.categoryIDs) != 3 {
		t.Errorf("expected 3 categories, got %d", len(p.categoryIDs))
	}
	if p.categoryIDs["Agent Reports"] != "DC_reports" {
		t.Errorf("Agent Reports category ID = %q, want DC_reports", p.categoryIDs["Agent Reports"])
	}
}

func TestPublisherPublish(t *testing.T) {
	var gotMutation string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := json.Marshal(map[string]any{"query": ""})
		raw, _ := json.Marshal(r.Body)
		_ = raw

		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		gotMutation = req.Query

		w.Header().Set("Content-Type", "application/json")

		if strings.Contains(req.Query, "createDiscussion") {
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"createDiscussion": map[string]any{
						"discussion": map[string]any{
							"url":    "https://github.com/test/repo/discussions/42",
							"number": 42,
						},
					},
				},
			})
		} else {
			w.Write(body)
		}
	}))
	defer server.Close()

	p := NewPublisher("test-token", "test-owner", "test-repo")
	p.baseURL = server.URL
	p.repoID = "R_test123"
	p.categoryIDs = map[string]string{
		"Agent Reports": "DC_reports",
	}

	campaign := &Campaign{
		ID:   "oc-dep-audit",
		Name: "Dependency Audit",
	}
	result := &CampaignResult{
		RunID:      "oc-dep-audit-123",
		Agent:      "openclaw",
		Status:     "success",
		StartedAt:  "2026-01-01T00:00:00Z",
		FinishedAt: "2026-01-01T00:01:00Z",
		ToolCalls:  10,
	}

	url, err := p.Publish(context.Background(), campaign, result)
	if err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	if url != "https://github.com/test/repo/discussions/42" {
		t.Errorf("unexpected URL: %s", url)
	}

	if !strings.Contains(gotMutation, "createDiscussion") {
		t.Error("expected createDiscussion mutation")
	}
}
