package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// graphqlHandler dispatches based on the query string inside the GraphQL request body.
type graphqlHandler struct {
	t        *testing.T
	handlers map[string]func(w http.ResponseWriter, vars map[string]any)
}

func (g *graphqlHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/graphql" {
		g.t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		return
	}

	var req graphqlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		g.t.Errorf("decode graphql body: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	for keyword, handler := range g.handlers {
		if strings.Contains(req.Query, keyword) {
			handler(w, req.Variables)
			return
		}
	}
	g.t.Errorf("unmatched graphql query: %s", req.Query[:min(len(req.Query), 80)])
	w.WriteHeader(http.StatusBadRequest)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// parseMCPText extracts the JSON text from an MCP tool result.
func parseMCPText(t *testing.T, result json.RawMessage) map[string]any {
	t.Helper()
	var resp map[string]any
	if err := json.Unmarshal(result, &resp); err != nil {
		t.Fatalf("unmarshal MCP response: %v", err)
	}
	contentArr, ok := resp["content"].([]any)
	if !ok || len(contentArr) == 0 {
		t.Fatal("MCP response missing content array")
	}
	text := contentArr[0].(map[string]any)["text"].(string)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("unmarshal MCP text: %v", err)
	}
	return parsed
}

// --- DiscussionList ---

func TestDiscussionList(t *testing.T) {
	gql := &graphqlHandler{t: t, handlers: map[string]func(w http.ResponseWriter, vars map[string]any){
		"discussions(first": func(w http.ResponseWriter, vars map[string]any) {
			if vars["owner"] != "tinyland-inc" || vars["repo"] != "remote-juggler" {
				t.Errorf("unexpected vars: %v", vars)
			}
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"repository": map[string]any{
						"discussions": map[string]any{
							"nodes": []map[string]any{
								{"number": 1, "title": "Test Discussion", "author": map[string]string{"login": "bot"}},
								{"number": 2, "title": "Another", "author": map[string]string{"login": "human"}},
							},
							"totalCount": 2,
						},
					},
				},
			})
		},
	}}
	server := httptest.NewServer(gql)
	defer server.Close()

	h := newTestGitHubHandler(server)
	args, _ := json.Marshal(map[string]any{
		"owner": "tinyland-inc",
		"repo":  "remote-juggler",
		"first": 10,
	})
	result, err := h.DiscussionList(context.Background(), args)
	if err != nil {
		t.Fatalf("DiscussionList: %v", err)
	}

	parsed := parseMCPText(t, result)
	disc, ok := parsed["discussions"].(map[string]any)
	if !ok {
		t.Fatalf("expected discussions map, got %T", parsed["discussions"])
	}
	nodes, ok := disc["nodes"].([]any)
	if !ok || len(nodes) != 2 {
		t.Fatalf("expected 2 discussion nodes, got %v", disc["nodes"])
	}
}

func TestDiscussionList_MissingOwner(t *testing.T) {
	h := newTestGitHubHandler(httptest.NewServer(http.NotFoundHandler()))
	args, _ := json.Marshal(map[string]any{"repo": "test"})
	_, err := h.DiscussionList(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "owner and repo are required") {
		t.Fatalf("expected owner/repo error, got: %v", err)
	}
}

func TestDiscussionList_DefaultFirst(t *testing.T) {
	var capturedFirst float64
	gql := &graphqlHandler{t: t, handlers: map[string]func(w http.ResponseWriter, vars map[string]any){
		"discussions(first": func(w http.ResponseWriter, vars map[string]any) {
			capturedFirst = vars["first"].(float64)
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"repository": map[string]any{
						"discussions": map[string]any{"nodes": []any{}, "totalCount": 0},
					},
				},
			})
		},
	}}
	server := httptest.NewServer(gql)
	defer server.Close()

	h := newTestGitHubHandler(server)
	args, _ := json.Marshal(map[string]any{
		"owner": "tinyland-inc",
		"repo":  "remote-juggler",
		// first omitted — should default to 25
	})
	_, err := h.DiscussionList(context.Background(), args)
	if err != nil {
		t.Fatalf("DiscussionList: %v", err)
	}
	if capturedFirst != 25 {
		t.Errorf("expected default first=25, got %v", capturedFirst)
	}
}

