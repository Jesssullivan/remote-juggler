package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
)

const (
	// botLoginName is the GitHub App login that we filter out to prevent loops.
	botLoginName = "rj-agent-bot[bot]"

	// conversationMaxDuration is the default timeout for conversation dispatch.
	conversationMaxDuration = 10 * time.Minute
)

// IssueCommentEvent is the GitHub issue_comment webhook payload.
type IssueCommentEvent struct {
	Action  string `json:"action"` // "created", "edited", "deleted"
	Comment struct {
		ID   int    `json:"id"`
		Body string `json:"body"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"comment"`
	Issue struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
	} `json:"issue"`
	Repository struct {
		FullName string `json:"full_name"` // "org/repo"
	} `json:"repository"`
}

// handleIssueComment processes an issue_comment webhook event.
// It dispatches a conversation if the comment mentions @rj-agent-bot.
func (a *APIServer) handleIssueComment(body []byte) {
	var event IssueCommentEvent
	if err := json.Unmarshal(body, &event); err != nil {
		log.Printf("conversation: failed to parse issue_comment event: %v", err)
		return
	}

	// Only respond to newly created comments.
	if event.Action != "created" {
		log.Printf("conversation: ignoring action=%s", event.Action)
		return
	}

	// Prevent infinite loops: ignore our own comments.
	if event.Comment.User.Login == botLoginName {
		log.Printf("conversation: ignoring own comment on %s#%d", event.Repository.FullName, event.Issue.Number)
		return
	}

	// Only respond when explicitly mentioned.
	if !strings.Contains(event.Comment.Body, "@rj-agent-bot") {
		log.Printf("conversation: no @rj-agent-bot mention in %s#%d comment %d",
			event.Repository.FullName, event.Issue.Number, event.Comment.ID)
		return
	}

	log.Printf("conversation: @rj-agent-bot mentioned in %s#%d by %s",
		event.Repository.FullName, event.Issue.Number, event.Comment.User.Login)

	// Dispatch conversation handler asynchronously.
	go a.handleConversation(event.Repository.FullName, event.Issue.Number)
}

// handleConversation fetches the issue thread, builds a synthetic campaign,
// dispatches it to TinyClaw, and posts the response as a comment.
func (a *APIServer) handleConversation(repo string, issueNumber int) {
	ctx, cancel := context.WithTimeout(context.Background(), conversationMaxDuration)
	defer cancel()

	// Refresh tokens before making GitHub API calls.
	if a.scheduler != nil {
		a.scheduler.refreshTokens()
		// Also refresh the thread fetcher's token.
		if a.scheduler.tokenProvider != nil && a.threadFetcher != nil {
			if token, err := a.scheduler.tokenProvider.Token(); err == nil {
				a.threadFetcher.UpdateToken(token)
			}
		}
	}

	// Fetch thread.
	if a.threadFetcher == nil {
		log.Printf("conversation: thread fetcher not configured")
		return
	}

	issue, err := a.threadFetcher.FetchIssueDetail(ctx, repo, issueNumber)
	if err != nil {
		log.Printf("conversation: failed to fetch issue %s#%d: %v", repo, issueNumber, err)
		return
	}

	messages, err := a.threadFetcher.FetchThread(ctx, repo, issueNumber)
	if err != nil {
		log.Printf("conversation: failed to fetch thread %s#%d: %v", repo, issueNumber, err)
		return
	}

	log.Printf("conversation: fetched %d messages from %s#%d", len(messages), repo, issueNumber)

	// Build conversation prompt.
	prompt := a.threadFetcher.FormatAsPrompt(issue, messages)

	// Build a synthetic campaign for dispatch through the existing pipeline.
	campaign := &Campaign{
		ID:          fmt.Sprintf("conv-%s-%d", sanitizeID(repo), issueNumber),
		Name:        fmt.Sprintf("Conversation: %s#%d", repo, issueNumber),
		Description: prompt,
		Agent:       "tinyclaw",
		Process: []string{
			"You are responding in a GitHub issue thread",
			"Be concise and helpful — this is a comment, not an essay",
			"Use tools to investigate when needed, then summarize findings",
		},
		Targets: []Target{{
			Forge: "github",
			Org:   strings.Split(repo, "/")[0],
			Repo:  strings.Split(repo, "/")[1],
		}},
		Guardrails: Guardrails{
			MaxDuration: "10m",
		},
		Feedback: Feedback{
			CreateIssues: false,
			CreatePRs:    false,
		},
		Outputs: CampaignOutputs{
			SetecKey: fmt.Sprintf("remotejuggler/conversations/%s/%d", sanitizeID(repo), issueNumber),
		},
	}

	// Dispatch via the scheduler (handles memory/skills, dispatcher routing, result storage).
	if err := a.scheduler.RunCampaign(ctx, campaign); err != nil {
		log.Printf("conversation: dispatch failed for %s#%d: %v", repo, issueNumber, err)
		a.postErrorComment(ctx, repo, issueNumber, err)
		return
	}

	// Get the result to extract the response text.
	a.mu.Lock()
	result, ok := a.lastResults[campaign.ID]
	a.mu.Unlock()

	if !ok || result == nil {
		log.Printf("conversation: no result stored for %s", campaign.ID)
		return
	}

	// Extract response text from findings or build from result.
	responseText := buildConversationResponse(result)
	if responseText == "" {
		log.Printf("conversation: empty response for %s#%d", repo, issueNumber)
		return
	}

	// Post the response as a comment.
	if a.scheduler.feedback == nil {
		log.Printf("conversation: feedback handler not configured, cannot post comment")
		return
	}

	if err := a.scheduler.feedback.CreateIssueComment(ctx, repo, issueNumber, responseText); err != nil {
		log.Printf("conversation: failed to post comment on %s#%d: %v", repo, issueNumber, err)
	} else {
		log.Printf("conversation: posted response on %s#%d (%d chars)", repo, issueNumber, len(responseText))
	}
}

