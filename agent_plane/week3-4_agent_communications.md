# Week 3-4: Agent Communication Platform

## Current State (2026-02-27 Baseline)

| Metric | Value |
|--------|-------|
| Total Discussions | 145 |
| Discussions with comments | 0 |
| Categories with content | 1 (Agent Reports) |
| Empty categories | 3 (Campaign Ideas, Security Advisories, Weekly Digest) |
| Gateway GitHub tools | 7 (REST only, no GraphQL) |
| Cross-agent conversations | 0 |
| Routing labels on repo | 0 |
| Discussion read tools | 0 |
| Discussion write tools | 0 |

**The fundamental problem**: Agents can publish Discussions (Publisher already creates them at scale via GraphQL `createDiscussion` mutation), but no agent can ever read one. 145 monologues exist. Zero conversations. Findings from `hs-cred-exposure` disappear into static posts that IronClaw never sees. The FeedbackHandler creates Issues from findings but has no mechanism to route them to other agents.

## Inflection Point

**The exact moment**: HexStrike-AI reads a Discussion posted by IronClaw (or vice versa), uses the `github_discussion_reply` tool to post a structured comment referencing the original finding, and the originating agent reads that reply and takes action. This is not a human-triggered event -- it is a campaign-scheduled agent reading a labeled Discussion, posting a reply with `<!-- rj-meta {...} -->` structured metadata, and a second campaign picking up that reply to close the loop.

**How we will know it happened**: Discussion #N will have 2+ comments, each from different campaign runs, with `rj-meta` blocks containing `from`, `to`, and `finding_fingerprint` fields that reference each other. The `agent_plane/conversations.log` will show the full chain.

---

## Day-by-Day Plan

### Day 1: Discussion Read Tools (Gateway)

**Goal**: Agents gain the ability to read Discussions for the first time.

**New file**: `gateway/github_discussion_tools.go`

This file follows the exact pattern of `gateway/github_tools.go` -- a `GitHubDiscussionHandler` struct with lazy token resolution via `tokenFunc`, using the Publisher's proven `graphql()` helper pattern.

#### Tool 1: `github_discussion_list`

Lists recent Discussions in a repository, optionally filtered by category.

```go
// GitHubDiscussionHandler implements Discussion tools using GitHub GraphQL API.
type GitHubDiscussionHandler struct {
    httpClient *http.Client
    tokenFunc  func(ctx context.Context) (string, error)
    graphqlURL string // "https://api.github.com/graphql"
}
```

**GraphQL query** (exact, tested against live API):

```graphql
query($owner: String!, $name: String!, $first: Int!, $categoryId: ID, $after: String) {
  repository(owner: $owner, name: $name) {
    discussions(
      first: $first,
      after: $after,
      categoryId: $categoryId,
      orderBy: {field: CREATED_AT, direction: DESC}
    ) {
      nodes {
        number
        title
        body
        createdAt
        updatedAt
        author { login }
        category { name id }
        labels(first: 10) { nodes { name } }
        comments { totalCount }
        url
      }
      pageInfo {
        hasNextPage
        endCursor
      }
    }
  }
}
```

**Input schema**:
```json
{
  "type": "object",
  "properties": {
    "owner":    {"type": "string", "description": "Repository owner"},
    "repo":     {"type": "string", "description": "Repository name"},
    "category": {"type": "string", "description": "Filter by category name (Agent Reports, Security Advisories, etc.)"},
    "first":    {"type": "integer", "description": "Number of discussions to return (default 10, max 50)"},
    "after":    {"type": "string", "description": "Cursor for pagination (from previous response)"}
  },
  "required": ["owner", "repo"]
}
```

**Category resolution**: If `category` is provided, first resolve to a category ID using the same query Publisher uses in `Init()`:

```graphql
query($owner: String!, $name: String!) {
  repository(owner: $owner, name: $name) {
    discussionCategories(first: 25) {
      nodes { id name }
    }
  }
}
```

Cache the category ID map in the handler (populated on first call, refreshed every 10 minutes).

**Return format**: MCP text result with JSON containing `discussions` array and `pageInfo`.

#### Tool 2: `github_discussion_get`

Gets a single Discussion by number, including its full comment thread.

**GraphQL query**:

```graphql
query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    discussion(number: $number) {
      number
      title
      body
      createdAt
      updatedAt
      author { login }
      category { name }
      labels(first: 10) { nodes { name } }
      url
      comments(first: 50) {
        totalCount
        nodes {
          id
          body
          createdAt
          author { login }
          replies(first: 20) {
            nodes {
              id
              body
              createdAt
              author { login }
            }
          }
        }
      }
    }
  }
}
```

**Input schema**:
```json
{
  "type": "object",
  "properties": {
    "owner":  {"type": "string"},
    "repo":   {"type": "string"},
    "number": {"type": "integer", "description": "Discussion number"}
  },
  "required": ["owner", "repo", "number"]
}
```

**Return format**: Full Discussion with nested comments. This is what agents will use to read conversation threads.

#### Tool 3: `github_discussion_search`

Searches Discussions by text query (for finding related findings across time).

**GraphQL query**:

```graphql
query($query: String!, $first: Int!) {
  search(query: $query, type: DISCUSSION, first: $first) {
    discussionCount
    nodes {
      ... on Discussion {
        number
        title
        body
        createdAt
        author { login }
        category { name }
        labels(first: 10) { nodes { name } }
        comments { totalCount }
        url
        repository { nameWithOwner }
      }
    }
  }
}
```

The `query` parameter uses GitHub's search syntax. The handler prepends `repo:owner/repo` automatically:

```go
func (h *GitHubDiscussionHandler) Search(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
    var a struct {
        Owner string `json:"owner"`
        Repo  string `json:"repo"`
        Query string `json:"query"`
        First int    `json:"first"`
    }
    // ...
    searchQuery := fmt.Sprintf("repo:%s/%s %s", a.Owner, a.Repo, a.Query)
    // ...
}
```

**Input schema**:
```json
{
  "type": "object",
  "properties": {
    "owner": {"type": "string"},
    "repo":  {"type": "string"},
    "query": {"type": "string", "description": "Search query (supports GitHub search syntax: label:security, author:app/rj-agent-bot)"},
    "first": {"type": "integer", "description": "Max results (default 10, max 25)"}
  },
  "required": ["owner", "repo", "query"]
}
```

#### Registration in gateway

**File: `gateway/tools.go`** -- Add definitions to `gatewayTools()`:

