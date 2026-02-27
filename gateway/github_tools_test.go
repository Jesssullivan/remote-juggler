package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestGitHubHandler(server *httptest.Server) *GitHubToolHandler {
	h := NewGitHubToolHandler(func(ctx context.Context) (string, error) {
		return "test-token-123", nil
	})
	h.apiBase = server.URL
	return h
}

func TestGitHubFetch(t *testing.T) {
	content := "package main\n\nfunc main() {}\n"
	encoded := base64.StdEncoding.EncodeToString([]byte(content))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/tinyland-inc/remote-juggler/contents/main.go" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token-123" {
			t.Errorf("missing auth header")
		}
		json.NewEncoder(w).Encode(map[string]any{
			"content":  encoded,
			"encoding": "base64",
			"sha":      "abc123",
			"size":     len(content),
			"path":     "main.go",
		})
	}))
	defer server.Close()

	h := newTestGitHubHandler(server)
	args, _ := json.Marshal(map[string]string{
		"owner": "tinyland-inc",
		"repo":  "remote-juggler",
		"path":  "main.go",
	})

	result, err := h.Fetch(context.Background(), args)
	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}

	var resp map[string]any
	json.Unmarshal(result, &resp)
	contentArr := resp["content"].([]any)
	text := contentArr[0].(map[string]any)["text"].(string)
	if text == "" {
		t.Error("expected non-empty text content")
	}
}

func TestGitHubFetch_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer server.Close()

	h := newTestGitHubHandler(server)
	args, _ := json.Marshal(map[string]string{
		"owner": "tinyland-inc",
		"repo":  "remote-juggler",
		"path":  "nonexistent.go",
	})

	result, err := h.Fetch(context.Background(), args)
	if err != nil {
		t.Fatalf("Fetch should not error on 404: %v", err)
	}
	// Should return error in the MCP result, not a Go error.
	var resp map[string]any
	json.Unmarshal(result, &resp)
	contentArr := resp["content"].([]any)
	text := contentArr[0].(map[string]any)["text"].(string)
	if text == "" {
		t.Error("expected error detail in text")
	}
}

func TestGitHubListAlerts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/tinyland-inc/remote-juggler/code-scanning/alerts" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode([]map[string]any{
			{"number": 1, "rule": map[string]string{"id": "go/sql-injection"}},
			{"number": 2, "rule": map[string]string{"id": "go/xss"}},
		})
	}))
	defer server.Close()

	h := newTestGitHubHandler(server)
	args, _ := json.Marshal(map[string]string{
		"owner": "tinyland-inc",
		"repo":  "remote-juggler",
	})

	result, err := h.ListAlerts(context.Background(), args)
	if err != nil {
		t.Fatalf("ListAlerts error: %v", err)
	}

	var resp map[string]any
	json.Unmarshal(result, &resp)
	contentArr := resp["content"].([]any)
	text := contentArr[0].(map[string]any)["text"].(string)

	var parsed map[string]any
	json.Unmarshal([]byte(text), &parsed)
	count := parsed["count"].(float64)
	if count != 2 {
		t.Errorf("expected 2 alerts, got %v", count)
	}
}

func TestGitHubListAlerts_Empty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{})
	}))
	defer server.Close()

	h := newTestGitHubHandler(server)
	args, _ := json.Marshal(map[string]string{
		"owner": "tinyland-inc",
		"repo":  "remote-juggler",
	})

	result, err := h.ListAlerts(context.Background(), args)
	if err != nil {
		t.Fatalf("ListAlerts error: %v", err)
	}

	var resp map[string]any
	json.Unmarshal(result, &resp)
	contentArr := resp["content"].([]any)
	text := contentArr[0].(map[string]any)["text"].(string)

	var parsed map[string]any
	json.Unmarshal([]byte(text), &parsed)
	count := parsed["count"].(float64)
	if count != 0 {
		t.Errorf("expected 0 alerts, got %v", count)
	}
}

