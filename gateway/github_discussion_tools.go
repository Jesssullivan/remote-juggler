package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// graphqlRequest is the body sent to GitHub's GraphQL API.
type graphqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

// graphqlResponse wraps the top-level GitHub GraphQL response.
type graphqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors,omitempty"`
}

// doGraphQL sends a GraphQL query and returns the data portion.
func (h *GitHubToolHandler) doGraphQL(ctx context.Context, query string, variables map[string]any) (json.RawMessage, error) {
	body, err := json.Marshal(graphqlRequest{Query: query, Variables: variables})
	if err != nil {
		return nil, fmt.Errorf("marshal graphql: %w", err)
	}

	url := h.apiBase + "/graphql"
	resp, err := h.doRequest(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read graphql response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		detail := string(respBody)
		if len(detail) > 500 {
			detail = detail[:500]
		}
		return nil, fmt.Errorf("graphql returned %d: %s", resp.StatusCode, detail)
	}

	var gqlResp graphqlResponse
	if err := json.Unmarshal(respBody, &gqlResp); err != nil {
		return nil, fmt.Errorf("unmarshal graphql response: %w", err)
	}

	if len(gqlResp.Errors) > 0 {
		msgs := make([]string, len(gqlResp.Errors))
		for i, e := range gqlResp.Errors {
			msgs[i] = e.Message
		}
		return nil, fmt.Errorf("graphql errors: %s", strings.Join(msgs, "; "))
	}

	return gqlResp.Data, nil
}

// DiscussionList lists discussions in a repository, optionally filtered by category.
func (h *GitHubToolHandler) DiscussionList(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Owner    string `json:"owner"`
		Repo     string `json:"repo"`
		Category string `json:"category"`
		First    int    `json:"first"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}
	if a.Owner == "" || a.Repo == "" {
		return nil, fmt.Errorf("owner and repo are required")
	}
	if a.First <= 0 || a.First > 100 {
		a.First = 25
	}

	// Build query with optional category filter.
	categoryFilter := ""
	if a.Category != "" {
		categoryFilter = fmt.Sprintf(`, categoryId: %q`, a.Category)
	}

	query := fmt.Sprintf(`query($owner: String!, $repo: String!, $first: Int!) {
  repository(owner: $owner, name: $repo) {
    discussions(first: $first, orderBy: {field: CREATED_AT, direction: DESC}%s) {
      nodes {
        number
        title
        author { login }
        createdAt
        updatedAt
        url
        labels(first: 10) { nodes { name } }
        category { name id }
        comments { totalCount }
      }
      totalCount
    }
  }
}`, categoryFilter)

	data, err := h.doGraphQL(ctx, query, map[string]any{
		"owner": a.Owner,
		"repo":  a.Repo,
		"first": a.First,
	})
	if err != nil {
		return mcpTextResult(map[string]any{"error": err.Error()})
	}

	var result struct {
		Repository struct {
			Discussions json.RawMessage `json:"discussions"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse discussions: %w", err)
	}

	return mcpTextResult(map[string]any{
		"discussions": json.RawMessage(result.Repository.Discussions),
	})
}

// DiscussionGet retrieves a single discussion by number.
func (h *GitHubToolHandler) DiscussionGet(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Owner  string `json:"owner"`
		Repo   string `json:"repo"`
		Number int    `json:"number"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}
	if a.Owner == "" || a.Repo == "" || a.Number <= 0 {
		return nil, fmt.Errorf("owner, repo, and number are required")
	}

	query := `query($owner: String!, $repo: String!, $number: Int!) {
  repository(owner: $owner, name: $repo) {
    discussion(number: $number) {
      id
      number
      title
      body
      author { login }
      createdAt
      updatedAt
      url
      labels(first: 20) { nodes { name id } }
      category { name id }
      comments(first: 50) {
        nodes {
          id
          body
          author { login }
          createdAt
        }
        totalCount
      }
    }
  }
}`

	data, err := h.doGraphQL(ctx, query, map[string]any{
		"owner":  a.Owner,
		"repo":   a.Repo,
		"number": a.Number,
	})
	if err != nil {
		return mcpTextResult(map[string]any{"error": err.Error()})
	}

	var result struct {
		Repository struct {
			Discussion json.RawMessage `json:"discussion"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse discussion: %w", err)
	}

	return mcpTextResult(map[string]any{
		"discussion": json.RawMessage(result.Repository.Discussion),
	})
}

// DiscussionSearch searches discussions in a repository.
func (h *GitHubToolHandler) DiscussionSearch(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
		Query string `json:"query"`
		First int    `json:"first"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}
	if a.Owner == "" || a.Repo == "" || a.Query == "" {
		return nil, fmt.Errorf("owner, repo, and query are required")
	}
	if a.First <= 0 || a.First > 100 {
		a.First = 10
	}

	// Use GitHub's search query syntax to scope to discussions in the repo.
	searchQuery := fmt.Sprintf("%s repo:%s/%s type:discussions", a.Query, a.Owner, a.Repo)

	query := `query($q: String!, $first: Int!) {
  search(query: $q, type: DISCUSSION, first: $first) {
    nodes {
      ... on Discussion {
        number
        title
        body
        author { login }
        createdAt
        url
        labels(first: 10) { nodes { name } }
        category { name }
        comments { totalCount }
      }
    }
    discussionCount
  }
}`

	data, err := h.doGraphQL(ctx, query, map[string]any{
		"q":     searchQuery,
		"first": a.First,
	})
	if err != nil {
		return mcpTextResult(map[string]any{"error": err.Error()})
	}

	var result struct {
		Search json.RawMessage `json:"search"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse search: %w", err)
	}

	return mcpTextResult(map[string]any{
		"results": json.RawMessage(result.Search),
	})
}

