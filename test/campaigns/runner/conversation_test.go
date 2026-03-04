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

func TestBuildConversationResponse_StripsMarkers(t *testing.T) {
	result := &CampaignResult{
		Status:    "success",
		ToolCalls: 0,
		KPIs: map[string]any{
			"response_text": "Here are my tools:\n- tool1\n- tool2\n\n__memory__\nDate: 2026-03-04\nWhat I did: listed tools\n__end_memory__\n",
		},
	}

	response := buildConversationResponse(result)
	if strings.Contains(response, "__memory__") {
		t.Errorf("response still contains __memory__ marker: %s", response)
	}
	if !strings.Contains(response, "tool1") {
		t.Error("response should contain tool1")
	}
}

func TestStripMarkers(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no markers",
			input: "Hello, world!",
			want:  "Hello, world!",
		},
		{
			name:  "memory marker",
			input: "Main text\n\n__memory__\nreflection\n__end_memory__",
			want:  "Main text",
		},
		{
			name:  "findings marker",
			input: "Response\n__findings__\n{\"title\":\"x\"}\n__end_findings__\nMore text",
			want:  "Response\n\nMore text",
		},
		{
			name:  "skill marker",
			input: "Text\n__skill__\n### Skill: test\n__end_skill__",
			want:  "Text",
		},
		{
			name:  "multiple markers",
			input: "Answer\n\n__memory__\nstuff\n__end_memory__\n\n__skill__\nskill\n__end_skill__",
			want:  "Answer",
		},
		{
			name:  "unclosed marker",
			input: "Answer\n\n__memory__\nstuff without end",
			want:  "Answer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripMarkers(tt.input)
			if got != tt.want {
				t.Errorf("stripMarkers() = %q, want %q", got, tt.want)
			}
		})
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