func TestGitHubGetAlert(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/tinyland-inc/remote-juggler/code-scanning/alerts/42" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"number": 42,
			"state":  "open",
			"rule":   map[string]string{"id": "go/sql-injection", "severity": "error"},
		})
	}))
	defer server.Close()

	h := newTestGitHubHandler(server)
	args, _ := json.Marshal(map[string]any{
		"owner":        "tinyland-inc",
		"repo":         "remote-juggler",
		"alert_number": 42,
	})

	result, err := h.GetAlert(context.Background(), args)
	if err != nil {
		t.Fatalf("GetAlert error: %v", err)
	}

	var resp map[string]any
	json.Unmarshal(result, &resp)
	if resp["content"] == nil {
		t.Error("expected content in response")
	}
}

func TestGitHubCreateBranch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/tinyland-inc/remote-juggler/git/ref/heads/main":
			json.NewEncoder(w).Encode(map[string]any{
				"object": map[string]string{"sha": "abc123def456"},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/tinyland-inc/remote-juggler/git/refs":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{
				"ref":    "refs/heads/sid/codeql-fix-1",
				"object": map[string]string{"sha": "abc123def456"},
			})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	h := newTestGitHubHandler(server)
	args, _ := json.Marshal(map[string]string{
		"owner":       "tinyland-inc",
		"repo":        "remote-juggler",
		"branch_name": "sid/codeql-fix-1",
	})

	result, err := h.CreateBranch(context.Background(), args)
	if err != nil {
		t.Fatalf("CreateBranch error: %v", err)
	}

	var resp map[string]any
	json.Unmarshal(result, &resp)
	contentArr := resp["content"].([]any)
	text := contentArr[0].(map[string]any)["text"].(string)

	var parsed map[string]any
	json.Unmarshal([]byte(text), &parsed)
	if parsed["created"] != true {
		t.Errorf("expected created=true, got %v", parsed["created"])
	}
	if parsed["branch"] != "sid/codeql-fix-1" {
		t.Errorf("unexpected branch: %v", parsed["branch"])
	}
}

func TestGitHubUpdateFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(map[string]any{
				"sha": "old-sha-123",
			})
		case http.MethodPut:
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{
				"content": map[string]any{
					"sha":  "new-sha-456",
					"path": "src/main.go",
				},
			})
		}
	}))
	defer server.Close()

	h := newTestGitHubHandler(server)
	args, _ := json.Marshal(map[string]string{
		"owner":   "tinyland-inc",
		"repo":    "remote-juggler",
		"path":    "src/main.go",
		"content": "package main\n\nfunc main() { println(\"fixed\") }\n",
		"message": "fix: resolve SQL injection vulnerability",
		"branch":  "sid/codeql-fix-1",
	})

	result, err := h.UpdateFile(context.Background(), args)
	if err != nil {
		t.Fatalf("UpdateFile error: %v", err)
	}

	var resp map[string]any
	json.Unmarshal(result, &resp)
	contentArr := resp["content"].([]any)
	text := contentArr[0].(map[string]any)["text"].(string)

	var parsed map[string]any
	json.Unmarshal([]byte(text), &parsed)
	if parsed["updated"] != true {
		t.Errorf("expected updated=true, got %v", parsed["updated"])
	}
}

func TestGitHubCreatePR(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/tinyland-inc/remote-juggler/pulls" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"number":   99,
			"html_url": "https://github.com/tinyland-inc/remote-juggler/pull/99",
			"title":    "fix: resolve SQL injection",
			"state":    "open",
		})
	}))
	defer server.Close()

	h := newTestGitHubHandler(server)
	args, _ := json.Marshal(map[string]string{
		"owner": "tinyland-inc",
		"repo":  "remote-juggler",
		"title": "fix: resolve SQL injection",
		"body":  "Automated fix from CodeQL alert #42",
		"head":  "sid/codeql-fix-1",
	})

	result, err := h.CreatePR(context.Background(), args)
	if err != nil {
		t.Fatalf("CreatePR error: %v", err)
	}

	var resp map[string]any
	json.Unmarshal(result, &resp)
	contentArr := resp["content"].([]any)
	text := contentArr[0].(map[string]any)["text"].(string)

	var parsed map[string]any
	json.Unmarshal([]byte(text), &parsed)
	if parsed["created"] != true {
		t.Errorf("expected created=true, got %v", parsed["created"])
	}
	if parsed["number"].(float64) != 99 {
		t.Errorf("expected PR number 99, got %v", parsed["number"])
	}
}

