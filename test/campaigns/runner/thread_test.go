package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestThreadFetcher_FetchThread(t *testing.T) {
	// Mock GitHub API.
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/tinyland-inc/remote-juggler/issues/42", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(GitHubIssueDetail{
			Number: 42,
			Title:  "Help with GPG signing",
			Body:   "How do I set up GPG signing for my identity?",
			State:  "open",
			User: struct {
				Login string `json:"login"`
			}{Login: "user1"},
		})
	})
	mux.HandleFunc("/repos/tinyland-inc/remote-juggler/issues/42/comments", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]GitHubComment{
			{
				ID:   1,
				Body: "You can check with juggler_gpg_status",
				User: struct {
					Login string `json:"login"`
				}{Login: "rj-agent-bot[bot]"},
			},
			{
				ID:   2,
				Body: "@rj-agent-bot can you also check SSH keys?",
				User: struct {
					Login string `json:"login"`
				}{Login: "user1"},
			},
		})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	fetcher := NewThreadFetcher("test-token")
	fetcher.baseURL = server.URL

	messages, err := fetcher.FetchThread(context.Background(), "tinyland-inc/remote-juggler", 42)
	if err != nil {
		t.Fatalf("FetchThread failed: %v", err)
	}

	if len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(messages))
	}

	// Issue body → user role.
	if messages[0].Role != "user" {
		t.Errorf("issue body role: got %q, want user", messages[0].Role)
	}
	if messages[0].Author != "user1" {
		t.Errorf("issue body author: got %q, want user1", messages[0].Author)
	}

	// Bot comment → assistant role.
	if messages[1].Role != "assistant" {
		t.Errorf("bot comment role: got %q, want assistant", messages[1].Role)
	}
	if messages[1].Author != "rj-agent-bot[bot]" {
		t.Errorf("bot comment author: got %q", messages[1].Author)
	}

	// User follow-up → user role.
	if messages[2].Role != "user" {
		t.Errorf("user comment role: got %q, want user", messages[2].Role)
	}
}

func TestThreadFetcher_FormatAsPrompt(t *testing.T) {
	fetcher := NewThreadFetcher("test-token")

	issue := &GitHubIssueDetail{
		Number: 42,
		Title:  "GPG signing help",
		Labels: []struct {
			Name string `json:"name"`
		}{{Name: "question"}},
	}

	messages := []ThreadMessage{
		{Role: "user", Author: "user1", Body: "How do I set up GPG?", CreatedAt: "2026-03-03T10:00:00Z"},
		{Role: "assistant", Author: "rj-agent-bot[bot]", Body: "Check with juggler_gpg_status", CreatedAt: "2026-03-03T10:01:00Z"},
		{Role: "user", Author: "user1", Body: "@rj-agent-bot also check SSH", CreatedAt: "2026-03-03T10:02:00Z"},
	}

	prompt := fetcher.FormatAsPrompt(issue, messages)

	// Verify structure.
	if !strings.Contains(prompt, "## GitHub Issue #42: GPG signing help") {
		t.Error("missing issue header")
	}
	if !strings.Contains(prompt, "**Labels**: question") {
		t.Error("missing labels")
	}
	if !strings.Contains(prompt, "**@user1**") {
		t.Error("missing user mention")
	}
	if !strings.Contains(prompt, "**@rj-agent-bot[bot]**") {
		t.Error("missing bot mention")
	}
	if !strings.Contains(prompt, "### Your Task") {
		t.Error("missing task instruction")
	}
}

func TestThreadFetcher_RoleMapping(t *testing.T) {
	fetcher := NewThreadFetcher("test-token")

	tests := []struct {
		login string
		want  string
	}{
		{"user1", "user"},
		{"another-user", "user"},
		{"rj-agent-bot[bot]", "assistant"},
	}

	for _, tt := range tests {
		got := fetcher.roleForUser(tt.login)
		if got != tt.want {
			t.Errorf("roleForUser(%q) = %q, want %q", tt.login, got, tt.want)
		}
	}
}