// --- DiscussionGet ---

func TestDiscussionGet(t *testing.T) {
	gql := &graphqlHandler{t: t, handlers: map[string]func(w http.ResponseWriter, vars map[string]any){
		"discussion(number": func(w http.ResponseWriter, vars map[string]any) {
			num := vars["number"].(float64)
			if num != 42 {
				t.Errorf("expected number=42, got %v", num)
			}
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"repository": map[string]any{
						"discussion": map[string]any{
							"id":     "D_kwDOXYZ",
							"number": 42,
							"title":  "Security finding",
							"body":   "Details here",
							"author": map[string]string{"login": "ironclaw"},
							"comments": map[string]any{
								"nodes":      []any{},
								"totalCount": 0,
							},
						},
					},
				},
			})
		},
	}}
	server := httptest.NewServer(gql)
	defer server.Close()

	h := newTestGitHubHandler(server)
	args, _ := json.Marshal(map[string]any{
		"owner":  "tinyland-inc",
		"repo":   "remote-juggler",
		"number": 42,
	})
	result, err := h.DiscussionGet(context.Background(), args)
	if err != nil {
		t.Fatalf("DiscussionGet: %v", err)
	}

	parsed := parseMCPText(t, result)
	disc, ok := parsed["discussion"].(map[string]any)
	if !ok {
		t.Fatalf("expected discussion map, got %T", parsed["discussion"])
	}
	if disc["title"] != "Security finding" {
		t.Errorf("unexpected title: %v", disc["title"])
	}
}

func TestDiscussionGet_MissingNumber(t *testing.T) {
	h := newTestGitHubHandler(httptest.NewServer(http.NotFoundHandler()))
	args, _ := json.Marshal(map[string]any{"owner": "x", "repo": "y"})
	_, err := h.DiscussionGet(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "owner, repo, and number are required") {
		t.Fatalf("expected validation error, got: %v", err)
	}
}

// --- DiscussionSearch ---

func TestDiscussionSearch(t *testing.T) {
	var capturedQuery string
	gql := &graphqlHandler{t: t, handlers: map[string]func(w http.ResponseWriter, vars map[string]any){
		"search(query": func(w http.ResponseWriter, vars map[string]any) {
			capturedQuery = vars["q"].(string)
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"search": map[string]any{
						"nodes": []map[string]any{
							{"number": 5, "title": "CVE-2026-1234", "author": map[string]string{"login": "hexstrike"}},
						},
						"discussionCount": 1,
					},
				},
			})
		},
	}}
	server := httptest.NewServer(gql)
	defer server.Close()

	h := newTestGitHubHandler(server)
	args, _ := json.Marshal(map[string]any{
		"owner": "tinyland-inc",
		"repo":  "remote-juggler",
		"query": "CVE-2026",
	})
	result, err := h.DiscussionSearch(context.Background(), args)
	if err != nil {
		t.Fatalf("DiscussionSearch: %v", err)
	}

	// Verify search query includes repo scope.
	if !strings.Contains(capturedQuery, "repo:tinyland-inc/remote-juggler") {
		t.Errorf("search query missing repo scope: %s", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "CVE-2026") {
		t.Errorf("search query missing user query: %s", capturedQuery)
	}

	parsed := parseMCPText(t, result)
	results, ok := parsed["results"].(map[string]any)
	if !ok {
		t.Fatalf("expected results map, got %T", parsed["results"])
	}
	nodes := results["nodes"].([]any)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 result, got %d", len(nodes))
	}
}

// --- DiscussionReply ---

