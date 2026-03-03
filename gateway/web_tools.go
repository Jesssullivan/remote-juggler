package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// WebToolHandler implements web_search and web_fetch gateway tools.
type WebToolHandler struct {
	httpClient *http.Client
	setec      *SetecClient // For fetching Brave API key from Setec.
}

// NewWebToolHandler creates a handler for web research tools.
func NewWebToolHandler(setec *SetecClient) *WebToolHandler {
	return &WebToolHandler{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		setec:      setec,
	}
}

// braveSearchResult is a single result from the Brave Search API.
type braveSearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// WebSearch calls the Brave Search API and returns formatted results.
func (h *WebToolHandler) WebSearch(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Query string `json:"query"`
		Count int    `json:"count"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}
	if a.Query == "" {
		return nil, fmt.Errorf("query is required")
	}
	if a.Count <= 0 {
		a.Count = 5
	}
	if a.Count > 20 {
		a.Count = 20
	}

	// Fetch API key from Setec.
	apiKey, err := h.setec.Get(ctx, "brave-api-key")
	if err != nil {
		return nil, fmt.Errorf("brave API key not found in Setec (key: brave-api-key): %w", err)
	}

	// Build Brave Search API request.
	u, _ := url.Parse("https://api.search.brave.com/res/v1/web/search")
	q := u.Query()
	q.Set("q", a.Query)
	q.Set("count", fmt.Sprintf("%d", a.Count))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("X-Subscription-Token", apiKey)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("brave search request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return nil, fmt.Errorf("brave search API returned %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	// Parse Brave API response.
	var braveResp struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &braveResp); err != nil {
		return nil, fmt.Errorf("parse brave response: %w", err)
	}

	// Convert to our result format.
	results := make([]braveSearchResult, 0, len(braveResp.Web.Results))
	for _, r := range braveResp.Web.Results {
		results = append(results, braveSearchResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Description,
		})
	}

	resultsJSON, _ := json.Marshal(results)
	return json.Marshal(map[string]any{
		"content": []map[string]any{{
			"type": "text",
			"text": string(resultsJSON),
		}},
	})
}

// WebFetch fetches a URL and returns its content as text.
func (h *WebToolHandler) WebFetch(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		URL       string `json:"url"`
		MaxLength int    `json:"max_length"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}
	if a.URL == "" {
		return nil, fmt.Errorf("url is required")
	}
	if a.MaxLength <= 0 {
		a.MaxLength = 10000
	}
	if a.MaxLength > 50000 {
		a.MaxLength = 50000
	}

	// Validate URL scheme.
	parsed, err := url.Parse(a.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("only http and https URLs are supported")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "RemoteJuggler-Agent/1.0 (web_fetch tool)")
	req.Header.Set("Accept", "text/html, application/xhtml+xml, text/plain, */*")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("URL returned %d", resp.StatusCode)
	}

	// Read with size limit (2x max_length to allow for HTML overhead).
	readLimit := int64(a.MaxLength * 2)
	if readLimit < 100000 {
		readLimit = 100000
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, readLimit))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	// Convert HTML to plain text (simple approach).
	text := htmlToText(string(body))

	// Truncate to max_length.
	if len(text) > a.MaxLength {
		text = text[:a.MaxLength] + "\n\n[truncated]"
	}

	return json.Marshal(map[string]any{
		"content": []map[string]any{{
			"type": "text",
			"text": text,
		}},
	})
}

// htmlToText converts HTML to readable plain text.
// This is a simple implementation that handles common cases.
func htmlToText(html string) string {
	// Remove script and style blocks.
	html = reScript.ReplaceAllString(html, "")
	html = reStyle.ReplaceAllString(html, "")

	// Convert common block elements to newlines.
	for _, tag := range []string{"br", "p", "div", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6"} {
		html = regexp.MustCompile(`(?i)</?`+tag+`[^>]*>`).ReplaceAllString(html, "\n")
	}

	// Extract href from links: <a href="URL">text</a> → text (URL)
	html = reLink.ReplaceAllString(html, "$2 ($1)")

	// Strip remaining tags.
	html = reTags.ReplaceAllString(html, "")

	// Decode common HTML entities.
	html = strings.ReplaceAll(html, "&amp;", "&")
	html = strings.ReplaceAll(html, "&lt;", "<")
	html = strings.ReplaceAll(html, "&gt;", ">")
	html = strings.ReplaceAll(html, "&quot;", `"`)
	html = strings.ReplaceAll(html, "&#39;", "'")
	html = strings.ReplaceAll(html, "&nbsp;", " ")

	// Collapse whitespace.
	html = reMultiSpace.ReplaceAllString(html, " ")
	html = reMultiNewline.ReplaceAllString(html, "\n\n")

	return strings.TrimSpace(html)
}

// Pre-compiled regexes for htmlToText.
var (
	reScript       = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	reStyle        = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	reLink         = regexp.MustCompile(`(?is)<a[^>]+href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	reTags         = regexp.MustCompile(`<[^>]+>`)
	reMultiSpace   = regexp.MustCompile(`[^\S\n]+`)
	reMultiNewline = regexp.MustCompile(`\n{3,}`)
)
