package main

import (
	"encoding/json"
	"testing"
)

func TestHTMLToTextBasic(t *testing.T) {
	html := `<html><body><h1>Hello World</h1><p>This is a test.</p></body></html>`
	got := htmlToText(html)
	if got == "" {
		t.Fatal("htmlToText returned empty string")
	}
	if !containsAll(got, "Hello World", "This is a test.") {
		t.Errorf("htmlToText output missing expected content: %q", got)
	}
}

func TestHTMLToTextStripsScripts(t *testing.T) {
	html := `<p>before</p><script>alert("xss")</script><p>after</p>`
	got := htmlToText(html)
	if containsAny(got, "alert", "xss", "script") {
		t.Errorf("htmlToText should strip scripts, got: %q", got)
	}
	if !containsAll(got, "before", "after") {
		t.Errorf("htmlToText should preserve non-script content, got: %q", got)
	}
}

func TestHTMLToTextStripsStyles(t *testing.T) {
	html := `<style>body { color: red; }</style><p>content</p>`
	got := htmlToText(html)
	if containsAny(got, "color", "red", "style") {
		t.Errorf("htmlToText should strip styles, got: %q", got)
	}
	if !containsAll(got, "content") {
		t.Errorf("htmlToText should preserve content, got: %q", got)
	}
}

func TestHTMLToTextExtractsLinks(t *testing.T) {
	html := `<a href="https://example.com">Example</a>`
	got := htmlToText(html)
	if !containsAll(got, "Example", "https://example.com") {
		t.Errorf("htmlToText should extract link text and URL, got: %q", got)
	}
}

func TestHTMLToTextDecodesEntities(t *testing.T) {
	html := `<p>Tom &amp; Jerry &lt;3&gt; &quot;quoted&quot;</p>`
	got := htmlToText(html)
	if !containsAll(got, "Tom & Jerry", `<3>`, `"quoted"`) {
		t.Errorf("htmlToText should decode entities, got: %q", got)
	}
}

func TestHTMLToTextPlainText(t *testing.T) {
	// Plain text should pass through unchanged.
	text := "Just plain text with no HTML."
	got := htmlToText(text)
	if got != text {
		t.Errorf("htmlToText(%q) = %q, want unchanged", text, got)
	}
}

func TestWebSearchArgsParsing(t *testing.T) {
	// Test that args parsing works for web_search.
	args := json.RawMessage(`{"query": "test query", "count": 3}`)
	var a struct {
		Query string `json:"query"`
		Count int    `json:"count"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if a.Query != "test query" {
		t.Errorf("query = %q, want 'test query'", a.Query)
	}
	if a.Count != 3 {
		t.Errorf("count = %d, want 3", a.Count)
	}
}

func TestWebFetchArgsParsing(t *testing.T) {
	args := json.RawMessage(`{"url": "https://example.com", "max_length": 5000}`)
	var a struct {
		URL       string `json:"url"`
		MaxLength int    `json:"max_length"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if a.URL != "https://example.com" {
		t.Errorf("url = %q, want 'https://example.com'", a.URL)
	}
	if a.MaxLength != 5000 {
		t.Errorf("max_length = %d, want 5000", a.MaxLength)
	}
}

func TestBraveSearchResultFormat(t *testing.T) {
	results := []braveSearchResult{
		{Title: "Test", URL: "https://test.com", Snippet: "A test page"},
	}
	data, err := json.Marshal(results)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var parsed []braveSearchResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(parsed) != 1 || parsed[0].Title != "Test" {
		t.Errorf("round-trip failed: %+v", parsed)
	}
}

// helpers
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !contains(s, sub) {
			return false
		}
	}
	return true
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if contains(s, sub) {
			return true
		}
	}
	return false
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