// postErrorComment posts a brief error message if dispatch fails.
func (a *APIServer) postErrorComment(ctx context.Context, repo string, issueNumber int, dispatchErr error) {
	if a.scheduler == nil || a.scheduler.feedback == nil {
		return
	}
	msg := fmt.Sprintf("Sorry, I encountered an error processing your request.\n\n<details>\n<summary>Error details</summary>\n\n```\n%s\n```\n</details>", sanitizeString(dispatchErr.Error()))
	if err := a.scheduler.feedback.CreateIssueComment(ctx, repo, issueNumber, msg); err != nil {
		log.Printf("conversation: failed to post error comment: %v", err)
	}
}

// buildConversationResponse extracts the response text from a campaign result.
// For agentic dispatch results, the response comes from findings or KPIs.
func buildConversationResponse(result *CampaignResult) string {
	// If there are findings, format them as the response.
	if len(result.Findings) > 0 {
		var b strings.Builder
		for _, f := range result.Findings {
			if f.Title != "" {
				b.WriteString(fmt.Sprintf("### %s\n\n", f.Title))
			}
			b.WriteString(f.Body)
			b.WriteString("\n\n")
		}
		return strings.TrimSpace(b.String())
	}

	// If the agent returned text in KPIs (the agentic dispatch stores response_text).
	if result.KPIs != nil {
		if text, ok := result.KPIs["response_text"].(string); ok && text != "" {
			return stripMarkers(text)
		}
	}

	// Fall back to a summary of what the agent did.
	if result.ToolCalls > 0 {
		var b strings.Builder
		b.WriteString(fmt.Sprintf("I investigated using %d tool calls", result.ToolCalls))
		if result.Status != "success" {
			b.WriteString(fmt.Sprintf(", but encountered an issue: %s", result.Error))
		} else {
			b.WriteString(".")
		}

		// Include tool trace summary.
		if len(result.ToolTrace) > 0 {
			b.WriteString("\n\n<details>\n<summary>Tools used</summary>\n\n")
			for _, tt := range result.ToolTrace {
				prefix := "-"
				if tt.IsError {
					prefix = "- (error)"
				}
				b.WriteString(fmt.Sprintf("%s `%s`\n", prefix, tt.Tool))
			}
			b.WriteString("\n</details>")
		}
		return b.String()
	}

	return ""
}

// stripMarkers removes agent-internal marker blocks (__memory__, __findings__,
// __skill__) from text before posting as a GitHub comment. These markers are
// used for structured data extraction by the adapter/runner, not for display.
func stripMarkers(text string) string {
	pairs := [][2]string{
		{"__memory__", "__end_memory__"},
		{"__findings__", "__end_findings__"},
		{"__skill__", "__end_skill__"},
	}
	for _, pair := range pairs {
		for {
			start := strings.Index(text, pair[0])
			if start < 0 {
				break
			}
			end := strings.Index(text[start:], pair[1])
			if end < 0 {
				// Unclosed marker — strip from start marker to end of text.
				text = strings.TrimSpace(text[:start])
				break
			}
			text = text[:start] + text[start+end+len(pair[1]):]
		}
	}
	return strings.TrimSpace(text)
}

// sanitizeID replaces characters that aren't valid in Setec keys or campaign IDs.
func sanitizeID(s string) string {
	return strings.ReplaceAll(s, "/", "-")
}
