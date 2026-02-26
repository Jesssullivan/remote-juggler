package main

import "encoding/json"

// gatewayTools returns MCP tool definitions for tools the gateway injects
// on top of the Chapel subprocess's tools.
func gatewayTools() []json.RawMessage {
	tools := []map[string]any{
		{
			"name":        "juggler_resolve_composite",
			"description": "Resolve a secret from multiple sources (env, SOPS, KDBX, Setec) with configurable precedence. Returns the first match along with which sources were checked.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "The secret name or key to resolve (e.g. DATABASE_URL, github-token)",
					},
					"sources": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string", "enum": []string{"env", "sops", "kdbx", "setec"}},
						"description": "Sources to check, in precedence order. Defaults to gateway config precedence.",
					},
				},
				"required": []string{"query"},
			},
		},
		{
			"name":        "juggler_setec_list",
			"description": "List all secrets available in the Tailscale Setec secret store.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			"name":        "juggler_setec_get",
			"description": "Get a secret value from the Tailscale Setec secret store by name.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "The secret name (without prefix)",
					},
				},
				"required": []string{"name"},
			},
		},
		{
			"name":        "juggler_setec_put",
			"description": "Store a secret in the Tailscale Setec secret store.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "The secret name (without prefix)",
					},
					"value": map[string]any{
						"type":        "string",
						"description": "The secret value to store",
					},
				},
				"required": []string{"name", "value"},
			},
		},
		{
			"name":        "juggler_audit_log",
			"description": "View recent credential access audit log entries. Each entry shows who accessed what, from which source, and whether it was allowed.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"count": map[string]any{
						"type":        "integer",
						"description": "Number of recent entries to return (default 20, max 100)",
					},
				},
			},
		},
		{
			"name":        "juggler_campaign_status",
			"description": "Query campaign run results stored in Setec. Returns latest results for a specific campaign or lists all campaign results.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"campaign_id": map[string]any{
						"type":        "string",
						"description": "Campaign ID to query (e.g. 'oc-dep-audit'). Omit to list all campaigns.",
					},
				},
			},
		},
		{
			"name":        "juggler_aperture_usage",
			"description": "Query AI API usage metrics from Aperture. Returns token counts and call frequency, optionally filtered by campaign or agent.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"campaign_id": map[string]any{
						"type":        "string",
						"description": "Filter usage by campaign ID",
					},
					"agent": map[string]any{
						"type":        "string",
						"description": "Filter usage by agent name (openclaw, hexstrike, claude-code)",
					},
				},
			},
		},
	}

	// GitHub tools for agent self-fix workflows.
	githubTools := []map[string]any{
		{
			"name":        "github_fetch",
			"description": "Fetch a file's contents from a GitHub repository via the Contents API.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"owner": map[string]any{
						"type":        "string",
						"description": "Repository owner (user or org)",
					},
					"repo": map[string]any{
						"type":        "string",
						"description": "Repository name",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "File path within the repository",
					},
					"ref": map[string]any{
						"type":        "string",
						"description": "Git ref (branch, tag, or SHA). Defaults to the default branch.",
					},
				},
				"required": []string{"owner", "repo", "path"},
			},
		},
		{
			"name":        "github_list_alerts",
			"description": "List code scanning (CodeQL) alerts for a repository.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"owner": map[string]any{
						"type":        "string",
						"description": "Repository owner (user or org)",
					},
					"repo": map[string]any{
						"type":        "string",
						"description": "Repository name",
					},
					"state": map[string]any{
						"type":        "string",
						"description": "Alert state filter: open, closed, dismissed, fixed. Defaults to open.",
					},
					"severity": map[string]any{
						"type":        "string",
						"description": "Severity filter: critical, high, medium, low, warning, note, error",
					},
				},
				"required": []string{"owner", "repo"},
			},
		},
		{
			"name":        "github_get_alert",
			"description": "Get details for a specific code scanning alert by number.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"owner": map[string]any{
						"type":        "string",
						"description": "Repository owner (user or org)",
					},
					"repo": map[string]any{
						"type":        "string",
						"description": "Repository name",
					},
					"alert_number": map[string]any{
						"type":        "integer",
						"description": "Alert number",
					},
				},
				"required": []string{"owner", "repo", "alert_number"},
			},
		},
		{
			"name":        "github_create_branch",
			"description": "Create a new branch in a GitHub repository from a base ref.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"owner": map[string]any{
						"type":        "string",
						"description": "Repository owner (user or org)",
					},
					"repo": map[string]any{
						"type":        "string",
						"description": "Repository name",
					},
					"branch_name": map[string]any{
						"type":        "string",
						"description": "Name for the new branch",
					},
					"base": map[string]any{
						"type":        "string",
						"description": "Base branch to create from (default: main)",
					},
				},
				"required": []string{"owner", "repo", "branch_name"},
			},
		},
		{
			"name":        "github_update_file",
			"description": "Create or update a file in a GitHub repository via the Contents API.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"owner": map[string]any{
						"type":        "string",
						"description": "Repository owner (user or org)",
					},
					"repo": map[string]any{
						"type":        "string",
						"description": "Repository name",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "File path within the repository",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "New file content (plain text, will be base64-encoded)",
					},
					"message": map[string]any{
						"type":        "string",
						"description": "Commit message",
					},
					"branch": map[string]any{
						"type":        "string",
						"description": "Branch to commit to",
					},
				},
				"required": []string{"owner", "repo", "path", "content", "message", "branch"},
			},
		},
		{
			"name":        "github_create_pr",
			"description": "Create a pull request in a GitHub repository.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"owner": map[string]any{
						"type":        "string",
						"description": "Repository owner (user or org)",
					},
					"repo": map[string]any{
						"type":        "string",
						"description": "Repository name",
					},
					"title": map[string]any{
						"type":        "string",
						"description": "Pull request title",
					},
					"body": map[string]any{
						"type":        "string",
						"description": "Pull request description body",
					},
					"head": map[string]any{
						"type":        "string",
						"description": "Branch containing changes",
					},
					"base": map[string]any{
						"type":        "string",
						"description": "Branch to merge into (default: main)",
					},
				},
				"required": []string{"owner", "repo", "title", "head"},
			},
		},
		{
			"name":        "github_create_issue",
			"description": "Create an issue in a GitHub repository.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"owner": map[string]any{
						"type":        "string",
						"description": "Repository owner (user or org)",
					},
					"repo": map[string]any{
						"type":        "string",
						"description": "Repository name",
					},
					"title": map[string]any{
						"type":        "string",
						"description": "Issue title",
					},
					"body": map[string]any{
						"type":        "string",
						"description": "Issue description body (markdown)",
					},
					"labels": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Labels to apply to the issue",
					},
				},
				"required": []string{"owner", "repo", "title"},
			},
		},
		{
			"name":        "juggler_request_secret",
			"description": "Request provisioning of a new secret. Creates a labeled issue on tinyland-inc/remote-juggler for human review.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "The secret name to request (e.g. 'brave-api-key')",
					},
					"reason": map[string]any{
						"type":        "string",
						"description": "Why the secret is needed",
					},
					"urgency": map[string]any{
						"type":        "string",
						"enum":        []string{"low", "medium", "high"},
						"description": "Request urgency (default: medium)",
					},
				},
				"required": []string{"name", "reason"},
			},
		},
	}
	tools = append(tools, githubTools...)

	var raw []json.RawMessage
	for _, t := range tools {
		b, _ := json.Marshal(t)
		raw = append(raw, b)
	}
	return raw
}