func TestGitHubCreateIssue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/tinyland-inc/remote-juggler/issues" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["title"] != "Secret Request: brave-api-key" {
			t.Errorf("unexpected title: %v", body["title"])
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"number":   42,
			"html_url": "https://github.com/tinyland-inc/remote-juggler/issues/42",
			"title":    body["title"],
			"state":    "open",
		})
	}))
	defer server.Close()

	h := newTestGitHubHandler(server)
	args, _ := json.Marshal(map[string]any{
		"owner":  "tinyland-inc",
		"repo":   "remote-juggler",
		"title":  "Secret Request: brave-api-key",
		"body":   "Need brave API key for web search",
		"labels": []string{"secret-request", "medium"},
	})

	result, err := h.CreateIssue(context.Background(), args)
	if err != nil {
		t.Fatalf("CreateIssue error: %v", err)
	}

	var resp map[string]any
	json.Unmarshal(result, &resp)
	contentArr := resp["content"].([]any)
	text := contentArr[0].(map[string]any)["text"].(string)

	var parsed map[string]any
	json.Unmarshal([]byte(text), &parsed)
	if parsed["created"] != true {
		t.Errorf("expected created=true, got %v", parsed["created"])
	}
	if parsed["number"].(float64) != 42 {
		t.Errorf("expected issue number 42, got %v", parsed["number"])
	}
}

func TestGitHubRequestSecret(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/tinyland-inc/remote-juggler/issues" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)

		title, _ := body["title"].(string)
		if title != "Secret Request: brave-api-key" {
			t.Errorf("unexpected title: %v", title)
		}

		labels, _ := body["labels"].([]any)
		if len(labels) != 2 {
			t.Errorf("expected 2 labels, got %d", len(labels))
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"number":   43,
			"html_url": "https://github.com/tinyland-inc/remote-juggler/issues/43",
			"title":    title,
			"state":    "open",
		})
	}))
	defer server.Close()

	h := newTestGitHubHandler(server)
	args, _ := json.Marshal(map[string]string{
		"name":    "brave-api-key",
		"reason":  "IronClaw needs web search capability for upstream monitoring",
		"urgency": "medium",
	})

	result, err := h.RequestSecret(context.Background(), args)
	if err != nil {
		t.Fatalf("RequestSecret error: %v", err)
	}

	var resp map[string]any
	json.Unmarshal(result, &resp)
	contentArr := resp["content"].([]any)
	text := contentArr[0].(map[string]any)["text"].(string)

	var parsed map[string]any
	json.Unmarshal([]byte(text), &parsed)
	if parsed["created"] != true {
		t.Errorf("expected created=true, got %v", parsed["created"])
	}
}

func TestGitHubTokenResolution(t *testing.T) {
	tokenCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{})
	}))
	defer server.Close()

	h := NewGitHubToolHandler(func(ctx context.Context) (string, error) {
		tokenCalled = true
		return "dynamic-token", nil
	})
	h.apiBase = server.URL

	args, _ := json.Marshal(map[string]string{
		"owner": "test",
		"repo":  "test",
	})

	_, err := h.ListAlerts(context.Background(), args)
	if err != nil {
		t.Fatalf("ListAlerts error: %v", err)
	}
	if !tokenCalled {
		t.Error("expected tokenFunc to be called")
	}
}