// DiscussionReply adds a comment to a discussion.
func (h *GitHubToolHandler) DiscussionReply(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Owner  string `json:"owner"`
		Repo   string `json:"repo"`
		Number int    `json:"number"`
		Body   string `json:"body"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}
	if a.Owner == "" || a.Repo == "" || a.Number <= 0 || a.Body == "" {
		return nil, fmt.Errorf("owner, repo, number, and body are required")
	}

	// First get the discussion's GraphQL node ID.
	idQuery := `query($owner: String!, $repo: String!, $number: Int!) {
  repository(owner: $owner, name: $repo) {
    discussion(number: $number) { id }
  }
}`
	idData, err := h.doGraphQL(ctx, idQuery, map[string]any{
		"owner":  a.Owner,
		"repo":   a.Repo,
		"number": a.Number,
	})
	if err != nil {
		return mcpTextResult(map[string]any{"error": err.Error()})
	}

	var idResult struct {
		Repository struct {
			Discussion struct {
				ID string `json:"id"`
			} `json:"discussion"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(idData, &idResult); err != nil {
		return nil, fmt.Errorf("parse discussion id: %w", err)
	}

	discussionID := idResult.Repository.Discussion.ID
	if discussionID == "" {
		return mcpTextResult(map[string]any{"error": "discussion not found"})
	}

	// Add the comment.
	mutation := `mutation($discussionId: ID!, $body: String!) {
  addDiscussionComment(input: {discussionId: $discussionId, body: $body}) {
    comment {
      id
      body
      author { login }
      createdAt
      url
    }
  }
}`

	data, err := h.doGraphQL(ctx, mutation, map[string]any{
		"discussionId": discussionID,
		"body":         a.Body,
	})
	if err != nil {
		return mcpTextResult(map[string]any{"error": err.Error()})
	}

	var result struct {
		AddDiscussionComment struct {
			Comment json.RawMessage `json:"comment"`
		} `json:"addDiscussionComment"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse reply: %w", err)
	}

	return mcpTextResult(map[string]any{
		"comment": json.RawMessage(result.AddDiscussionComment.Comment),
		"replied": true,
	})
}

// DiscussionLabel adds labels to a discussion.
func (h *GitHubToolHandler) DiscussionLabel(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Owner  string   `json:"owner"`
		Repo   string   `json:"repo"`
		Number int      `json:"number"`
		Labels []string `json:"labels"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}
	if a.Owner == "" || a.Repo == "" || a.Number <= 0 || len(a.Labels) == 0 {
		return nil, fmt.Errorf("owner, repo, number, and labels are required")
	}

	// Get the discussion's node ID and the repo's label IDs.
	lookupQuery := `query($owner: String!, $repo: String!, $number: Int!) {
  repository(owner: $owner, name: $repo) {
    id
    discussion(number: $number) { id }
    labels(first: 100) {
      nodes { id name }
    }
  }
}`

	lookupData, err := h.doGraphQL(ctx, lookupQuery, map[string]any{
		"owner":  a.Owner,
		"repo":   a.Repo,
		"number": a.Number,
	})
	if err != nil {
		return mcpTextResult(map[string]any{"error": err.Error()})
	}

	var lookup struct {
		Repository struct {
			ID         string `json:"id"`
			Discussion struct {
				ID string `json:"id"`
			} `json:"discussion"`
			Labels struct {
				Nodes []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"nodes"`
			} `json:"labels"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(lookupData, &lookup); err != nil {
		return nil, fmt.Errorf("parse lookup: %w", err)
	}

	if lookup.Repository.Discussion.ID == "" {
		return mcpTextResult(map[string]any{"error": "discussion not found"})
	}

	// Map requested label names to IDs.
	labelMap := make(map[string]string)
	for _, l := range lookup.Repository.Labels.Nodes {
		labelMap[l.Name] = l.ID
	}

	var labelIDs []string
	var missing []string
	for _, name := range a.Labels {
		if id, ok := labelMap[name]; ok {
			labelIDs = append(labelIDs, id)
		} else {
			missing = append(missing, name)
		}
	}

	if len(labelIDs) == 0 {
		return mcpTextResult(map[string]any{
			"error":          "no matching labels found",
			"missing_labels": missing,
		})
	}

	// Add labels via mutation.
	mutation := `mutation($labelableId: ID!, $labelIds: [ID!]!) {
  addLabelsToLabelable(input: {labelableId: $labelableId, labelIds: $labelIds}) {
    labelable {
      ... on Discussion {
        labels(first: 20) { nodes { name } }
      }
    }
  }
}`

	data, err := h.doGraphQL(ctx, mutation, map[string]any{
		"labelableId": lookup.Repository.Discussion.ID,
		"labelIds":    labelIDs,
	})
	if err != nil {
		return mcpTextResult(map[string]any{"error": err.Error()})
	}

	var result struct {
		AddLabelsToLabelable struct {
			Labelable struct {
				Labels struct {
					Nodes []struct {
						Name string `json:"name"`
					} `json:"nodes"`
				} `json:"labels"`
			} `json:"labelable"`
		} `json:"addLabelsToLabelable"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse label result: %w", err)
	}

	appliedLabels := make([]string, 0)
	for _, l := range result.AddLabelsToLabelable.Labelable.Labels.Nodes {
		appliedLabels = append(appliedLabels, l.Name)
	}

	resp := map[string]any{
		"labeled":        true,
		"applied_labels": appliedLabels,
	}
	if len(missing) > 0 {
		resp["missing_labels"] = missing
	}
	return mcpTextResult(resp)
}