```go
// Discussion tools for agent communication.
discussionTools := []map[string]any{
    {
        "name":        "github_discussion_list",
        "description": "List recent GitHub Discussions, optionally filtered by category.",
        "inputSchema": map[string]any{...},
    },
    {
        "name":        "github_discussion_get",
        "description": "Get a GitHub Discussion by number, including its full comment thread.",
        "inputSchema": map[string]any{...},
    },
    {
        "name":        "github_discussion_search",
        "description": "Search GitHub Discussions by text query, labels, or author.",
        "inputSchema": map[string]any{...},
    },
}
tools = append(tools, discussionTools...)
```

**File: `gateway/mcp_proxy.go`** -- Add to `gatewayToolNames` map:

```go
"github_discussion_list":   true,
"github_discussion_get":    true,
"github_discussion_search": true,
```

Add dispatch cases in `handleGatewayTool()`:

```go
case "github_discussion_list":
    result, err = p.discussions.List(ctx, args)
    // audit log
case "github_discussion_get":
    result, err = p.discussions.Get(ctx, args)
    // audit log
case "github_discussion_search":
    result, err = p.discussions.Search(ctx, args)
    // audit log
```

**MCPProxy struct** -- Add field:

```go
discussions *GitHubDiscussionHandler
```

Wire it in `main.go` alongside `github`:

```go
proxy.discussions = NewGitHubDiscussionHandler(githubTokenFunc)
```

#### GraphQL helper

The `GitHubDiscussionHandler` needs its own `graphql()` method matching Publisher's proven pattern. Extract the common GraphQL client into a shared helper or duplicate it (it is only 30 lines):

```go
func (h *GitHubDiscussionHandler) graphql(ctx context.Context, query string, variables map[string]any) (json.RawMessage, error) {
    token, err := h.tokenFunc(ctx)
    if err != nil {
        return nil, fmt.Errorf("resolve token: %w", err)
    }
    payload := map[string]any{"query": query, "variables": variables}
    body, _ := json.Marshal(payload)

    req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.graphqlURL, bytes.NewReader(body))
    if err != nil {
        return nil, err
    }
    req.Header.Set("Authorization", "Bearer "+token)
    req.Header.Set("Content-Type", "application/json")

    resp, err := h.httpClient.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    respBody, _ := io.ReadAll(resp.Body)
    if resp.StatusCode != 200 {
        return nil, fmt.Errorf("graphql %d: %s", resp.StatusCode, truncateStr(string(respBody), 300))
    }
    return respBody, nil
}
```

**Verification command**:
```bash
cd gateway && go build ./...
```

---

### Day 2: Discussion Read Tools Testing

**Goal**: 100% test coverage for all 3 read tools, integration-tested against mock GraphQL server and verified in tools/list.

#### Unit tests: `gateway/github_discussion_tools_test.go`

Follow the exact pattern from `gateway/github_tools_test.go` -- httptest mock servers returning canned GraphQL responses.

**Test: `TestDiscussionList_Basic`**
- Mock GraphQL returns 3 discussions
- Verify: returns correct count, numbers, titles, categories
- Verify: no internal URLs or secrets in output (sanitization)

**Test: `TestDiscussionList_WithCategory`**
- Mock: category resolution query + filtered discussion query
- Verify: categoryId variable is passed correctly

**Test: `TestDiscussionList_Pagination`**
- Mock: returns `hasNextPage: true` and `endCursor`
- Verify: pageInfo included in response for agent to use

**Test: `TestDiscussionGet_WithComments`**
- Mock: discussion #42 with 3 comments, nested replies
- Verify: all comments and replies present in response
- Verify: author login fields populated

**Test: `TestDiscussionGet_NotFound`**
- Mock: GraphQL returns null discussion
- Verify: graceful error message, not a panic

**Test: `TestDiscussionSearch_Basic`**
- Mock: search returns 2 matching discussions
- Verify: query has `repo:owner/repo` prepended
- Verify: discussionCount matches

**Test: `TestDiscussionSearch_Empty`**
- Mock: search returns 0 results
- Verify: empty array, not null

**Test: `TestDiscussionList_SanitizesOutput`**
- Mock: discussion body contains `ghp_secret123` and `.svc.cluster.local`
- Verify: sanitization applied before returning to agent

#### Integration test (live verification)

After deploying, verify tools appear in the gateway:

```bash
# Verify tools/list includes new Discussion tools
curl -s -X POST http://rj-gateway.fuzzy-dev.svc.cluster.local:8080/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' \
  | jq '.result.tools[] | select(.name | startswith("github_discussion")) | .name'

# Expected output:
# "github_discussion_list"
# "github_discussion_get"
# "github_discussion_search"
```

```bash
# Read an existing Discussion (e.g., #159)
curl -s -X POST http://rj-gateway.fuzzy-dev.svc.cluster.local:8080/mcp \
  -H 'Content-Type: application/json' \
  -d '{
    "jsonrpc":"2.0","id":2,"method":"tools/call",
    "params":{
      "name":"github_discussion_get",
      "arguments":{"owner":"tinyland-inc","repo":"remote-juggler","number":159}
    }
  }' | jq '.result.content[0].text' | python3 -m json.tool | head -20
```

#### Update tools_test.go

Add new tools to the expected tool names list in `gateway/tools_test.go`:

```go
"github_discussion_list":   true,
"github_discussion_get":    true,
"github_discussion_search": true,
```

**Verification command**:
```bash
cd gateway && go test -v -run Discussion ./...
cd gateway && go test -v -run TestGatewayTools ./...
```

---

### Day 3: Discussion Write Tools

**Goal**: Agents can reply to Discussions and label them for routing.

#### Tool 4: `github_discussion_reply`

Adds a comment to a Discussion. This is the critical tool that enables conversations.

**GraphQL mutation**:

```graphql
mutation($discussionId: ID!, $body: String!) {
  addDiscussionComment(input: {discussionId: $discussionId, body: $body}) {
    comment {
      id
      url
      createdAt
    }
  }
}
```

**Important**: The mutation requires the Discussion's GraphQL node ID (not the number). The handler must first resolve the number to a node ID:

```graphql
query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    discussion(number: $number) {
      id
    }
  }
}
```

**Input schema**:
```json
{
  "type": "object",
  "properties": {
    "owner":  {"type": "string"},
    "repo":   {"type": "string"},
    "number": {"type": "integer", "description": "Discussion number to reply to"},
    "body":   {"type": "string", "description": "Comment body (markdown). Include <!-- rj-meta {...} --> for structured metadata."}
  },
  "required": ["owner", "repo", "number", "body"]
}
```

**Implementation flow**:
1. Resolve discussion number to node ID
2. Sanitize the body (reuse `sanitizeString` from publisher)
3. Execute `addDiscussionComment` mutation
4. Return comment URL and ID

#### Tool 5: `github_discussion_label`

