package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// ThreadFetcher fetches GitHub issue/discussion threads and formats them
// for use as conversation context in agent prompts.
type ThreadFetcher struct {
	httpClient *http.Client
	token      string
	baseURL    string
	botLogin   string // e.g. "rj-agent-bot[bot]"
}

// NewThreadFetcher creates a thread fetcher for the GitHub API.
func NewThreadFetcher(token string) *ThreadFetcher {
	return &ThreadFetcher{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		token:      token,
		baseURL:    "https://api.github.com",
		botLogin:   "rj-agent-bot[bot]",
	}
}

// GitHubIssueDetail is the subset of issue fields we need.
type GitHubIssueDetail struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"`
	User   struct {
		Login string `json:"login"`
	} `json:"user"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	CreatedAt string `json:"created_at"`
}

// GitHubComment is a single comment on an issue.
type GitHubComment struct {
	ID   int    `json:"id"`
	Body string `json:"body"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	CreatedAt string `json:"created_at"`
}

// ThreadMessage is a single message in a conversation thread.
type ThreadMessage struct {
	Role      string // "user" or "assistant"
	Author    string // GitHub login
	Body      string
	CreatedAt string
}

// FetchThread retrieves an issue and all its comments, returning them as
// an ordered list of ThreadMessages.
func (t *ThreadFetcher) FetchThread(ctx context.Context, repo string, issueNumber int) ([]ThreadMessage, error) {
	// Fetch issue body.
	issue, err := t.fetchIssue(ctx, repo, issueNumber)
	if err != nil {
		return nil, fmt.Errorf("fetch issue: %w", err)
	}

	// Fetch all comments.
	comments, err := t.fetchComments(ctx, repo, issueNumber)
	if err != nil {
		return nil, fmt.Errorf("fetch comments: %w", err)
	}

	// Build messages: issue body is the first message.
	messages := make([]ThreadMessage, 0, 1+len(comments))
	messages = append(messages, ThreadMessage{
		Role:      t.roleForUser(issue.User.Login),
		Author:    issue.User.Login,
		Body:      issue.Body,
		CreatedAt: issue.CreatedAt,
	})

	for _, c := range comments {
		messages = append(messages, ThreadMessage{
			Role:      t.roleForUser(c.User.Login),
			Author:    c.User.Login,
			Body:      c.Body,
			CreatedAt: c.CreatedAt,
		})
	}

	return messages, nil
}

// FormatAsPrompt converts thread messages into a text prompt suitable for
// inclusion in a campaign description. Includes issue metadata.
func (t *ThreadFetcher) FormatAsPrompt(issue *GitHubIssueDetail, messages []ThreadMessage) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("## GitHub Issue #%d: %s\n\n", issue.Number, issue.Title))

	if len(issue.Labels) > 0 {
		labels := make([]string, len(issue.Labels))
		for i, l := range issue.Labels {
			labels[i] = l.Name
		}
		b.WriteString(fmt.Sprintf("**Labels**: %s\n\n", strings.Join(labels, ", ")))
	}

	b.WriteString("### Conversation Thread\n\n")
	for _, m := range messages {
		b.WriteString(fmt.Sprintf("**@%s** (%s):\n%s\n\n", m.Author, m.CreatedAt, m.Body))
	}

	b.WriteString("---\n\n")
	b.WriteString("### Your Task\n\n")
	b.WriteString("Respond to the latest message in this GitHub issue thread. ")
	b.WriteString("Be helpful and concise. Use your tools when needed to answer questions or investigate. ")
	b.WriteString("Format your response as a GitHub comment (markdown).\n")

	return b.String()
}

// FetchIssueDetail retrieves the issue metadata (exported for use by conversation handler).
func (t *ThreadFetcher) FetchIssueDetail(ctx context.Context, repo string, issueNumber int) (*GitHubIssueDetail, error) {
	return t.fetchIssue(ctx, repo, issueNumber)
}

func (t *ThreadFetcher) fetchIssue(ctx context.Context, repo string, issueNumber int) (*GitHubIssueDetail, error) {
	url := fmt.Sprintf("%s/repos/%s/issues/%d", t.baseURL, repo, issueNumber)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	t.setAuth(req)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GET %s returned %d: %s", url, resp.StatusCode, string(body))
	}

	var issue GitHubIssueDetail
	if err := json.NewDecoder(resp.Body).Decode(&issue); err != nil {
		return nil, err
	}
	return &issue, nil
}

func (t *ThreadFetcher) fetchComments(ctx context.Context, repo string, issueNumber int) ([]GitHubComment, error) {
	url := fmt.Sprintf("%s/repos/%s/issues/%d/comments?per_page=100", t.baseURL, repo, issueNumber)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	t.setAuth(req)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GET %s returned %d: %s", url, resp.StatusCode, string(body))
	}

	var comments []GitHubComment
	if err := json.NewDecoder(resp.Body).Decode(&comments); err != nil {
		return nil, err
	}
	return comments, nil
}

func (t *ThreadFetcher) roleForUser(login string) string {
	if login == t.botLogin {
		return "assistant"
	}
	return "user"
}

func (t *ThreadFetcher) setAuth(req *http.Request) {
	if t.token != "" {
		req.Header.Set("Authorization", "token "+t.token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
}

// UpdateToken replaces the stored token (for App token refresh).
func (t *ThreadFetcher) UpdateToken(token string) {
	t.token = token
	log.Printf("thread fetcher: token updated")
}