func TestDiscussionReply(t *testing.T) {
	callCount := 0
	gql := &graphqlHandler{t: t, handlers: map[string]func(w http.ResponseWriter, vars map[string]any){
		// First call: get discussion node ID.
		"discussion(number": func(w http.ResponseWriter, vars map[string]any) {
			callCount++
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"repository": map[string]any{
						"discussion": map[string]any{"id": "D_kwDOABC"},
					},
				},
			})
		},
		// Second call: addDiscussionComment mutation.
		"addDiscussionComment": func(w http.ResponseWriter, vars map[string]any) {
			callCount++
			if vars["discussionId"] != "D_kwDOABC" {
				t.Errorf("unexpected discussionId: %v", vars["discussionId"])
			}
			if vars["body"] != "This is a reply" {
				t.Errorf("unexpected body: %v", vars["body"])
			}
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"addDiscussionComment": map[string]any{
						"comment": map[string]any{
							"id":   "DC_kwDOXYZ",
							"body": vars["body"],
							"author": map[string]string{
								"login": "rj-agent-bot",
							},
						},
					},
				},
			})
		},
	}}
	server := httptest.NewServer(gql)
	defer server.Close()

	h := newTestGitHubHandler(server)
	args, _ := json.Marshal(map[string]any{
		"owner":  "tinyland-inc",
		"repo":   "remote-juggler",
		"number": 10,
		"body":   "This is a reply",
	})
	result, err := h.DiscussionReply(context.Background(), args)
	if err != nil {
		t.Fatalf("DiscussionReply: %v", err)
	}

	if callCount != 2 {
		t.Errorf("expected 2 GraphQL calls, got %d", callCount)
	}

	parsed := parseMCPText(t, result)
	if parsed["replied"] != true {
		t.Errorf("expected replied=true, got %v", parsed["replied"])
	}
	comment, ok := parsed["comment"].(map[string]any)
	if !ok {
		t.Fatalf("expected comment map, got %T", parsed["comment"])
	}
	if comment["body"] != "This is a reply" {
		t.Errorf("unexpected comment body: %v", comment["body"])
	}
}

func TestDiscussionReply_NotFound(t *testing.T) {
	gql := &graphqlHandler{t: t, handlers: map[string]func(w http.ResponseWriter, vars map[string]any){
		"discussion(number": func(w http.ResponseWriter, vars map[string]any) {
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"repository": map[string]any{
						"discussion": map[string]any{"id": ""},
					},
				},
			})
		},
	}}
	server := httptest.NewServer(gql)
	defer server.Close()

	h := newTestGitHubHandler(server)
	args, _ := json.Marshal(map[string]any{
		"owner":  "tinyland-inc",
		"repo":   "remote-juggler",
		"number": 999,
		"body":   "hello",
	})
	result, err := h.DiscussionReply(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	parsed := parseMCPText(t, result)
	if parsed["error"] != "discussion not found" {
		t.Errorf("expected 'discussion not found' error, got: %v", parsed["error"])
	}
}

// --- DiscussionLabel ---

func TestDiscussionLabel(t *testing.T) {
	callCount := 0
	gql := &graphqlHandler{t: t, handlers: map[string]func(w http.ResponseWriter, vars map[string]any){
		// First call: lookup discussion + label IDs.
		"labels(first: 100)": func(w http.ResponseWriter, vars map[string]any) {
			callCount++
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"repository": map[string]any{
						"id": "R_kgDOXYZ",
						"discussion": map[string]any{
							"id": "D_kwDOABC",
						},
						"labels": map[string]any{
							"nodes": []map[string]any{
								{"id": "LA_123", "name": "severity:high"},
								{"id": "LA_456", "name": "agent:hexstrike"},
								{"id": "LA_789", "name": "handoff:pending"},
							},
						},
					},
				},
			})
		},
		// Second call: addLabelsToLabelable mutation.
		"addLabelsToLabelable": func(w http.ResponseWriter, vars map[string]any) {
			callCount++
			labelIDs := vars["labelIds"].([]any)
			if len(labelIDs) != 2 {
				t.Errorf("expected 2 label IDs, got %d", len(labelIDs))
			}
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"addLabelsToLabelable": map[string]any{
						"labelable": map[string]any{
							"labels": map[string]any{
								"nodes": []map[string]any{
									{"name": "severity:high"},
									{"name": "agent:hexstrike"},
								},
							},
						},
					},
				},
			})
		},
	}}
	server := httptest.NewServer(gql)
	defer server.Close()

	h := newTestGitHubHandler(server)
	args, _ := json.Marshal(map[string]any{
		"owner":  "tinyland-inc",
		"repo":   "remote-juggler",
		"number": 5,
		"labels": []string{"severity:high", "agent:hexstrike"},
	})
	result, err := h.DiscussionLabel(context.Background(), args)
	if err != nil {
		t.Fatalf("DiscussionLabel: %v", err)
	}

	if callCount != 2 {
		t.Errorf("expected 2 GraphQL calls, got %d", callCount)
	}

	parsed := parseMCPText(t, result)
	if parsed["labeled"] != true {
		t.Errorf("expected labeled=true, got %v", parsed["labeled"])
	}
	labels := parsed["applied_labels"].([]any)
	if len(labels) != 2 {
		t.Errorf("expected 2 applied labels, got %d", len(labels))
	}
}

