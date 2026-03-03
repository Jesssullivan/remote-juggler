package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHandleIssueComment_IgnoresOwnComments(t *testing.T) {
	event := IssueCommentEvent{
		Action: "created",
	}
	event.Comment.Body = "@rj-agent-bot hello"
	event.Comment.User.Login = "rj-agent-bot[bot]"
	event.Issue.Number = 1
	event.Repository.FullName = "tinyland-inc/remote-juggler"

	body, _ := json.Marshal(event)

	// Create API server with no scheduler (will log and return, not panic).
	api := &APIServer{
		lastResults: make(map[string]*CampaignResult),
	}

	// Should not panic — it detects the bot login and returns early.
	api.handleIssueComment(body)
}

func TestHandleIssueComment_IgnoresNonCreatedActions(t *testing.T) {
	event := IssueCommentEvent{
		Action: "edited",
	}
	event.Comment.Body = "@rj-agent-bot hello"
	event.Comment.User.Login = "user1"
	event.Issue.Number = 1
	event.Repository.FullName = "tinyland-inc/remote-juggler"

	body, _ := json.Marshal(event)

	api := &APIServer{
		lastResults: make(map[string]*CampaignResult),
	}

	// Should not dispatch — action is "edited", not "created".
	api.handleIssueComment(body)
}

func TestHandleIssueComment_IgnoresNoMention(t *testing.T) {
	event := IssueCommentEvent{
		Action: "created",
	}
	event.Comment.Body = "just a regular comment"
	event.Comment.User.Login = "user1"
	event.Issue.Number = 1
	event.Repository.FullName = "tinyland-inc/remote-juggler"

	body, _ := json.Marshal(event)

	api := &APIServer{
		lastResults: make(map[string]*CampaignResult),
	}

	// Should not dispatch — no @rj-agent-bot mention.
	api.handleIssueComment(body)
}

func TestBuildConversationResponse_FromFindings(t *testing.T) {
	result := &CampaignResult{
		Status:    "success",
		ToolCalls: 3,
		Findings: []Finding{
			{Title: "GPG Status", Body: "Your GPG key is configured correctly."},
			{Title: "SSH Status", Body: "SSH key `id_ed25519` is active."},
		},
	}

	response := buildConversationResponse(result)

	if !strings.Contains(response, "### GPG Status") {
		t.Error("missing first finding title")
	}
	if !strings.Contains(response, "### SSH Status") {
		t.Error("missing second finding title")
	}
	if !strings.Contains(response, "GPG key is configured correctly") {
		t.Error("missing first finding body")
	}
}

func TestBuildConversationResponse_FromKPIs(t *testing.T) {
	result := &CampaignResult{
		Status:    "success",
		ToolCalls: 1,
		KPIs: map[string]any{
			"response_text": "Here is your answer.",
		},
	}

	response := buildConversationResponse(result)
	if response != "Here is your answer." {
		t.Errorf("expected KPI response, got %q", response)
	}
}

func TestBuildConversationResponse_FallbackToolSummary(t *testing.T) {
	result := &CampaignResult{
		Status:    "success",
		ToolCalls: 5,
		ToolTrace: []ToolTraceEntry{
			{Tool: "juggler_gpg_status", Summary: "Called juggler_gpg_status"},
			{Tool: "web_search", Summary: "Called web_search"},
		},
	}

	response := buildConversationResponse(result)

	if !strings.Contains(response, "5 tool calls") {
		t.Error("missing tool call count")
	}
	if !strings.Contains(response, "`juggler_gpg_status`") {
		t.Error("missing tool name in trace")
	}
}

func TestBuildConversationResponse_Empty(t *testing.T) {
	result := &CampaignResult{
		Status:    "success",
		ToolCalls: 0,
	}

	response := buildConversationResponse(result)
	if response != "" {
		t.Errorf("expected empty response, got %q", response)
	}
}

func TestSanitizeID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"tinyland-inc/remote-juggler", "tinyland-inc-remote-juggler"},
		{"simple", "simple"},
		{"a/b/c", "a-b-c"},
	}

	for _, tt := range tests {
		got := sanitizeID(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