Adds labels to a Discussion (for routing). GitHub Discussions support labels via the `Labelable` interface.

**GraphQL mutation**:

```graphql
mutation($labelableId: ID!, $labelIds: [ID!]!) {
  addLabelsToLabelable(input: {labelableId: $labelableId, labelIds: $labelIds}) {
    labelable {
      ... on Discussion {
        number
        labels(first: 10) { nodes { name } }
      }
    }
  }
}
```

**Label ID resolution**: Labels must be resolved from names to node IDs:

```graphql
query($owner: String!, $name: String!, $first: Int!) {
  repository(owner: $owner, name: $name) {
    labels(first: $first) {
      nodes { id name }
    }
  }
}
```

Cache the label name-to-ID map (same pattern as category caching).

**Input schema**:
```json
{
  "type": "object",
  "properties": {
    "owner":  {"type": "string"},
    "repo":   {"type": "string"},
    "number": {"type": "integer", "description": "Discussion number to label"},
    "labels": {"type": "array", "items": {"type": "string"}, "description": "Label names to add (e.g., ['handoff:hexstrike-ai', 'severity:high'])"}
  },
  "required": ["owner", "repo", "number", "labels"]
}
```

#### Create routing labels on the repository

Use `gh` CLI to create the labels needed for agent routing:

```bash
# Agent identity labels
GITHUB_TOKEN= gh label create "agent:ironclaw" --repo tinyland-inc/remote-juggler --color "0E8A16" --description "Finding originated from IronClaw agent"
GITHUB_TOKEN= gh label create "agent:picoclaw" --repo tinyland-inc/remote-juggler --color "1D76DB" --description "Finding originated from PicoClaw agent"
GITHUB_TOKEN= gh label create "agent:hexstrike-ai" --repo tinyland-inc/remote-juggler --color "B60205" --description "Finding originated from HexStrike-AI agent"
GITHUB_TOKEN= gh label create "agent:gateway-direct" --repo tinyland-inc/remote-juggler --color "5319E7" --description "Finding originated from gateway-direct agent"

# Handoff labels (request another agent to act)
GITHUB_TOKEN= gh label create "handoff:ironclaw" --repo tinyland-inc/remote-juggler --color "C2E0C6" --description "Requesting IronClaw to review and act"
GITHUB_TOKEN= gh label create "handoff:picoclaw" --repo tinyland-inc/remote-juggler --color "BFD4F2" --description "Requesting PicoClaw to review and act"
GITHUB_TOKEN= gh label create "handoff:hexstrike-ai" --repo tinyland-inc/remote-juggler --color "FCCECE" --description "Requesting HexStrike-AI to review and act"

# Severity labels (for routing priority)
GITHUB_TOKEN= gh label create "severity:critical" --repo tinyland-inc/remote-juggler --color "B60205" --description "Critical finding requiring immediate attention"
GITHUB_TOKEN= gh label create "severity:high" --repo tinyland-inc/remote-juggler --color "D93F0B" --description "High severity finding"
GITHUB_TOKEN= gh label create "severity:medium" --repo tinyland-inc/remote-juggler --color "FBCA04" --description "Medium severity finding"
GITHUB_TOKEN= gh label create "severity:low" --repo tinyland-inc/remote-juggler --color "0E8A16" --description "Low severity finding"

# State labels
GITHUB_TOKEN= gh label create "state:open" --repo tinyland-inc/remote-juggler --color "EDEDED" --description "Finding is open and needs action"
GITHUB_TOKEN= gh label create "state:acknowledged" --repo tinyland-inc/remote-juggler --color "C5DEF5" --description "Agent has acknowledged and is working on finding"
GITHUB_TOKEN= gh label create "state:resolved" --repo tinyland-inc/remote-juggler --color "0E8A16" --description "Finding has been resolved"
```

#### Registration

Add to `gatewayToolNames`, `gatewayTools()`, and `handleGatewayTool()` switch (same pattern as Day 1 tools).

Tool count after Day 3: **53 + 5 = 58 total tools** (36 Chapel + 22 gateway).

#### Tests

- `TestDiscussionReply_Basic` -- mock mutation, verify comment created
- `TestDiscussionReply_ResolvesNodeID` -- verify number-to-ID resolution
- `TestDiscussionReply_SanitizesBody` -- secrets stripped from comment body
- `TestDiscussionLabel_Basic` -- mock mutation, verify labels added
- `TestDiscussionLabel_ResolvesLabelIDs` -- verify label name-to-ID resolution
- `TestDiscussionLabel_UnknownLabel` -- graceful error for non-existent labels

**Verification**:
```bash
cd gateway && go test -v -run 'Discussion(Reply|Label)' ./...
```

---

### Day 4: Finding Router

**Goal**: Campaign results with findings are automatically routed to the appropriate agent via Discussion labels.

**New file**: `test/campaigns/runner/router.go`

The Router sits between `storeResult` and the existing Publisher, adding routing intelligence.

#### Router struct

```go
// Router routes campaign findings to appropriate agents via Discussion labels.
type Router struct {
    publisher  *Publisher
    rules      []RoutingRule
}

// RoutingRule maps a finding pattern to a target agent and Discussion category.
type RoutingRule struct {
    // Match criteria (any match triggers the rule)
    SourceAgent    string   // Campaign's agent (e.g., "ironclaw")
    SeverityIn     []string // Finding severity must be in this list
    CampaignPrefix string   // Campaign ID prefix (e.g., "oc-" for openclaw)
    LabelContains  string   // Finding has a label containing this string

    // Routing action
    TargetAgent string // Agent to hand off to (e.g., "hexstrike-ai")
    Category    string // Discussion category to post in
    Labels      []string // Labels to apply to the Discussion
    Priority    int    // Lower = higher priority (for ordering)
}
```

#### Default routing rules

```go
var defaultRoutingRules = []RoutingRule{
    // Security findings from any agent -> HexStrike-AI
    {
        SeverityIn:  []string{"critical", "high"},
        LabelContains: "security",
        TargetAgent: "hexstrike-ai",
        Category:    "Security Advisories",
        Labels:      []string{"handoff:hexstrike-ai", "severity:high"},
        Priority:    1,
    },
    // Credential exposure findings from any agent -> HexStrike-AI
    {
        LabelContains: "credential-exposure",
        TargetAgent:   "hexstrike-ai",
        Category:      "Security Advisories",
        Labels:        []string{"handoff:hexstrike-ai"},
        Priority:      2,
    },
    // Code quality findings from HexStrike -> IronClaw for fixing
    {
        SourceAgent:    "hexstrike-ai",
        SeverityIn:     []string{"medium", "low"},
        LabelContains:  "code-quality",
        TargetAgent:    "ironclaw",
        Category:       "Agent Reports",
        Labels:         []string{"handoff:ironclaw"},
        Priority:       3,
    },
    // Dependency issues from any agent -> IronClaw
    {
        LabelContains: "dependency",
        TargetAgent:   "ironclaw",
        Category:      "Agent Reports",
        Labels:        []string{"handoff:ironclaw"},
        Priority:      4,
    },
    // Upstream drift -> PicoClaw (lightweight, fast scanner)
    {
        CampaignPrefix: "xa-upstream",
        TargetAgent:    "picoclaw",
        Category:       "Agent Reports",
        Labels:         []string{"handoff:picoclaw"},
        Priority:       5,
    },
}
```