func TestDiscussionLabel_MissingLabels(t *testing.T) {
	gql := &graphqlHandler{t: t, handlers: map[string]func(w http.ResponseWriter, vars map[string]any){
		"labels(first: 100)": func(w http.ResponseWriter, vars map[string]any) {
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"repository": map[string]any{
						"id":         "R_kgDOXYZ",
						"discussion": map[string]any{"id": "D_kwDOABC"},
						"labels": map[string]any{
							"nodes": []map[string]any{
								{"id": "LA_123", "name": "severity:high"},
							},
						},
					},
				},
			})
		},
	}}
	server := httptest.NewServer(gql)
	defer server.Close()

	h := newTestGitHubHandler(server)
	args, _ := json.Marshal(map[string]any{
		"owner":  "tinyland-inc",
		"repo":   "remote-juggler",
		"number": 5,
		"labels": []string{"nonexistent-label"},
	})
	result, err := h.DiscussionLabel(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}

	parsed := parseMCPText(t, result)
	if parsed["error"] != "no matching labels found" {
		t.Errorf("expected 'no matching labels found', got: %v", parsed["error"])
	}
	missing := parsed["missing_labels"].([]any)
	if len(missing) != 1 || missing[0] != "nonexistent-label" {
		t.Errorf("unexpected missing_labels: %v", missing)
	}
}

// --- GraphQL error handling ---

func TestDiscussionGraphQLError(t *testing.T) {
	gql := &graphqlHandler{t: t, handlers: map[string]func(w http.ResponseWriter, vars map[string]any){
		"discussions(first": func(w http.ResponseWriter, vars map[string]any) {
			json.NewEncoder(w).Encode(map[string]any{
				"data":   nil,
				"errors": []map[string]string{{"message": "Could not resolve to a Repository"}},
			})
		},
	}}
	server := httptest.NewServer(gql)
	defer server.Close()

	h := newTestGitHubHandler(server)
	args, _ := json.Marshal(map[string]any{
		"owner": "nonexistent",
		"repo":  "nope",
	})
	result, err := h.DiscussionList(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}

	// GraphQL errors are returned in the MCP text, not as Go errors.
	parsed := parseMCPText(t, result)
	errMsg, ok := parsed["error"].(string)
	if !ok || !strings.Contains(errMsg, "Could not resolve") {
		t.Errorf("expected GraphQL error in response, got: %v", parsed)
	}
}

func TestDiscussionGraphQLHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	defer server.Close()

	h := newTestGitHubHandler(server)
	args, _ := json.Marshal(map[string]any{
		"owner": "tinyland-inc",
		"repo":  "remote-juggler",
	})
	result, err := h.DiscussionList(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}

	parsed := parseMCPText(t, result)
	errMsg, ok := parsed["error"].(string)
	if !ok || !strings.Contains(errMsg, "401") {
		t.Errorf("expected 401 error, got: %v", parsed)
	}
}