// injectGatewayTools modifies a tools/list JSON-RPC response to include
// the gateway's own tools alongside the Chapel subprocess's tools.
func injectGatewayTools(response []byte) []byte {
	var msg struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  struct {
			Tools []json.RawMessage `json:"tools"`
		} `json:"result"`
	}

	if err := json.Unmarshal(response, &msg); err != nil {
		return response // pass through on parse failure
	}

	// Append gateway tools.
	msg.Result.Tools = append(msg.Result.Tools, gatewayTools()...)

	modified, err := json.Marshal(msg)
	if err != nil {
		return response
	}
	return modified
}

// gatewayOnlyToolsList constructs a tools/list response with only gateway tools.
// Used when the Chapel subprocess is unavailable or returns an error.
func gatewayOnlyToolsList(id json.RawMessage) []byte {
	msg := struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  struct {
			Tools []json.RawMessage `json:"tools"`
		} `json:"result"`
	}{
		JSONRPC: "2.0",
		ID:      id,
	}
	msg.Result.Tools = gatewayTools()
	data, _ := json.Marshal(msg)
	return data
}

// isToolsListResponse checks if a JSON-RPC response is for tools/list.
// We detect this by checking if the result has a "tools" array.
func isToolsListResponse(data []byte) bool {
	var msg struct {
		Result struct {
			Tools json.RawMessage `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		return false
	}
	return msg.Result.Tools != nil
}