#### Router integration with scheduler.go

The Router hooks into `storeResult()` in `scheduler.go`. After the Publisher creates a Discussion, the Router:

1. Evaluates each finding against routing rules
2. For matched findings, adds handoff labels to the Discussion via `github_discussion_label`
3. Posts a routing comment with structured metadata

Modify `scheduler.go` `storeResult()`:

```go
// After publisher creates Discussion:
if s.router != nil && result.DiscussionURL != "" {
    routedFindings := s.router.Route(ctx, campaign, result)
    if len(routedFindings) > 0 {
        log.Printf("campaign %s: routed %d findings to other agents", campaign.ID, len(routedFindings))
    }
}
```

The Router's `Route()` method:

```go
func (r *Router) Route(ctx context.Context, campaign *Campaign, result *CampaignResult) []RoutedFinding {
    var routed []RoutedFinding
    for _, finding := range result.Findings {
        for _, rule := range r.rules {
            if rule.matches(campaign, &finding) {
                // Extract discussion number from URL
                num := extractDiscussionNumber(result.DiscussionURL)
                if num == 0 { continue }

                // Label the Discussion for the target agent
                err := r.labelDiscussion(ctx, num, rule.Labels)
                if err != nil {
                    log.Printf("router: label error: %v", err)
                    continue
                }

                // Post routing comment with structured metadata
                meta := RoutingMeta{
                    From:               campaign.Agent,
                    To:                 rule.TargetAgent,
                    MessageType:        "handoff",
                    Priority:           severityToPriority(finding.Severity),
                    FindingFingerprint: finding.Fingerprint,
                    CampaignID:         campaign.ID,
                    Timestamp:          time.Now().UTC().Format(time.RFC3339),
                }
                body := r.formatRoutingComment(&finding, &meta)
                err = r.replyToDiscussion(ctx, num, body)
                // ...
                routed = append(routed, RoutedFinding{...})
                break // First matching rule wins
            }
        }
    }
    return routed
}
```

The Router calls the gateway's Discussion tools internally via HTTP (same pattern as Dispatcher's `callTool()`).

#### Router tests: `test/campaigns/runner/router_test.go`

- `TestRouterMatchesSeverity` -- high severity security finding matches HexStrike rule
- `TestRouterMatchesCampaignPrefix` -- `xa-upstream-*` routes to PicoClaw
- `TestRouterMatchesLabel` -- finding with `credential-exposure` label routes to HexStrike
- `TestRouterFirstRuleWins` -- higher priority rule takes precedence
- `TestRouterNoMatch` -- findings without matching rules are not routed
- `TestRouterFormatsMetaComment` -- verify `<!-- rj-meta {...} -->` block format
- `TestRouterExtractsDiscussionNumber` -- parses `/discussions/42` from URL

**Verification**:
```bash
cd test/campaigns/runner && go test -v -run Router ./...
```

---

### Day 5: Handoff Campaign

**Goal**: A new campaign type that polls for Discussions with `handoff:<agent>` labels and dispatches agents to act on them.

#### New campaign: `test/campaigns/cross-agent/xa-finding-handoff.json`

```json
{
  "id": "xa-finding-handoff",
  "name": "Cross-Agent Finding Handoff",
  "description": "Polls for Discussions with handoff:<agent> labels. For each unhandled Discussion, dispatches the target agent to read the finding, perform analysis, and reply with results.",
  "agent": "gateway-direct",
  "trigger": {
    "schedule": "*/15 * * * *",
    "event": "manual"
  },
  "process": [
    "Search Discussions with label 'handoff:hexstrike-ai' AND NOT label 'state:acknowledged' via github_discussion_search",
    "For each matching Discussion: read full thread via github_discussion_get",
    "Extract the rj-meta block from the routing comment to identify the original finding",
    "Trigger the appropriate campaign for the target agent based on finding type",
    "Label the Discussion 'state:acknowledged' to prevent re-processing",
    "Search Discussions with label 'handoff:ironclaw' AND NOT label 'state:acknowledged'",
    "Repeat: read, extract, trigger, acknowledge",
    "Search Discussions with label 'handoff:picoclaw' AND NOT label 'state:acknowledged'",
    "Repeat: read, extract, trigger, acknowledge"
  ],
  "tools": [
    "github_discussion_search",
    "github_discussion_get",
    "github_discussion_label",
    "github_discussion_reply",
    "juggler_campaign_trigger",
    "juggler_campaign_status"
  ],
  "targets": [
    {
      "forge": "github",
      "org": "tinyland-inc",
      "repo": "remote-juggler"
    }
  ],
  "outputs": {
    "setecKey": "remotejuggler/campaigns/xa-finding-handoff"
  },
  "guardrails": {
    "maxDuration": "10m",
    "readOnly": false,
    "aiApiBudget": {
      "maxTokens": 20000
    }
  },
  "feedback": {
    "publishOnSuccess": false
  },
  "metrics": {
    "successCriteria": "All unacknowledged handoff Discussions are processed",
    "kpis": [
      "discussions_scanned",
      "handoffs_processed",
      "handoffs_by_agent"
    ]
  }
}
```

#### Agent-specific response campaigns

Each agent needs a campaign that responds to handoff requests. These are the "listener" campaigns.

**`test/campaigns/hexstrike/hs-handoff-response.json`**:

```json
{
  "id": "hs-handoff-response",
  "name": "HexStrike Handoff Response",
  "description": "Responds to security findings handed off from other agents. Reads the Discussion thread, performs targeted security analysis, and replies with findings.",
  "agent": "hexstrike-ai",
  "trigger": {
    "event": "manual"
  },
  "process": [
    "Read the specified Discussion via github_discussion_get to understand the finding",
    "Extract the original finding details from the rj-meta block",
    "Perform targeted security analysis based on finding type",
    "If credential-exposure: run credential_scan on specified paths",
    "If network-security: run targeted network scan",
    "If vulnerability: check CVE database and assess impact",
    "Reply to the Discussion with structured findings and rj-meta response block",
    "Label Discussion 'state:resolved' if no issues found, or add severity labels if issues confirmed",
    "Store analysis results in Setec"
  ],
  "tools": [
    "github_discussion_get",
    "github_discussion_reply",
    "github_discussion_label",
    "credential_scan",
    "juggler_resolve_composite",
    "juggler_setec_put",
    "juggler_campaign_status"
  ],
  "targets": [
    {
      "forge": "github",
      "org": "tinyland-inc",
      "repo": "remote-juggler"
    }
  ],
  "outputs": {
    "setecKey": "remotejuggler/campaigns/hs-handoff-response",
    "issueLabels": ["campaign", "security", "handoff-response"],
    "issueRepo": "tinyland-inc/remote-juggler"
  },
  "guardrails": {
    "maxDuration": "30m",
    "readOnly": false,
    "aiApiBudget": {
      "maxTokens": 50000
    }
  },
  "feedback": {
    "createIssues": true,
    "publishOnSuccess": false
  }
}
```

**`test/campaigns/openclaw/oc-handoff-response.json`**: Similar structure, but IronClaw-specific (code quality fixes, dependency updates, PR creation).

#### Handoff dispatcher enhancement

The `xa-finding-handoff` campaign is `gateway-direct`, meaning it uses `dispatchDirect()`. But it needs to trigger agent-specific campaigns. The current `dispatchDirect()` calls tools sequentially from the `tools` list. For the handoff campaign, the gateway-direct agent needs to:

1. Call `github_discussion_search` to find unhandled discussions
2. Call `github_discussion_get` to read each one
3. Call `juggler_campaign_trigger` to dispatch the right agent

This requires the handoff campaign to run in `autonomous` mode (not `schema` mode), because the tool sequence depends on search results.

**Dispatcher change**: When `campaign.Mode == "autonomous"`, `dispatchDirect()` should pass the campaign prompt (derived from `process` steps) to the gateway's LLM endpoint (Aperture) and let it choose tools dynamically. This is a significant enhancement that deserves its own ticket, but for Week 3-4 we can start with a simpler approach:

**Simple approach for Week 3-4**: The `xa-finding-handoff` campaign runs as an IronClaw campaign (not gateway-direct) since IronClaw already has autonomous reasoning. The campaign's `process` steps guide it.

Revised: change `"agent": "gateway-direct"` to `"agent": "ironclaw"` in the campaign definition.

---

### Day 6: Structured Communication Protocol

**Goal**: Standardize how agents embed machine-readable metadata in Discussion comments.

#### Protocol specification

Every agent comment MUST include an `rj-meta` HTML comment block at the end of the body:

```markdown
[Human-readable content here]

<!-- rj-meta
{
  "version": "1",
  "from": "ironclaw",
  "to": "hexstrike-ai",
  "message_type": "handoff|response|acknowledge|escalate",
  "priority": "critical|high|medium|low",
  "finding_fingerprint": "sha256-of-finding-title-and-campaign-id",
  "campaign_id": "oc-identity-audit",
  "run_id": "oc-identity-audit-1740456000",
  "thread_id": "discussion-42",
  "parent_comment_id": "DC_kwDOE...",
  "timestamp": "2026-03-10T14:30:00Z",
  "action_requested": "scan|review|fix|confirm|none",
  "context": {
    "severity": "high",
    "file": "gateway/mcp_proxy.go",
    "line": 215,
    "finding_type": "credential-exposure"
  }
}
-->
```

**Field definitions**:

| Field | Required | Description |
|-------|----------|-------------|
| `version` | yes | Protocol version (always "1" for now) |
| `from` | yes | Agent name that posted this comment |
| `to` | no | Target agent (omit for broadcast) |
| `message_type` | yes | `handoff` (new work), `response` (answering handoff), `acknowledge` (noted, working on it), `escalate` (need human) |
| `priority` | yes | Maps to severity labels |
| `finding_fingerprint` | yes | SHA256 of `campaign_id + ":" + finding.Title` (stable dedup key) |
| `campaign_id` | yes | Campaign that generated this finding |
| `run_id` | yes | Specific run identifier |
| `thread_id` | yes | `discussion-{number}` for thread continuity |
| `parent_comment_id` | no | GraphQL ID of the comment being replied to |
| `timestamp` | yes | ISO 8601 UTC |
| `action_requested` | no | What the target agent should do |
| `context` | no | Finding-specific context (severity, file, line, type) |

#### Parser function

Add to `test/campaigns/runner/router.go`:

```go
// ParseRJMeta extracts the rj-meta JSON block from a Discussion body or comment.
func ParseRJMeta(body string) (*RJMeta, error) {
    re := regexp.MustCompile(`(?s)<!--\s*rj-meta\s*\n(.*?)\n\s*-->`)
    matches := re.FindStringSubmatch(body)
    if len(matches) < 2 {
        return nil, fmt.Errorf("no rj-meta block found")
    }
    var meta RJMeta
    if err := json.Unmarshal([]byte(matches[1]), &meta); err != nil {
        return nil, fmt.Errorf("parse rj-meta: %w", err)
    }
    return &meta, nil
}
```

#### Update Publisher to emit structured metadata

Modify `publisher.go` `formatBody()` to append an `rj-meta` block to every Discussion body:

```go
// At the end of formatBody:
meta := map[string]any{
    "version":     "1",
    "from":        result.Agent,
    "message_type": "report",
    "campaign_id": campaign.ID,
    "run_id":      result.RunID,
    "timestamp":   result.FinishedAt,
    "finding_count": len(result.Findings),
}
if len(result.Findings) > 0 {
    fingerprints := make([]string, 0, len(result.Findings))
    for _, f := range result.Findings {
        fingerprints = append(fingerprints, f.Fingerprint)
    }
    meta["finding_fingerprints"] = fingerprints
}
metaJSON, _ := json.MarshalIndent(meta, "", "  ")
b.WriteString(fmt.Sprintf("\n<!-- rj-meta\n%s\n-->\n", string(metaJSON)))
```

#### Fingerprint generation

Add to `test/campaigns/runner/router.go`:

```go
func GenerateFingerprint(campaignID, findingTitle string) string {
    h := sha256.Sum256([]byte(campaignID + ":" + findingTitle))
    return hex.EncodeToString(h[:])
}
```

#### Protocol tests

- `TestParseRJMeta_Valid` -- extract metadata from comment body
- `TestParseRJMeta_NoBlock` -- returns error when no block present
- `TestParseRJMeta_InvalidJSON` -- returns error for malformed JSON
- `TestGenerateFingerprint_Stable` -- same inputs produce same hash
- `TestGenerateFingerprint_Unique` -- different inputs produce different hashes
- `TestPublisherEmitsRJMeta` -- verify formatBody includes rj-meta block

**Verification**:
```bash
cd test/campaigns/runner && go test -v -run 'RJMeta|Fingerprint' ./...
```

---

### Day 7: Agent Workspace Updates

**Goal**: All 3 agents know about Discussion tools and the communication protocol.

#### IronClaw workspace updates

**File**: `deploy/fork-dockerfiles/ironclaw/workspace/TOOLS.md`

Add new section after "### GitHub Tools (8)":

```markdown
### Discussion Tools (5)

| Tool | Parameters | Description |
|------|-----------|-------------|
| `github_discussion_list` | `owner`, `repo`, `category` (opt), `first` (opt) | List recent Discussions |
| `github_discussion_get` | `owner`, `repo`, `number` | Get Discussion with full comment thread |
| `github_discussion_search` | `owner`, `repo`, `query`, `first` (opt) | Search Discussions by text/labels |
| `github_discussion_reply` | `owner`, `repo`, `number`, `body` | Reply to a Discussion |
| `github_discussion_label` | `owner`, `repo`, `number`, `labels` | Add labels to a Discussion |
```

Add `rj-tool` examples:

```markdown
## Discussion Operations

```bash
# List recent Discussions
exec("/workspace/bin/rj-tool github_discussion_list owner=tinyland-inc repo=remote-juggler first=5")

# Get a Discussion with comments
exec("/workspace/bin/rj-tool github_discussion_get owner=tinyland-inc repo=remote-juggler number=42")

# Search for security findings
exec("/workspace/bin/rj-tool github_discussion_search owner=tinyland-inc repo=remote-juggler query='label:handoff:ironclaw -label:state:acknowledged'")

# Reply to a Discussion
exec("/workspace/bin/rj-tool github_discussion_reply owner=tinyland-inc repo=remote-juggler number=42 body='Analysis complete. No issues found.'")

# Label a Discussion
exec("/workspace/bin/rj-tool github_discussion_label owner=tinyland-inc repo=remote-juggler number=42 labels=state:resolved")
```
```

**File**: `deploy/fork-dockerfiles/ironclaw/workspace/AGENT.md` (create new)

IronClaw currently has no AGENT.md. Create one with communication protocol:

```markdown
# IronClaw Agent Instructions

You are **IronClaw**, a code analysis and remediation agent in the RemoteJuggler agent plane. You specialize in dependency auditing, code quality, and automated fixes.

## Communication Protocol

When you find something that requires another agent's expertise, create a handoff:

1. Post findings to the Discussion via the Publisher (automatic)
2. The Router will label it with `handoff:<target-agent>`
3. The target agent will read the Discussion and reply

When you receive a handoff (Discussion labeled `handoff:ironclaw`):

1. Read the Discussion via `github_discussion_get`
2. Parse the `<!-- rj-meta {...} -->` block for context
3. Perform your analysis
4. Reply with your findings and your own `rj-meta` block:

<!-- rj-meta
{
  "version": "1",
  "from": "ironclaw",
  "to": "<original-agent>",
  "message_type": "response",
  ...
}
-->

## Handoff Rules

- Security findings (credentials, vulnerabilities) -> handoff to HexStrike-AI
- Upstream drift detected -> handoff to PicoClaw
- Code quality / dependency issues -> handle yourself
- Infrastructure issues -> escalate (message_type: "escalate")
```

#### HexStrike-AI workspace updates

**File**: `deploy/fork-dockerfiles/hexstrike-ai/workspace/TOOLS.md`

Add Discussion tools section (same table as IronClaw). HexStrike uses direct MCP tools through its adapter, so the format is slightly different:

```markdown
### Discussion Tools (via adapter)

- `github_discussion_list` -- List recent Discussions
- `github_discussion_get` -- Get Discussion with full comment thread
- `github_discussion_search` -- Search Discussions
- `github_discussion_reply` -- Reply to a Discussion
- `github_discussion_label` -- Add labels to a Discussion
```

**File**: `deploy/fork-dockerfiles/hexstrike-ai/workspace/AGENT.md`

Add "Communication Protocol" section (same as IronClaw but with HexStrike-specific handoff rules).

#### PicoClaw workspace updates

**File**: `deploy/fork-dockerfiles/picoclaw/workspace/TOOLS.md`

Add Discussion tools (same format as PicoClaw's existing tool list).

**File**: `deploy/fork-dockerfiles/picoclaw/workspace/AGENT.md`

Add "Communication Protocol" section.

#### HexStrike adapter prefix filter update

**Critical**: The HexStrike adapter uses a prefix filter (`juggler_*`, `github_*`) to decide which tools to proxy. The new `github_discussion_*` tools already match the `github_*` prefix, so they will be proxied automatically. **No adapter change needed**.

**Verification**:
```bash
# After pushing workspace updates and redeploying:
kubectl exec -n fuzzy-dev deploy/ironclaw -- cat /workspace/TOOLS.md | grep discussion
kubectl exec -n fuzzy-dev deploy/hexstrike-ai-agent -- cat /workspace/TOOLS.md | grep discussion
kubectl exec -n fuzzy-dev deploy/picoclaw-agent -- cat /workspace/TOOLS.md | grep discussion
```

---

### Day 8: Seed the First Conversation

**Goal**: Manually trigger the first cross-agent conversation to verify the full pipeline.

#### Step 1: Create a seed Discussion with a security finding

Trigger `oc-identity-audit` (IronClaw campaign) which checks SSH keys and credentials:

```bash
# Trigger via campaign runner
kubectl exec -n fuzzy-dev deploy/campaign-runner -- \
  wget -q -O- --post-data='' 'http://localhost:8081/trigger?campaign=oc-identity-audit'

# Wait for completion (poll status)
kubectl exec -n fuzzy-dev deploy/campaign-runner -- \
  wget -q -O- 'http://localhost:8081/status'
```

If the campaign produces findings, the Publisher creates a Discussion and the Router labels it with `handoff:hexstrike-ai` (if findings match security rules).

#### Step 2: Verify the routing happened

```bash
# Search for handoff-labeled Discussions
GITHUB_TOKEN= gh api graphql -f query='{
  search(query: "repo:tinyland-inc/remote-juggler label:handoff:hexstrike-ai", type: DISCUSSION, first: 5) {
    nodes {
      ... on Discussion {
        number
        title
        labels(first: 10) { nodes { name } }
        comments { totalCount }
      }
    }
  }
}' --jq '.data.search.nodes[]'
```

#### Step 3: Trigger HexStrike response

```bash
# Trigger handoff response campaign
kubectl exec -n fuzzy-dev deploy/campaign-runner -- \
  wget -q -O- --post-data='' 'http://localhost:8081/trigger?campaign=hs-handoff-response'
```

#### Step 4: Verify the reply

```bash
# Read the Discussion to see HexStrike's reply
GITHUB_TOKEN= gh api graphql -f query='query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    discussion(number: $number) {
      title
      comments(first: 10) {
        nodes {
          body
          author { login }
          createdAt
        }
      }
    }
  }
}' -f owner=tinyland-inc -f name=remote-juggler -F number=<DISCUSSION_NUMBER> \
  --jq '.data.repository.discussion.comments.nodes[] | "\(.author.login): \(.body | .[0:100])"'
```

#### Fallback: If no natural findings exist

If `oc-identity-audit` produces no findings (clean run), we need to seed a finding manually. Create a Discussion with a synthetic security finding:

```bash
# Create a synthetic finding Discussion
GITHUB_TOKEN= gh api graphql -f query='mutation($repoId: ID!, $catId: ID!, $title: String!, $body: String!) {
  createDiscussion(input: {repositoryId: $repoId, categoryId: $catId, title: $title, body: $body}) {
    discussion { number url }
  }
}' \
  -f repoId="$(GITHUB_TOKEN= gh api graphql -f query='{ repository(owner:"tinyland-inc", name:"remote-juggler") { id } }' --jq '.data.repository.id')" \
  -f catId="DIC_kwDORVOrMs4C3KRI" \
  -f title="[FINDING] Potential credential exposure in gateway config" \
  -f body="## Finding: Potential credential exposure

**Agent**: ironclaw | **Campaign**: oc-identity-audit | **Severity**: high

A configuration file appears to contain a hardcoded API token pattern that should be resolved via Setec.

**File**: gateway/config.go, line 42
**Pattern**: String matching \`*_TOKEN=\` outside of test files

### Recommendation
Verify this is not a live credential and migrate to Setec-backed resolution.

<!-- rj-meta
{
  \"version\": \"1\",
  \"from\": \"ironclaw\",
  \"to\": \"hexstrike-ai\",
  \"message_type\": \"handoff\",
  \"priority\": \"high\",
  \"finding_fingerprint\": \"seed-test-001\",
  \"campaign_id\": \"oc-identity-audit\",
  \"run_id\": \"oc-identity-audit-seed-1\",
  \"thread_id\": \"discussion-seed\",
  \"timestamp\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",
  \"action_requested\": \"scan\",
  \"context\": {
    \"severity\": \"high\",
    \"file\": \"gateway/config.go\",
    \"line\": 42,
    \"finding_type\": \"credential-exposure\"
  }
}
-->
"

# Then label it
GITHUB_TOKEN= gh api repos/tinyland-inc/remote-juggler/labels --jq '.'  # verify labels exist first
```

---

### Day 9: Automated Conversation Flow

**Goal**: The `xa-finding-handoff` campaign runs on schedule and processes handoffs without manual intervention.

#### Deploy handoff campaign

```bash
# Copy campaign definition to ConfigMap
kubectl create configmap campaign-definitions \
  --namespace=fuzzy-dev \
  --from-file=test/campaigns/ \
  --dry-run=client -o yaml | kubectl apply -f -

# Restart campaign runner to pick up new campaigns
kubectl rollout restart deployment/campaign-runner -n fuzzy-dev
```

#### Verify campaign loaded

```bash
kubectl exec -n fuzzy-dev deploy/campaign-runner -- \
  wget -q -O- 'http://localhost:8081/campaigns' | python3 -m json.tool | grep handoff
```

#### Trigger and observe full cycle

```bash
# 1. Trigger a security scan (produces findings)
kubectl exec -n fuzzy-dev deploy/campaign-runner -- \
  wget -q -O- --post-data='' 'http://localhost:8081/trigger?campaign=hs-cred-exposure'

# 2. Wait for it to complete and publish Discussion
# (Poll status or watch logs)
kubectl logs -n fuzzy-dev deploy/campaign-runner --since=5m | grep -E 'publish|route|handoff'

# 3. The Router labels the Discussion with handoff:ironclaw
# 4. Wait for xa-finding-handoff to run (every 15 min, or trigger manually)
kubectl exec -n fuzzy-dev deploy/campaign-runner -- \
  wget -q -O- --post-data='' 'http://localhost:8081/trigger?campaign=xa-finding-handoff'

# 5. IronClaw picks up the handoff, reads Discussion, replies
# (Check logs)
kubectl logs -n fuzzy-dev deploy/ironclaw -c ironclaw --since=10m | tail -20

# 6. Verify the Discussion now has comments
GITHUB_TOKEN= gh api graphql -f query='{
  search(query: "repo:tinyland-inc/remote-juggler label:state:acknowledged", type: DISCUSSION, first: 5) {
    nodes {
      ... on Discussion {
        number title
        comments { totalCount }
        labels(first: 10) { nodes { name } }
      }
    }
  }
}' --jq '.data.search.nodes[]'
```

---

### Day 10: Validation and Documentation

**Goal**: Verify all completion metrics, document the conversation, capture lessons learned.

#### Run full test suites

```bash
# Gateway tests (should include 8+ new Discussion tool tests)
cd gateway && go test -v ./... 2>&1 | tail -5

# Campaign runner tests (should include router tests)
cd test/campaigns/runner && go test -v ./... 2>&1 | tail -5
```

#### Verify Discussion categories have content

```bash
GITHUB_TOKEN= gh api graphql -f query='{
  repository(owner:"tinyland-inc", name:"remote-juggler") {
    discussions(first: 100, orderBy: {field: CREATED_AT, direction: DESC}) {
      nodes {
        category { name }
        comments { totalCount }
      }
    }
  }
}' --jq '
  "Discussions with comments: \([.data.repository.discussions.nodes[] | select(.comments.totalCount > 0)] | length)",
  "By category:",
  (.data.repository.discussions.nodes | group_by(.category.name) | .[] | "  \(.[0].category.name): \(length)")
'
```

Expected output:
```
Discussions with comments: 3+
By category:
  Agent Reports: 140+
  Security Advisories: 1+
  Campaign Ideas: 0
  Weekly Digest: 0
```

#### Document the first conversation

Create `agent_plane/conversations/001-first-handoff.md` with:
- Discussion number and URL
- Timeline: who posted what, when
- Full rj-meta blocks from each comment
- What each agent did in response
- Whether the finding was resolved

---

## Completion Metrics

| Metric | Target | How to Verify |
|--------|--------|---------------|
| Gateway Discussion tools deployed | 5 tools | `curl /mcp tools/list \| jq '.result.tools[] \| select(.name \| startswith("github_discussion"))' \| wc -l` |
| Discussion tool unit tests | 12+ tests | `cd gateway && go test -run Discussion -v ./... 2>&1 \| grep -c PASS` |
| Router unit tests | 7+ tests | `cd test/campaigns/runner && go test -run Router -v ./... 2>&1 \| grep -c PASS` |
| Discussions with agent comments | 3+ | GraphQL query for `comments.totalCount > 0` |
| Complete handoff cycles | 1+ | Discussion with handoff -> acknowledge -> response -> resolve label chain |
| Security Advisories category used | 1+ Discussion | GraphQL query filtered by category |
| Routing labels exist on repo | 12+ labels | `gh label list --repo tinyland-inc/remote-juggler \| grep -c 'agent:\|handoff:\|severity:\|state:'` |
| rj-meta protocol in use | All new comments | Parse Discussion comments for `<!-- rj-meta` |
| All agent TOOLS.md updated | 3 files | Check Discussion section exists in each |
| New campaigns deployed | 3 (xa-finding-handoff, hs-handoff-response, oc-handoff-response) | `kubectl exec deploy/campaign-runner -- wget -qO- localhost:8081/campaigns \| grep handoff` |

---

## Risk Register

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| GitHub App token lacks Discussion write scope | Medium | Blocks Day 3 | Verify: `GITHUB_TOKEN= gh api graphql -f query='{ viewer { login } }'`. If the rj-agent-bot App token works for `createDiscussion` (Publisher already uses it), it will work for `addDiscussionComment`. Same GraphQL endpoint, same auth. |
| GraphQL API rate limiting | Low | Slows polling | GitHub GraphQL rate limit is 5,000 points/hour. Each query costs 1 point. `xa-finding-handoff` running every 15 min = 4 queries/hour per cycle = negligible. |
| IronClaw cannot call Discussion tools via rj-tool | Medium | Blocks Day 8 | The `rj-tool` wrapper sends MCP JSON-RPC to the gateway. New tools are registered in `gatewayToolNames` and `handleGatewayTool()`, so rj-tool will route them correctly. No rj-tool changes needed. Verify: `kubectl exec deploy/ironclaw -- /workspace/bin/rj-tool github_discussion_list owner=tinyland-inc repo=remote-juggler first=1` |
| HexStrike adapter prefix filter blocks Discussion tools | Low | HexStrike cannot use tools | The adapter uses `github_*` prefix. `github_discussion_*` matches. No change needed. Verify by checking adapter tool trace in logs. |
| Discussion search returns stale results | Medium | Handoff campaign misses new Discussions | GitHub search indexing can lag up to 60 seconds. The 15-minute poll interval provides ample buffer. |
| rj-meta parsing fails on malformed comments | Medium | Breaks handoff chain | `ParseRJMeta()` returns error, Router logs and continues. Handoff is not acknowledged, so it will be retried on next cycle. Add `TestParseRJMeta_Garbage` test case. |
| PVC state shadowing breaks workspace TOOLS.md updates | High | Agents use stale TOOLS.md | PVC mounts over `/home/node/.openclaw/` (IronClaw) and `/home/picoclaw/.picoclaw/` (PicoClaw). Workspace files are at `/workspace/` which is NOT PVC-mounted, so this risk does not apply. TOOLS.md and AGENT.md go in `/workspace/` baked into the image. Verify: `kubectl exec deploy/ironclaw -- ls /workspace/TOOLS.md` |
| Campaign ConfigMap too large (>1MB) | Low | New campaigns not loaded | Current ConfigMap has ~48 campaigns. Adding 3 more is well within limits. Monitor with `kubectl get configmap campaign-definitions -n fuzzy-dev -o json \| wc -c` |
| Agent replies create infinite conversation loops | Medium | Token budget explosion | The `state:acknowledged` label prevents re-processing. Max 1 handoff + 1 response per finding. The Router only labels on initial publish, not on response comments. The `xa-finding-handoff` campaign only searches for `NOT label:state:acknowledged`. |
| Gateway binary size increase | Low | Longer deploy times | Adding 5 tools (~300 lines of Go) adds <50KB to binary. Negligible. |

---

## File Manifest

### New files to create

| File | Lines (est) | Purpose |
|------|-------------|---------|
| `gateway/github_discussion_tools.go` | ~350 | 5 Discussion tool handlers + GraphQL helper |
| `gateway/github_discussion_tools_test.go` | ~400 | 12+ unit tests for Discussion tools |
| `test/campaigns/runner/router.go` | ~250 | Finding router with severity-based rules |
| `test/campaigns/runner/router_test.go` | ~200 | 7+ router unit tests |
| `test/campaigns/cross-agent/xa-finding-handoff.json` | ~50 | Handoff polling campaign |
| `test/campaigns/hexstrike/hs-handoff-response.json` | ~45 | HexStrike response campaign |
| `test/campaigns/openclaw/oc-handoff-response.json` | ~45 | IronClaw response campaign |
| `deploy/fork-dockerfiles/ironclaw/workspace/AGENT.md` | ~60 | IronClaw agent instructions |
| `agent_plane/conversations/001-first-handoff.md` | ~50 | Documentation of first conversation |

### Existing files to modify

| File | Change |
|------|--------|
| `gateway/tools.go` | Add 5 Discussion tool definitions to `gatewayTools()` |
| `gateway/mcp_proxy.go` | Add 5 tools to `gatewayToolNames` map + 5 `case` branches in `handleGatewayTool()` + `discussions` field on MCPProxy struct |
| `gateway/main.go` | Wire `proxy.discussions = NewGitHubDiscussionHandler(...)` |
| `gateway/tools_test.go` | Add 5 tool names to expected tools list |
| `test/campaigns/runner/scheduler.go` | Add Router hook in `storeResult()` (3 lines) |
| `test/campaigns/runner/publisher.go` | Add rj-meta block to `formatBody()` (~15 lines) |
| `deploy/fork-dockerfiles/ironclaw/workspace/TOOLS.md` | Add Discussion tools section |
| `deploy/fork-dockerfiles/hexstrike-ai/workspace/TOOLS.md` | Add Discussion tools section |
| `deploy/fork-dockerfiles/hexstrike-ai/workspace/AGENT.md` | Add Communication Protocol section |
| `deploy/fork-dockerfiles/picoclaw/workspace/TOOLS.md` | Add Discussion tools section |
| `deploy/fork-dockerfiles/picoclaw/workspace/AGENT.md` | Add Communication Protocol section |

### Total estimated new code

- Go: ~1,200 lines (tools + router + tests)
- JSON: ~140 lines (3 campaign definitions)
- Markdown: ~200 lines (workspace docs + conversation log)
- **Total**: ~1,540 lines
