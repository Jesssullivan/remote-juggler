# Week 1-2: Close the Tool Invocation Loop

**Epic**: Agent Plane Operational Readiness (6-week)
**Phase**: Tool Invocation -- the foundation everything else depends on
**Files in scope**: `deploy/adapters/*.go`, `test/campaigns/**/*.json`, `deploy/fork-dockerfiles/ironclaw/`

---

## Inflection Point

This phase is complete when:

1. A manual trigger of `oc-identity-audit` produces a response containing real data from `juggler_status`, `juggler_list_identities`, and `juggler_validate` -- visible as `tool_calls >= 3` in the campaign result with non-empty ToolTrace summaries that contain actual identity information (not "tool not found" or empty strings).
2. A manual trigger of `hs-cred-exposure` produces a response where at least one HexStrike native tool (`credential_scan`) returns scan data (not `"target is required"`) -- visible as a ToolTrace entry with a summary longer than 50 characters containing repo names.
3. A manual trigger of `pc-identity-audit` reports `tool_calls >= 2` in the campaign result (not hardcoded 0).
4. The campaign runner's `/campaigns` endpoint shows `lastResult != null` for at least 35 of 47 enabled campaigns.

Measurable gate: All four conditions verified via `kubectl exec` commands against the live cluster, documented with timestamps.

---

## The Three Root Causes

Before the day-by-day plan, here is the precise diagnosis:

### Root Cause 1: IronClaw -- Tools Listed as Text, Not Function Definitions

**File**: `deploy/adapters/ironclaw.go`, lines 93-101

```go
// Current code (broken):
prompt := fmt.Sprintf("Campaign: %s (run_id: %s)\n\n", c.Name, runID)
for i, step := range c.Process {
    prompt += fmt.Sprintf("%d. %s\n", i+1, step)
}
if len(c.Tools) > 0 {
    prompt += fmt.Sprintf("\nAvailable MCP tools: %v\n", c.Tools)  // LINE 99: THIS IS THE BUG
}
```

Line 99 renders tools as a Go string slice: `Available MCP tools: [juggler_status juggler_list_identities]`. This is plain text in the user message. OpenClaw's LLM sees tool names but has no function definitions to call. The `/v1/responses` API supports a `tools` array in the request payload (OpenAI-compatible function calling), but we never populate it. The LLM cannot produce `function_call` output items without tool definitions in the request.

Additionally, lines 80-88 parse only `process`, `tools`, `name`, `id`, `mode`, and `model` from the campaign JSON. The `description`, `targets`, `guardrails`, `metrics` (successCriteria, KPIs), and `outputs` fields are all silently dropped.

### Root Cause 2: HexStrike -- Adapter Sends Wrong Argument Names

**File**: `deploy/adapters/hexstrike.go`, lines 91-98

```go
// Current code (broken):
args := map[string]any{
    "campaign_id": c.ID,
    "run_id":      runID,
}
if len(targetRepos) > 0 {
    args["targets"] = targetRepos  // "targets" is an array of "org/repo" strings
}
```

The OCaml MCP tools expect a `target` parameter (singular) of type string (e.g., `"tinyland-inc/remote-juggler"`), not `targets` (plural, array). Every tool validates `target` as required, finds it missing, and returns `"target is required"`. The adapter also passes `campaign_id` and `run_id` which are unknown parameters that the OCaml tools ignore.

Furthermore, HexStrike tools like `credential_scan`, `vuln_scan`, `container_scan`, `port_scan`, `tls_check`, and `network_posture` each have different required parameters beyond just `target`. The adapter sends the same generic argument set to every tool.

### Root Cause 3: TinyClaw -- Tool Calls Hardcoded to Zero

**File**: `deploy/adapters/tinyclaw.go`, line 187

```go
return &LastResult{
    Status:    "success",
    ToolCalls: 0, // TinyClaw doesn't report individual tool calls in dispatch response
    Findings:  findings,
}, nil
```

TinyClaw's `/api/dispatch` response format is `{content, finish_reason, error}` and genuinely does not include tool call counts. However, the TinyClaw agent internally uses tools -- we just never extract that information. The `content` field may contain references to tool usage that we could parse, or we could query `/api/status` for tool metrics after dispatch.

---

## Day-by-Day Plan

### Day 1: IronClaw Prompt Enrichment

**Objective**: Pass the full campaign context to IronClaw's LLM, not just process steps and tool names as text.

#### Change 1: Expand campaign struct parsing

**File**: `deploy/adapters/ironclaw.go`, lines 81-88

Replace the minimal struct with full campaign field extraction:

```go
// BEFORE (line 81-88):
var c struct {
    ID      string   `json:"id"`
    Name    string   `json:"name"`
    Process []string `json:"process"`
    Tools   []string `json:"tools"`
    Mode    string   `json:"mode"`
    Model   string   `json:"model"`
}

// AFTER:
var c struct {
    ID          string   `json:"id"`
    Name        string   `json:"name"`
    Description string   `json:"description"`
    Process     []string `json:"process"`
    Tools       []string `json:"tools"`
    Mode        string   `json:"mode"`
    Model       string   `json:"model"`
    Targets     []struct {
        Forge  string `json:"forge"`
        Org    string `json:"org"`
        Repo   string `json:"repo"`
        Branch string `json:"branch"`
    } `json:"targets"`
    Guardrails struct {
        MaxDuration string `json:"maxDuration"`
        ReadOnly    bool   `json:"readOnly"`
    } `json:"guardrails"`
    Metrics struct {
        SuccessCriteria string   `json:"successCriteria"`
        KPIs            []string `json:"kpis"`
    } `json:"metrics"`
    Outputs struct {
        SetecKey string `json:"setecKey"`
    } `json:"outputs"`
}
```

#### Change 2: Build enriched prompt

**File**: `deploy/adapters/ironclaw.go`, lines 93-101

Replace the sparse prompt builder with a structured template:

```go
// AFTER (replaces lines 93-101):
var promptBuilder strings.Builder

promptBuilder.WriteString(fmt.Sprintf("# Campaign: %s\n", c.Name))
promptBuilder.WriteString(fmt.Sprintf("**Run ID**: %s\n", runID))
if c.Description != "" {
    promptBuilder.WriteString(fmt.Sprintf("**Purpose**: %s\n", c.Description))
}
promptBuilder.WriteString("\n")

// Targets
if len(c.Targets) > 0 {
    promptBuilder.WriteString("## Targets\n")
    for _, t := range c.Targets {
        branch := t.Branch
        if branch == "" {
            branch = "main"
        }
        promptBuilder.WriteString(fmt.Sprintf("- %s/%s (branch: %s, forge: %s)\n", t.Org, t.Repo, branch, t.Forge))
    }
    promptBuilder.WriteString("\n")
}

// Process steps
promptBuilder.WriteString("## Process\n")
for i, step := range c.Process {
    promptBuilder.WriteString(fmt.Sprintf("%d. %s\n", i+1, step))
}
promptBuilder.WriteString("\n")

// Tools -- tell the LLM to use exec("rj-tool ...") since MCP is not available
if len(c.Tools) > 0 {
    promptBuilder.WriteString("## Available Tools\n")
    promptBuilder.WriteString("Call tools via the exec command: `exec(\"/workspace/bin/rj-tool <tool_name> key=value ...\")`\n\n")
    for _, tool := range c.Tools {
        promptBuilder.WriteString(fmt.Sprintf("- `%s`\n", tool))
    }
    promptBuilder.WriteString("\n")
}

// Guardrails
promptBuilder.WriteString("## Constraints\n")
if c.Guardrails.MaxDuration != "" {
    promptBuilder.WriteString(fmt.Sprintf("- **Max Duration**: %s\n", c.Guardrails.MaxDuration))
}
if c.Guardrails.ReadOnly {
    promptBuilder.WriteString("- **Read-Only**: Do NOT create branches, PRs, or modify repositories\n")
}
promptBuilder.WriteString("\n")

// Success criteria and KPIs
if c.Metrics.SuccessCriteria != "" {
    promptBuilder.WriteString(fmt.Sprintf("## Success Criteria\n%s\n\n", c.Metrics.SuccessCriteria))
}
if len(c.Metrics.KPIs) > 0 {
    promptBuilder.WriteString("## KPIs to Track\n")
    for _, kpi := range c.Metrics.KPIs {
        promptBuilder.WriteString(fmt.Sprintf("- %s\n", kpi))
    }
    promptBuilder.WriteString("\n")
}

// Output location
if c.Outputs.SetecKey != "" {
    promptBuilder.WriteString(fmt.Sprintf("## Output\nStore results via: `exec(\"/workspace/bin/rj-tool juggler_setec_put name=%s value=<JSON>\")`\n\n", c.Outputs.SetecKey))
}

promptBuilder.WriteString(findingsInstruction)
prompt := promptBuilder.String()
```

This requires adding `"strings"` to the import block at line 3.

#### Change 3: Add `strings` import

**File**: `deploy/adapters/ironclaw.go`, line 3-10

Add `"strings"` to the import block (it is not currently imported in ironclaw.go).

#### Test Plan

Add a new test to `deploy/adapters/ironclaw_test.go`:

```go
func TestIronclawBackend_DispatchEnrichedPrompt(t *testing.T) {
    var capturedBody []byte
    agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        capturedBody, _ = io.ReadAll(r.Body)
        json.NewEncoder(w).Encode(map[string]any{
            "id": "resp_1", "status": "completed", "output": []any{},
        })
    }))
    defer agent.Close()

    b := NewIronclawBackend(agent.URL)
    campaign := json.RawMessage(`{
        "id":"oc-dep-audit",
        "name":"Cross-Repo Dependency Audit",
        "description":"Audits dependency manifests across all repos",
        "process":["Fetch manifests","Parse dependencies","Find divergences"],
        "tools":["github_fetch","juggler_setec_put"],
        "targets":[{"forge":"github","org":"tinyland-inc","repo":"remote-juggler","branch":"main"}],
        "guardrails":{"maxDuration":"30m","readOnly":true},
        "metrics":{"successCriteria":"All repos scanned","kpis":["repos_scanned","divergences"]},
        "outputs":{"setecKey":"remotejuggler/campaigns/oc-dep-audit"}
    }`)

    _, err := b.Dispatch(campaign, "run-enriched-1")
    if err != nil {
        t.Fatalf("dispatch error: %v", err)
    }

    // Parse the sent payload to extract the prompt.
    var payload map[string]any
    json.Unmarshal(capturedBody, &payload)
    input := payload["input"].([]any)
    msg := input[0].(map[string]any)
    content := msg["content"].(string)

    checks := []string{
        "Cross-Repo Dependency Audit",
        "Audits dependency manifests",
        "tinyland-inc/remote-juggler",
        "rj-tool",
        "github_fetch",
        "Read-Only",
        "All repos scanned",
        "repos_scanned",
        "remotejuggler/campaigns/oc-dep-audit",
    }
    for _, check := range checks {
        if !strings.Contains(content, check) {
            t.Errorf("prompt missing expected content: %q", check)
        }
    }
}
```

Run tests:
```bash
cd /home/jsullivan2/git/RemoteJuggler/deploy/adapters && go test -v -run TestIronclaw ./...
```

---

### Day 2: IronClaw Tool Registration via rj-tool Exec Pattern

**Objective**: Decide and implement the tool invocation strategy for IronClaw.

#### Decision: rj-tool exec wrapper (NOT /v1/responses function definitions)

**Why not function definitions in /v1/responses**:
- OpenClaw's `/v1/responses` endpoint does support the `tools` array, but the tools must resolve locally within OpenClaw. When OpenClaw sees `function_call` output, it expects to execute the function itself (or return `incomplete` status for the client to provide results). The adapter would need to implement a multi-turn loop: send request -> get function_calls -> execute tools -> send results back -> repeat.
- This is a massive refactor of `ironclaw.go` from a single HTTP roundtrip to a stateful conversation loop with tool execution middleware.
- The `rj-tool` wrapper already works -- the problem is the LLM does not know to use it.

**Why rj-tool exec is the right approach for Week 1-2**:
- OpenClaw has an `exec` built-in tool that runs shell commands in the workspace.
- The `rj-tool` wrapper (`/workspace/bin/rj-tool`) is already deployed, tested, and handles JSON-RPC to the gateway.
- We only need the LLM to know the pattern: `exec("/workspace/bin/rj-tool tool_name key=value")`.
- Day 1's prompt enrichment already includes this instruction.

#### Change 1: Validate rj-tool is accessible and working

Create a validation test that simulates what OpenClaw would do:

**File**: New test in `deploy/adapters/ironclaw_test.go`

```go
func TestIronclawBackend_DispatchExecToolPattern(t *testing.T) {
    // Verify the prompt instructs the LLM to use exec() with rj-tool.
    var capturedBody []byte
    agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        capturedBody, _ = io.ReadAll(r.Body)
        // Simulate OpenClaw responding with exec tool calls.
        json.NewEncoder(w).Encode(map[string]any{
            "id":     "resp_exec",
            "status": "completed",
            "output": []map[string]any{
                {
                    "type":      "function_call",
                    "id":        "call_exec_1",
                    "name":      "exec",
                    "call_id":   "exec_1",
                    "arguments": `{"command":"/workspace/bin/rj-tool juggler_status"}`,
                },
                {
                    "type":    "message",
                    "role":    "assistant",
                    "content": "Identity check complete. Bot identity rj-agent-bot is configured.",
                },
            },
        })
    }))
    defer agent.Close()

    b := NewIronclawBackend(agent.URL)
    campaign := json.RawMessage(`{
        "id":"oc-identity-audit",
        "name":"IronClaw Identity Audit",
        "process":["Query identity"],
        "tools":["juggler_status"]
    }`)

    result, err := b.Dispatch(campaign, "run-exec-1")
    if err != nil {
        t.Fatalf("dispatch error: %v", err)
    }
    // The exec() call should be counted as a tool call.
    if result.ToolCalls < 1 {
        t.Errorf("expected at least 1 tool call (exec), got %d", result.ToolCalls)
    }

    // Verify prompt contains exec instruction.
    var payload map[string]any
    json.Unmarshal(capturedBody, &payload)
    input := payload["input"].([]any)
    msg := input[0].(map[string]any)
    content := msg["content"].(string)
    if !strings.Contains(content, "/workspace/bin/rj-tool") {
        t.Error("prompt should contain rj-tool exec instruction")
    }
}
```

#### Change 2: Count exec() calls as tool invocations in the response parser

**File**: `deploy/adapters/ironclaw.go`, lines 178-205

The existing code already counts `function_call` output items (line 184-189). When OpenClaw uses `exec()`, it produces a `function_call` with `name: "exec"`. We should track these but extract the actual tool name from the arguments:

```go
// Enhanced tool trace extraction (replaces lines 179-205):
var trace []ToolTrace
var textContent string
for _, item := range respData.Output {
    ts := time.Now().UTC().Format(time.RFC3339)
    switch item.Type {
    case "function_call":
        toolName := item.Name
        summary := truncate(item.Args, 200)

        // If this is an exec() call to rj-tool, extract the actual tool name.
        if toolName == "exec" {
            var execArgs struct {
                Command string `json:"command"`
            }
            if json.Unmarshal([]byte(item.Args), &execArgs) == nil {
                if strings.Contains(execArgs.Command, "rj-tool") {
                    parts := strings.Fields(execArgs.Command)
                    for i, p := range parts {
                        if strings.HasSuffix(p, "rj-tool") && i+1 < len(parts) {
                            toolName = parts[i+1] // e.g., "juggler_status"
                            break
                        }
                    }
                }
            }
        }

        trace = append(trace, ToolTrace{
            Timestamp: ts,
            Tool:      toolName,
            Summary:   summary,
        })
    case "message":
        switch v := item.Content.(type) {
        case string:
            textContent = v
        case []any:
            for _, part := range v {
                if m, ok := part.(map[string]any); ok {
                    if text, ok := m["text"].(string); ok {
                        textContent += text
                    }
                }
            }
        }
    }
}
```

#### Test Execution

```bash
cd /home/jsullivan2/git/RemoteJuggler/deploy/adapters && go test -v -run TestIronclaw ./...
```

---

### Day 3: IronClaw E2E Verification

**Objective**: Deploy Day 1-2 changes, trigger real campaigns, verify tool_calls > 0.

#### Step 1: Build and push updated adapter image

```bash
# From the repo root:
cd /home/jsullivan2/git/RemoteJuggler

# Build the adapter binary
cd deploy/adapters && go build -o adapter . && cd ../..

# The adapter is built as part of the container image via deploy/docker/Dockerfile.adapter
# Trigger the containers workflow or build locally:
docker build -f deploy/docker/Dockerfile.adapter -t ghcr.io/tinyland-inc/remote-juggler/adapter:edge deploy/adapters/

# Push (requires GHCR auth):
docker push ghcr.io/tinyland-inc/remote-juggler/adapter:edge
```

Or trigger the GitHub Actions workflow:
```bash
gh workflow run containers.yml --ref main
```

#### Step 2: Restart IronClaw deployment to pick up new adapter

```bash
kubectl rollout restart deployment/ironclaw -n fuzzy-dev
kubectl rollout status deployment/ironclaw -n fuzzy-dev --timeout=180s
```

#### Step 3: Trigger test campaigns

Campaign 1: `oc-identity-audit` (simple, 6 tools, 15m timeout)
```bash
# Trigger via campaign runner API
kubectl exec -n fuzzy-dev deploy/rj-gateway -- \
  wget -qO- --post-data='' 'http://localhost:8081/trigger?campaign=oc-identity-audit'
```

Wait 2-3 minutes, then check:
```bash
kubectl exec -n fuzzy-dev deploy/rj-gateway -- \
  wget -qO- 'http://localhost:8081/status'
```

Expected output: `tool_calls >= 3`, ToolTrace entries for `juggler_status`, `juggler_list_identities`, `juggler_validate`.

Campaign 2: `oc-credential-health` (medium complexity)
```bash
kubectl exec -n fuzzy-dev deploy/rj-gateway -- \
  wget -qO- --post-data='' 'http://localhost:8081/trigger?campaign=oc-credential-health'
```

Campaign 3: `xa-platform-health` (cross-agent, 5 tools)
```bash
kubectl exec -n fuzzy-dev deploy/rj-gateway -- \
  wget -qO- --post-data='' 'http://localhost:8081/trigger?campaign=xa-platform-health'
```

#### Expected Outputs

For `oc-identity-audit`:
```json
{
  "status": "success",
  "tool_calls": 6,
  "tool_trace": [
    {"tool": "juggler_status", "summary": "Current identity: rj-agent-bot..."},
    {"tool": "juggler_list_identities", "summary": "Found 2 identities..."},
    {"tool": "juggler_validate", "summary": "SSH connectivity OK..."},
    {"tool": "juggler_token_verify", "summary": "Token valid, scopes: repo..."},
    {"tool": "juggler_gpg_status", "summary": "GPG not configured..."},
    {"tool": "juggler_audit_log", "summary": "20 recent entries..."}
  ]
}
```

#### Verification Queries

```bash
# Check the campaign result stored in Setec:
kubectl exec -n fuzzy-dev deploy/rj-gateway -- \
  wget -qO- --post-data='{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"juggler_setec_get","arguments":{"name":"campaigns/oc-identity-audit"}}}' \
  -H 'Content-Type: application/json' \
  'http://localhost:8080/mcp'
```

#### Rollback Plan

If tools cause issues (agent stuck, crash loops, excessive token usage):

1. Revert the adapter image to the previous SHA:
   ```bash
   kubectl set image deployment/ironclaw adapter=ghcr.io/tinyland-inc/remote-juggler/adapter:sha-d599092 -n fuzzy-dev
   ```

2. If the campaign runner is stuck polling, restart it:
   ```bash
   kubectl rollout restart deployment/rj-gateway -n fuzzy-dev
   ```

3. Check for runaway token usage:
   ```bash
   kubectl exec -n fuzzy-dev deploy/rj-gateway -- \
     wget -qO- --post-data='{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"juggler_aperture_usage","arguments":{}}}' \
     -H 'Content-Type: application/json' \
     'http://localhost:8080/mcp'
   ```

---

### Day 4: HexStrike Parameter Schema Fix

**Objective**: Fix the argument schema mismatch so HexStrike native tools receive correctly-named parameters.

#### Step 1: Discover actual HexStrike tool schemas

The HexStrike MCP server exposes `tools/list`. Query it from inside the cluster:

```bash
kubectl exec -n fuzzy-dev deploy/hexstrike-ai -- \
  wget -qO- --post-data='{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' \
  -H 'Content-Type: application/json' \
  'http://127.0.0.1:9080/mcp'
```

This returns the full inputSchema for all 42 tools. Pipe through jq to extract parameter names:

```bash
kubectl exec -n fuzzy-dev deploy/hexstrike-ai -- \
  wget -qO- --post-data='{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' \
  -H 'Content-Type: application/json' \
  'http://127.0.0.1:9080/mcp' 2>/dev/null | \
  python3 -c "
import sys, json
data = json.load(sys.stdin)
tools = data.get('result', {}).get('tools', [])
for t in tools:
    name = t['name']
    props = t.get('inputSchema', {}).get('properties', {})
    required = t.get('inputSchema', {}).get('required', [])
    print(f'{name}: {list(props.keys())} (required: {required})')
"
```

Based on the memory notes and HexStrike-AI v2 architecture (OCaml MCP with F*-verified tools), the expected parameter pattern for security tools is:

| Tool | Expected Parameters | Required |
|------|-------------------|----------|
| `credential_scan` | `target` (string, "org/repo"), `branch` (string), `depth` (int) | `target` |
| `vuln_scan` | `target` (string), `severity_threshold` (string) | `target` |
| `container_scan` | `image` (string, GHCR image ref) | `image` |
| `port_scan` | `target` (string, hostname/IP), `ports` (string, range) | `target` |
| `tls_check` | `target` (string, hostname) | `target` |
| `network_posture` | `namespace` (string), `cluster` (string) | `namespace` |

#### Step 2: Create a tool schema registry

**File**: `deploy/adapters/hexstrike.go` -- add after line 11 (after import block)

```go
// hexstrikeToolSchemas maps HexStrike native tool names to their expected
// parameter builders. Each function takes the campaign context and returns
// the correct arguments for that specific tool.
type hexstrikeArgBuilder func(campaignID, runID string, targets []string) map[string]any

var hexstrikeToolArgs = map[string]hexstrikeArgBuilder{
    "credential_scan": func(cid, rid string, targets []string) map[string]any {
        args := map[string]any{}
        if len(targets) > 0 {
            args["target"] = targets[0] // "org/repo" format
        }
        args["branch"] = "main"
        return args
    },
    "vuln_scan": func(cid, rid string, targets []string) map[string]any {
        args := map[string]any{}
        if len(targets) > 0 {
            args["target"] = targets[0]
        }
        args["severity_threshold"] = "high"
        return args
    },
    "container_scan": func(cid, rid string, targets []string) map[string]any {
        args := map[string]any{}
        if len(targets) > 0 {
            // Convert org/repo to GHCR image reference
            args["image"] = "ghcr.io/" + targets[0]
        }
        return args
    },
    "port_scan": func(cid, rid string, targets []string) map[string]any {
        args := map[string]any{}
        if len(targets) > 0 {
            args["target"] = targets[0]
        }
        return args
    },
    "tls_check": func(cid, rid string, targets []string) map[string]any {
        args := map[string]any{}
        if len(targets) > 0 {
            args["target"] = targets[0]
        }
        return args
    },
    "network_posture": func(cid, rid string, targets []string) map[string]any {
        return map[string]any{
            "namespace": "fuzzy-dev",
        }
    },
    "sops_rotation_check": func(cid, rid string, targets []string) map[string]any {
        args := map[string]any{}
        if len(targets) > 0 {
            args["target"] = targets[0]
        }
        return args
    },
    "cve_monitor": func(cid, rid string, targets []string) map[string]any {
        args := map[string]any{}
        if len(targets) > 0 {
            args["target"] = targets[0]
        }
        return args
    },
}
```

**Important**: The exact parameter names MUST be confirmed by the `tools/list` query on Day 4. The registry above is a best-guess template. Update it after running the query.

#### Step 3: Fix the dispatch loop to use correct arguments

**File**: `deploy/adapters/hexstrike.go`, lines 78-98

Replace the current generic argument builder:

```go
// BEFORE (lines 78-98):
for _, toolName := range c.Tools {
    ts := time.Now().UTC().Format(time.RFC3339)
    if strings.HasPrefix(toolName, "juggler_") || strings.HasPrefix(toolName, "github_") {
        trace = append(trace, ToolTrace{
            Timestamp: ts,
            Tool:      toolName,
            Summary:   "skipped (gateway tool)",
        })
        continue
    }
    args := map[string]any{
        "campaign_id": c.ID,
        "run_id":      runID,
    }
    if len(targetRepos) > 0 {
        args["targets"] = targetRepos
    }
    result, err := b.callMCPTool(toolName, args)

// AFTER:
for _, toolName := range c.Tools {
    ts := time.Now().UTC().Format(time.RFC3339)
    if strings.HasPrefix(toolName, "juggler_") || strings.HasPrefix(toolName, "github_") {
        trace = append(trace, ToolTrace{
            Timestamp: ts,
            Tool:      toolName,
            Summary:   "skipped (gateway tool)",
        })
        continue
    }

    // Build tool-specific arguments using the schema registry.
    var args map[string]any
    if builder, ok := hexstrikeToolArgs[toolName]; ok {
        args = builder(c.ID, runID, targetRepos)
    } else {
        // Unknown tool -- use minimal fallback with "target" (singular).
        args = map[string]any{}
        if len(targetRepos) > 0 {
            args["target"] = targetRepos[0]
        }
    }

    result, err := b.callMCPTool(toolName, args)
```

#### Step 4: Multi-target dispatch

Many campaigns target multiple repos. The current code sends all targets to one tool call. For HexStrike tools that accept a single `target`, we need to loop:

**File**: `deploy/adapters/hexstrike.go` -- refactor the dispatch loop

```go
// For tools that take a single target, dispatch once per target repo.
for _, toolName := range c.Tools {
    ts := time.Now().UTC().Format(time.RFC3339)
    if strings.HasPrefix(toolName, "juggler_") || strings.HasPrefix(toolName, "github_") {
        trace = append(trace, ToolTrace{
            Timestamp: ts,
            Tool:      toolName,
            Summary:   "skipped (gateway tool)",
        })
        continue
    }

    // Tools that don't need per-target dispatch.
    if toolName == "network_posture" {
        args := map[string]any{"namespace": "fuzzy-dev"}
        if builder, ok := hexstrikeToolArgs[toolName]; ok {
            args = builder(c.ID, runID, targetRepos)
        }
        result, err := b.callMCPTool(toolName, args)
        if err != nil {
            trace = append(trace, ToolTrace{Timestamp: ts, Tool: toolName, Summary: fmt.Sprintf("error: %v", err), IsError: true})
            lastErr = err.Error()
        } else {
            summary := "ok"
            if result != "" {
                summary = truncate(result, 200)
                accumulatedOutput.WriteString(result + "\n")
            }
            trace = append(trace, ToolTrace{Timestamp: ts, Tool: toolName, Summary: summary})
        }
        continue
    }

    // Per-target dispatch for tools that need it.
    dispatchTargets := targetRepos
    if len(dispatchTargets) == 0 {
        dispatchTargets = []string{""} // Still call once with empty target.
    }
    for _, target := range dispatchTargets {
        ts := time.Now().UTC().Format(time.RFC3339)
        var args map[string]any
        if builder, ok := hexstrikeToolArgs[toolName]; ok {
            args = builder(c.ID, runID, []string{target})
        } else {
            args = map[string]any{}
            if target != "" {
                args["target"] = target
            }
        }

        result, err := b.callMCPTool(toolName, args)
        if err != nil {
            trace = append(trace, ToolTrace{Timestamp: ts, Tool: toolName, Summary: fmt.Sprintf("error (%s): %v", target, err), IsError: true})
            lastErr = err.Error()
            continue
        }
        summary := "ok"
        if result != "" {
            summary = truncate(result, 200)
            accumulatedOutput.WriteString(result + "\n")
        }
        trace = append(trace, ToolTrace{Timestamp: ts, Tool: toolName, Summary: fmt.Sprintf("[%s] %s", target, summary)})
    }
}
```

#### Test Plan

Update `deploy/adapters/hexstrike_test.go` -- add test verifying correct parameter name:

```go
func TestHexstrikeBackend_CorrectTargetParameter(t *testing.T) {
    var receivedArgs map[string]any

    agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        body, _ := io.ReadAll(r.Body)
        var req struct {
            Params struct {
                Name string         `json:"name"`
                Args map[string]any `json:"arguments"`
            } `json:"params"`
        }
        json.Unmarshal(body, &req)
        receivedArgs = req.Params.Args

        result, _ := json.Marshal(map[string]any{
            "content": []map[string]string{
                {"type": "text", "text": "scan complete"},
            },
        })
        json.NewEncoder(w).Encode(map[string]any{
            "result": json.RawMessage(result),
        })
    }))
    defer agent.Close()

    b := NewHexstrikeBackend(agent.URL)
    campaign := json.RawMessage(`{
        "id": "hs-cred-exposure",
        "name": "Credential Exposure Scan",
        "process": ["scan"],
        "tools": ["credential_scan"],
        "targets": [{"org":"tinyland-inc","repo":"remote-juggler"}]
    }`)

    _, err := b.Dispatch(campaign, "run-param-fix")
    if err != nil {
        t.Fatalf("dispatch error: %v", err)
    }

    // Verify "target" (singular) is sent, not "targets" (plural).
    if _, ok := receivedArgs["targets"]; ok {
        t.Error("should not send 'targets' (plural) parameter")
    }
    if receivedArgs["target"] != "tinyland-inc/remote-juggler" {
        t.Errorf("expected target=tinyland-inc/remote-juggler, got %v", receivedArgs["target"])
    }
    // Verify campaign_id and run_id are NOT sent (tools don't expect them).
    if _, ok := receivedArgs["campaign_id"]; ok {
        t.Error("should not send campaign_id to HexStrike native tools")
    }
}
```

Run:
```bash
cd /home/jsullivan2/git/RemoteJuggler/deploy/adapters && go test -v -run TestHexstrike ./...
```

---

### Day 5: HexStrike E2E Verification

**Objective**: Deploy Day 4 changes and verify HexStrike tools return real scan data.

#### Step 1: Query tools/list to confirm schemas

Before deploying, SSH into the cluster and confirm the actual parameter names:

```bash
# Get the actual tool schemas from the running HexStrike pod
kubectl exec -n fuzzy-dev deploy/hexstrike-ai -c hexstrike -- \
  wget -qO- --post-data='{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' \
  -H 'Content-Type: application/json' \
  'http://127.0.0.1:9080/mcp' 2>/dev/null | python3 -m json.tool > /tmp/hexstrike-tools.json

# Review the output
cat /tmp/hexstrike-tools.json | python3 -c "
import sys, json
data = json.load(sys.stdin)
for t in data.get('result', {}).get('tools', []):
    print(f\"  {t['name']}: required={t.get('inputSchema',{}).get('required',[])} props={list(t.get('inputSchema',{}).get('properties',{}).keys())}\")
"
```

**If schemas differ from Day 4 assumptions**, update `hexstrikeToolArgs` in `hexstrike.go` before deploying.

#### Step 2: Build and deploy

```bash
# Rebuild adapter
cd /home/jsullivan2/git/RemoteJuggler/deploy/adapters && go build -o adapter .

# Build and push container (or trigger CI)
gh workflow run containers.yml --ref main

# Restart hexstrike deployment
kubectl rollout restart deployment/hexstrike-ai -n fuzzy-dev
kubectl rollout status deployment/hexstrike-ai -n fuzzy-dev --timeout=180s
```

#### Step 3: Trigger hs-cred-exposure

```bash
kubectl exec -n fuzzy-dev deploy/rj-gateway -- \
  wget -qO- --post-data='' 'http://localhost:8081/trigger?campaign=hs-cred-exposure'
```

Wait ~5 minutes (45m timeout but should complete faster), then check:

```bash
kubectl exec -n fuzzy-dev deploy/rj-gateway -- \
  wget -qO- 'http://localhost:8081/status'
```

#### Expected Output

```json
{
  "status": "success",
  "tool_calls": 8,
  "tool_trace": [
    {"tool": "credential_scan", "summary": "[tinyland-inc/remote-juggler] Scanned 142 files, 3 commits with high-entropy strings..."},
    {"tool": "credential_scan", "summary": "[tinyland-inc/tinyland.dev] Scanned 89 files..."},
    ...
    {"tool": "juggler_resolve_composite", "summary": "skipped (gateway tool)"},
    {"tool": "juggler_setec_put", "summary": "skipped (gateway tool)"}
  ]
}
```

The key verification: `credential_scan` entries must have summaries longer than 50 characters that reference repo names and file counts, NOT `"target is required"`.

#### Step 4: Trigger a second campaign

```bash
kubectl exec -n fuzzy-dev deploy/rj-gateway -- \
  wget -qO- --post-data='' 'http://localhost:8081/trigger?campaign=hs-container-vuln'
```

Verify `container_scan` returns image analysis data.

#### Rollback

```bash
kubectl set image deployment/hexstrike-ai adapter=ghcr.io/tinyland-inc/remote-juggler/adapter:sha-d599092 -n fuzzy-dev
```

---

### Day 6: TinyClaw Tool Call Tracking

**Objective**: Fix the hardcoded `ToolCalls: 0` in tinyclaw.go so campaign results reflect actual tool usage.

#### Approach: Parse tool usage from TinyClaw's response content

TinyClaw's `/api/dispatch` returns `{content, finish_reason}`. The `content` field contains the LLM's full response, which includes evidence of tool calls (TinyClaw uses Anthropic's native tool_use). We can extract tool call counts by:

1. **Option A**: Query `/api/status` after dispatch for tool metrics (if TinyClaw tracks them).
2. **Option B**: Parse the content for tool_use evidence patterns.
3. **Option C**: Count `exec()` or function invocations mentioned in the response text.

#### Step 1: Check if TinyClaw's /api/status exposes tool metrics

```bash
kubectl exec -n fuzzy-dev deploy/tinyclaw-agent -c tinyclaw -- \
  wget -qO- 'http://127.0.0.1:18790/api/status'
```

If the status response includes tool call counts, use Option A.

#### Step 2: Implement Option A (preferred) or Option B

**File**: `deploy/adapters/tinyclaw.go`, lines 162-190

If TinyClaw's `/api/status` includes a `tool_calls` or `tools_used` field after dispatch:

```go
// AFTER (replaces lines 162-190):

// Parse TinyClaw dispatch response.
var dispatchResp struct {
    Content      string `json:"content"`
    FinishReason string `json:"finish_reason"`
    Error        string `json:"error"`
    ToolCalls    int    `json:"tool_calls"` // May be populated by TinyClaw
}
if err := json.Unmarshal(respBody, &dispatchResp); err != nil {
    return &LastResult{
        Status:    "success",
        ToolCalls: 0,
    }, nil
}

if dispatchResp.FinishReason == "error" || dispatchResp.Error != "" {
    return &LastResult{
        Status: "failure",
        Error:  dispatchResp.Error,
    }, nil
}

findings := extractFindings(dispatchResp.Content, c.ID, runID)

// Count tool calls: use response field if available, otherwise estimate from content.
toolCalls := dispatchResp.ToolCalls
if toolCalls == 0 && dispatchResp.Content != "" {
    // Heuristic: count lines matching tool invocation patterns.
    // TinyClaw's LLM output includes "Tool: <name>" or "Called: <name>" patterns.
    toolCalls = countToolReferences(dispatchResp.Content, c.Tools)
}

return &LastResult{
    Status:    "success",
    ToolCalls: toolCalls,
    Findings:  findings,
}, nil
```

Add the helper function:

```go
// countToolReferences scans LLM output for evidence of tool invocations.
// This is a heuristic for agents that don't report structured tool call data.
func countToolReferences(content string, knownTools []string) int {
    count := 0
    contentLower := strings.ToLower(content)
    for _, tool := range knownTools {
        // Count each unique tool mention as one invocation.
        if strings.Contains(contentLower, strings.ToLower(tool)) {
            count++
        }
    }
    return count
}
```

#### Step 3: Also pass description, targets, guardrails to TinyClaw

Apply the same prompt enrichment as IronClaw. Update the campaign struct parsing:

**File**: `deploy/adapters/tinyclaw.go`, lines 105-111

```go
// BEFORE:
var c struct {
    ID      string   `json:"id"`
    Name    string   `json:"name"`
    Process []string `json:"process"`
    Tools   []string `json:"tools"`
    Model   string   `json:"model"`
}

// AFTER:
var c struct {
    ID          string   `json:"id"`
    Name        string   `json:"name"`
    Description string   `json:"description"`
    Process     []string `json:"process"`
    Tools       []string `json:"tools"`
    Model       string   `json:"model"`
    Targets     []struct {
        Org  string `json:"org"`
        Repo string `json:"repo"`
    } `json:"targets"`
    Guardrails struct {
        MaxDuration string `json:"maxDuration"`
        ReadOnly    bool   `json:"readOnly"`
    } `json:"guardrails"`
    Metrics struct {
        SuccessCriteria string   `json:"successCriteria"`
        KPIs            []string `json:"kpis"`
    } `json:"metrics"`
    Outputs struct {
        SetecKey string `json:"setecKey"`
    } `json:"outputs"`
}
```

Update the prompt builder (lines 117-125):

```go
// AFTER (replaces lines 117-125):
var promptBuilder strings.Builder
promptBuilder.WriteString(fmt.Sprintf("# Campaign: %s (run_id: %s)\n\n", c.Name, runID))
if c.Description != "" {
    promptBuilder.WriteString(fmt.Sprintf("**Purpose**: %s\n\n", c.Description))
}
if len(c.Targets) > 0 {
    promptBuilder.WriteString("## Targets\n")
    for _, t := range c.Targets {
        promptBuilder.WriteString(fmt.Sprintf("- %s/%s\n", t.Org, t.Repo))
    }
    promptBuilder.WriteString("\n")
}
promptBuilder.WriteString("## Process\n")
for i, step := range c.Process {
    promptBuilder.WriteString(fmt.Sprintf("%d. %s\n", i+1, step))
}
if c.Guardrails.ReadOnly {
    promptBuilder.WriteString("\n**Read-Only mode**: Do NOT modify any repositories.\n")
}
if c.Metrics.SuccessCriteria != "" {
    promptBuilder.WriteString(fmt.Sprintf("\n## Success Criteria\n%s\n", c.Metrics.SuccessCriteria))
}

// Inject workspace skills.
promptBuilder.WriteString(b.loadSkills())
promptBuilder.WriteString(findingsInstruction)

prompt := promptBuilder.String()
```

#### Test Plan

Add to `deploy/adapters/tinyclaw_test.go`:

```go
func TestPicoclawBackend_ToolCallCounting(t *testing.T) {
    agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        json.NewEncoder(w).Encode(map[string]string{
            "content":       "I called juggler_status and it returned the current identity. Then I called juggler_list_identities and found 2 identities configured.",
            "finish_reason": "stop",
        })
    }))
    defer agent.Close()

    b := NewPicoclawBackend(agent.URL, "")
    campaign := json.RawMessage(`{
        "id":"pc-identity-audit",
        "name":"TinyClaw Identity Audit",
        "process":["Check identity"],
        "tools":["juggler_status","juggler_list_identities","juggler_campaign_status"]
    }`)

    result, err := b.Dispatch(campaign, "run-count-1")
    if err != nil {
        t.Fatalf("dispatch error: %v", err)
    }
    // Should count at least 2 tool references (juggler_status, juggler_list_identities).
    if result.ToolCalls < 2 {
        t.Errorf("expected at least 2 tool calls, got %d", result.ToolCalls)
    }
}

func TestPicoclawBackend_EnrichedPrompt(t *testing.T) {
    var capturedContent string
    agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        body, _ := io.ReadAll(r.Body)
        var req map[string]string
        json.Unmarshal(body, &req)
        capturedContent = req["content"]
        json.NewEncoder(w).Encode(map[string]string{
            "content": "done", "finish_reason": "stop",
        })
    }))
    defer agent.Close()

    b := NewPicoclawBackend(agent.URL, "")
    campaign := json.RawMessage(`{
        "id":"pc-identity-audit",
        "name":"TinyClaw Identity Audit",
        "description":"Verifies bot identity state",
        "process":["Query identity","Report findings"],
        "tools":["juggler_status"],
        "targets":[{"org":"tinyland-inc","repo":"tinyclaw"}],
        "guardrails":{"maxDuration":"15m","readOnly":true},
        "metrics":{"successCriteria":"Identity verified"}
    }`)

    _, err := b.Dispatch(campaign, "run-enrich-1")
    if err != nil {
        t.Fatalf("dispatch error: %v", err)
    }

    if !strings.Contains(capturedContent, "Verifies bot identity state") {
        t.Error("prompt missing description")
    }
    if !strings.Contains(capturedContent, "tinyland-inc/picoclaw") {
        t.Error("prompt missing target")
    }
    if !strings.Contains(capturedContent, "Read-Only") {
        t.Error("prompt missing guardrails")
    }
}
```

Run:
```bash
cd /home/jsullivan2/git/RemoteJuggler/deploy/adapters && go test -v -run TestPicoclaw ./...
```

---

### Day 7: Campaign Prompt Template Standardization

**Objective**: Extract the common prompt template into a shared function used by both ironclaw.go and tinyclaw.go.

#### Step 1: Create shared prompt builder

**File**: `deploy/adapters/prompt.go` (new file)

```go
package main

import (
    "fmt"
    "strings"
)

// CampaignContext holds the fields extracted from a campaign JSON that
// are relevant to building an LLM prompt.
type CampaignContext struct {
    ID              string
    Name            string
    Description     string
    RunID           string
    Process         []string
    Tools           []string
    Targets         []TargetInfo
    MaxDuration     string
    ReadOnly        bool
    SuccessCriteria string
    KPIs            []string
    SetecKey        string
}

// TargetInfo identifies a repo target.
type TargetInfo struct {
    Forge  string
    Org    string
    Repo   string
    Branch string
}

// BuildCampaignPrompt generates a structured LLM prompt from campaign context.
// The toolInstruction parameter controls how the agent should invoke tools:
//   - For IronClaw: 'exec("/workspace/bin/rj-tool <tool> key=value")'
//   - For TinyClaw: tools are available natively via the agent's tool system
func BuildCampaignPrompt(ctx CampaignContext, toolInstruction string) string {
    var b strings.Builder

    b.WriteString(fmt.Sprintf("# Campaign: %s\n", ctx.Name))
    b.WriteString(fmt.Sprintf("**Run ID**: %s\n", ctx.RunID))
    if ctx.Description != "" {
        b.WriteString(fmt.Sprintf("**Purpose**: %s\n", ctx.Description))
    }
    b.WriteString("\n")

    // Targets
    if len(ctx.Targets) > 0 {
        b.WriteString("## Targets\n")
        for _, t := range ctx.Targets {
            branch := t.Branch
            if branch == "" {
                branch = "main"
            }
            if t.Forge != "" {
                b.WriteString(fmt.Sprintf("- %s/%s (branch: %s, forge: %s)\n", t.Org, t.Repo, branch, t.Forge))
            } else {
                b.WriteString(fmt.Sprintf("- %s/%s\n", t.Org, t.Repo))
            }
        }
        b.WriteString("\n")
    }

    // Process
    b.WriteString("## Process\n")
    for i, step := range ctx.Process {
        b.WriteString(fmt.Sprintf("%d. %s\n", i+1, step))
    }
    b.WriteString("\n")

    // Tools
    if len(ctx.Tools) > 0 {
        b.WriteString("## Available Tools\n")
        if toolInstruction != "" {
            b.WriteString(toolInstruction + "\n\n")
        }
        for _, tool := range ctx.Tools {
            b.WriteString(fmt.Sprintf("- `%s`\n", tool))
        }
        b.WriteString("\n")
    }

    // Constraints
    b.WriteString("## Constraints\n")
    if ctx.MaxDuration != "" {
        b.WriteString(fmt.Sprintf("- **Max Duration**: %s\n", ctx.MaxDuration))
    }
    if ctx.ReadOnly {
        b.WriteString("- **Read-Only**: Do NOT create branches, PRs, or modify repositories\n")
    }
    b.WriteString("\n")

    // Success criteria
    if ctx.SuccessCriteria != "" {
        b.WriteString(fmt.Sprintf("## Success Criteria\n%s\n\n", ctx.SuccessCriteria))
    }

    // KPIs
    if len(ctx.KPIs) > 0 {
        b.WriteString("## KPIs to Track\n")
        for _, kpi := range ctx.KPIs {
            b.WriteString(fmt.Sprintf("- %s\n", kpi))
        }
        b.WriteString("\n")
    }

    // Output location
    if ctx.SetecKey != "" {
        b.WriteString(fmt.Sprintf("## Output\nStore results in Setec with key: `%s`\n\n", ctx.SetecKey))
    }

    b.WriteString(findingsInstruction)
    return b.String()
}
```

#### Step 2: Refactor ironclaw.go to use shared prompt builder

Replace the Day 1 inline prompt builder with a call to `BuildCampaignPrompt`.

#### Step 3: Refactor tinyclaw.go to use shared prompt builder

Replace the Day 6 inline prompt builder with a call to `BuildCampaignPrompt`.

#### Step 4: Add prompt builder tests

**File**: `deploy/adapters/prompt_test.go` (new file)

```go
package main

import (
    "strings"
    "testing"
)

func TestBuildCampaignPrompt_Complete(t *testing.T) {
    ctx := CampaignContext{
        ID:          "oc-dep-audit",
        Name:        "Cross-Repo Dependency Audit",
        Description: "Audits dependency manifests across all repos",
        RunID:       "run-123",
        Process:     []string{"Fetch manifests", "Parse dependencies"},
        Tools:       []string{"github_fetch", "juggler_setec_put"},
        Targets: []TargetInfo{
            {Forge: "github", Org: "tinyland-inc", Repo: "remote-juggler", Branch: "main"},
        },
        MaxDuration:     "30m",
        ReadOnly:        true,
        SuccessCriteria: "All repos scanned",
        KPIs:            []string{"repos_scanned", "divergences"},
        SetecKey:        "remotejuggler/campaigns/oc-dep-audit",
    }

    prompt := BuildCampaignPrompt(ctx, "Use exec() to call tools")

    checks := []string{
        "Cross-Repo Dependency Audit",
        "run-123",
        "Audits dependency manifests",
        "tinyland-inc/remote-juggler",
        "Fetch manifests",
        "github_fetch",
        "Use exec() to call tools",
        "30m",
        "Read-Only",
        "All repos scanned",
        "repos_scanned",
        "remotejuggler/campaigns/oc-dep-audit",
        "__findings__", // findings instruction included
    }
    for _, check := range checks {
        if !strings.Contains(prompt, check) {
            t.Errorf("prompt missing: %q", check)
        }
    }
}

func TestBuildCampaignPrompt_Minimal(t *testing.T) {
    ctx := CampaignContext{
        Name:    "Simple Campaign",
        RunID:   "run-1",
        Process: []string{"Do the thing"},
    }

    prompt := BuildCampaignPrompt(ctx, "")

    if !strings.Contains(prompt, "Simple Campaign") {
        t.Error("missing campaign name")
    }
    if !strings.Contains(prompt, "Do the thing") {
        t.Error("missing process step")
    }
    // Should NOT contain sections for empty fields.
    if strings.Contains(prompt, "## Targets") {
        t.Error("should not have Targets section when no targets")
    }
    if strings.Contains(prompt, "## Available Tools") {
        t.Error("should not have Tools section when no tools")
    }
}
```

Run all adapter tests:
```bash
cd /home/jsullivan2/git/RemoteJuggler/deploy/adapters && go test -v ./...
```

---

### Days 8-10: Campaign Activation Sprint

**Objective**: Activate and test the 30 never-run campaigns in batches, fix campaign-specific issues.

#### Campaign Inventory by Agent

Counting from `index.json` (47 enabled + 1 disabled = 48 total):

| Agent | Count | IDs |
|-------|-------|-----|
| ironclaw | 19 | oc-gateway-smoketest, oc-dep-audit, oc-coverage-gaps, oc-docs-freshness, oc-license-scan, oc-dead-code, oc-ts-strict, oc-a11y-check, oc-weekly-digest, oc-issue-triage, oc-prompt-audit, oc-codeql-fix, oc-wiki-update, oc-upstream-sync, oc-self-evolve, oc-fork-review, oc-identity-audit, oc-credential-health, oc-secret-request, oc-token-budget, oc-ts-package-audit, oc-infra-review |
| hexstrike-ai | 7 | hs-cred-exposure, hs-dep-vuln, hs-cve-monitor, hs-network-posture, hs-gateway-pentest, hs-sops-rotation, hs-container-vuln |
| tinyclaw | 5 | pc-upstream-sync, pc-self-evolve, pc-ts-package-scan, pc-credential-health, pc-identity-audit |
| gateway-direct | 5 | cc-mcp-regression, cc-identity-switch, cc-config-sync, cc-cred-resolution, cc-gateway-health |
| cross-agent | 8 | xa-audit-completeness, xa-cred-lifecycle, xa-acl-enforcement, xa-fork-health, xa-identity-audit, xa-platform-health, xa-token-budget, xa-upstream-drift |

#### Day 8: Low-risk batch (identity, health, gateway campaigns)

Trigger 10 campaigns that are simple, read-only, and have short timeouts:

```bash
# Batch 1: Identity and health checks
for campaign in oc-identity-audit pc-identity-audit xa-identity-audit oc-credential-health pc-credential-health; do
  echo "Triggering $campaign..."
  kubectl exec -n fuzzy-dev deploy/rj-gateway -- \
    wget -qO- --post-data='' "http://localhost:8081/trigger?campaign=$campaign" 2>/dev/null
  sleep 30  # Space out dispatches to avoid overloading agents
done

# Wait for completion (~15min max per campaign)
sleep 300

# Check results
kubectl exec -n fuzzy-dev deploy/rj-gateway -- \
  wget -qO- 'http://localhost:8081/campaigns' 2>/dev/null | \
  python3 -c "
import sys, json
data = json.load(sys.stdin)
for cid, info in sorted(data.items()):
    lr = info.get('lastResult', {}) or {}
    print(f'{cid}: status={lr.get(\"status\",\"never_run\")}, tools={lr.get(\"tool_calls\",0)}')
"
```

Then trigger the gateway-direct campaigns (these don't use LLM agents):
```bash
for campaign in cc-gateway-health cc-config-sync cc-cred-resolution cc-identity-switch cc-mcp-regression; do
  echo "Triggering $campaign..."
  kubectl exec -n fuzzy-dev deploy/rj-gateway -- \
    wget -qO- --post-data='' "http://localhost:8081/trigger?campaign=$campaign" 2>/dev/null
  sleep 10
done
```

#### Day 9: Medium-risk batch (analysis campaigns)

```bash
# Batch 2: Code analysis campaigns
for campaign in oc-dep-audit oc-license-scan oc-dead-code oc-docs-freshness oc-coverage-gaps oc-ts-strict; do
  echo "Triggering $campaign..."
  kubectl exec -n fuzzy-dev deploy/rj-gateway -- \
    wget -qO- --post-data='' "http://localhost:8081/trigger?campaign=$campaign" 2>/dev/null
  sleep 60  # These take longer
done

# Batch 3: HexStrike security campaigns
for campaign in hs-cred-exposure hs-dep-vuln hs-container-vuln; do
  echo "Triggering $campaign..."
  kubectl exec -n fuzzy-dev deploy/rj-gateway -- \
    wget -qO- --post-data='' "http://localhost:8081/trigger?campaign=$campaign" 2>/dev/null
  sleep 120  # Security scans are heavier
done

# Cross-agent campaigns
for campaign in xa-platform-health xa-token-budget xa-audit-completeness xa-fork-health; do
  echo "Triggering $campaign..."
  kubectl exec -n fuzzy-dev deploy/rj-gateway -- \
    wget -qO- --post-data='' "http://localhost:8081/trigger?campaign=$campaign" 2>/dev/null
  sleep 60
done
```

#### Day 10: Remaining campaigns and issue triage

```bash
# Batch 4: Remaining campaigns
for campaign in oc-gateway-smoketest oc-a11y-check oc-prompt-audit oc-issue-triage oc-weekly-digest oc-ts-package-audit oc-infra-review oc-token-budget; do
  echo "Triggering $campaign..."
  kubectl exec -n fuzzy-dev deploy/rj-gateway -- \
    wget -qO- --post-data='' "http://localhost:8081/trigger?campaign=$campaign" 2>/dev/null
  sleep 60
done

# Campaigns requiring write access (be careful)
for campaign in oc-codeql-fix oc-wiki-update oc-self-evolve oc-upstream-sync oc-fork-review pc-upstream-sync pc-self-evolve; do
  echo "Triggering $campaign..."
  kubectl exec -n fuzzy-dev deploy/rj-gateway -- \
    wget -qO- --post-data='' "http://localhost:8081/trigger?campaign=$campaign" 2>/dev/null
  sleep 120
done

# HexStrike remaining
for campaign in hs-cve-monitor hs-network-posture hs-gateway-pentest hs-sops-rotation; do
  echo "Triggering $campaign..."
  kubectl exec -n fuzzy-dev deploy/rj-gateway -- \
    wget -qO- --post-data='' "http://localhost:8081/trigger?campaign=$campaign" 2>/dev/null
  sleep 120
done

# Cross-agent remaining
for campaign in xa-cred-lifecycle xa-acl-enforcement xa-upstream-drift; do
  echo "Triggering $campaign..."
  kubectl exec -n fuzzy-dev deploy/rj-gateway -- \
    wget -qO- --post-data='' "http://localhost:8081/trigger?campaign=$campaign" 2>/dev/null
  sleep 60
done
```

#### Fix Campaign-Specific Issues

Common problems expected and fixes:

| Issue | Campaign(s) | Fix |
|-------|-------------|-----|
| Timeout too short | oc-dep-audit (10 repos, 30m) | Increase maxDuration to 45m |
| Wrong tool name | hs-cve-monitor uses `cve_monitor` (may be `cve_scan` in OCaml) | Update campaign JSON after tools/list query |
| Missing Setec key permissions | oc-secret-request | Verify Setec ACL allows write from campaign runner |
| dependent campaign not triggered | hs-dep-vuln (depends on oc-dep-audit) | Trigger oc-dep-audit first in same scheduler cycle |
| Tool not in HexStrike policy | hs-network-posture, hs-cve-monitor | Update Dhall policy in hexstrike-ai fork to add grants |
| Self-evolve campaigns need write access | oc-self-evolve, pc-self-evolve | Verify readOnly=false and allowedBranches are correct |

#### Tracking Script

Save campaign results to a local file for tracking progress:

```bash
#!/bin/bash
# save as: /home/jsullivan2/git/RemoteJuggler/agent_plane/check_progress.sh
echo "=== Campaign Activation Progress ($(date -Iseconds)) ==="
kubectl exec -n fuzzy-dev deploy/rj-gateway -- \
  wget -qO- 'http://localhost:8081/campaigns' 2>/dev/null | \
  python3 -c "
import sys, json
data = json.load(sys.stdin)
total = len(data)
run = sum(1 for v in data.values() if v.get('lastResult'))
tools_gt0 = sum(1 for v in data.values() if (v.get('lastResult') or {}).get('tool_calls', 0) > 0)
findings = sum(1 for v in data.values() if len((v.get('lastResult') or {}).get('findings', [])) > 0)
print(f'Total: {total}')
print(f'Run at least once: {run}/{total}')
print(f'With tool_calls > 0: {tools_gt0}/{total}')
print(f'With findings: {findings}/{total}')
print()
for cid, info in sorted(data.items()):
    lr = info.get('lastResult') or {}
    status = lr.get('status', 'never_run')
    tc = lr.get('tool_calls', 0)
    nf = len(lr.get('findings', []))
    marker = '  ' if status == 'never_run' else ('OK' if status == 'success' else 'XX')
    print(f'  [{marker}] {cid}: status={status}, tools={tc}, findings={nf}')
"
```

---

## Completion Metrics

| Metric | Target | How to Verify |
|--------|--------|--------------|
| IronClaw tool_calls > 0 | At least 5 campaigns | `check_progress.sh` -- filter for oc-* campaigns with tool_calls > 0 |
| HexStrike native tools return real data | credential_scan, vuln_scan, container_scan return non-error results | ToolTrace summaries longer than 50 chars, no "target is required" |
| TinyClaw tool_calls accurately tracked | At least 3 campaigns report tool_calls > 0 | pc-identity-audit, pc-credential-health, pc-ts-package-scan |
| 35+ campaigns run at least once | 35 of 47 enabled campaigns | `check_progress.sh` "Run at least once" line |
| 10+ campaigns with real findings | 10 campaigns have non-empty Findings arrays | `check_progress.sh` "With findings" line |

---

## Risk Register

### R1: OpenClaw exec() tool is disabled or restricted

**Probability**: Medium
**Impact**: High -- entire IronClaw tool strategy depends on exec()
**Detection**: Day 3 E2E -- if oc-identity-audit returns 0 tool calls, exec is not working
**Mitigation**: Check OpenClaw config (`openclaw.json` line 82-98). The `tools.deny` list includes `message`, `canvas`, `nodes` but NOT `exec`. If exec is blocked, switch to the multi-turn function calling approach (major refactor, would push IronClaw fix to Week 3).
**Verify**:
```bash
kubectl exec -n fuzzy-dev deploy/ironclaw -c ironclaw -- \
  cat /app/tinyland/openclaw.json | python3 -c "import sys,json; d=json.load(sys.stdin); print('deny:', d.get('tools',{}).get('deny',[])); print('profile:', d.get('tools',{}).get('profile',''))"
```

### R2: HexStrike tool schemas differ from assumptions

**Probability**: High
**Impact**: Medium -- Day 4 code needs revision
**Detection**: Day 5 Step 1 `tools/list` query
**Mitigation**: The Day 4 plan explicitly includes querying actual schemas first. The `hexstrikeToolArgs` registry is designed to be updated after discovery. Budget 2 hours for schema alignment.

### R3: Agent pods crash or OOM during activation sprint

**Probability**: Medium (IronClaw has 2Gi limit, security scans are heavy)
**Impact**: Medium -- delays individual campaign verification
**Detection**: `kubectl get pods -n fuzzy-dev` shows CrashLoopBackOff
**Mitigation**:
1. Space campaign triggers 60-120s apart (already in the plan)
2. Monitor memory during scans: `kubectl top pod -n fuzzy-dev`
3. If OOM: increase memory limit in Terraform, or reduce campaign concurrency
4. Fallback: skip heavy campaigns (hs-dep-vuln, hs-cred-exposure) and revisit in Week 3

### R4: Campaign runner polling timeout (Dispatcher 2-minute httpClient)

**Probability**: Medium
**Impact**: Low -- campaigns still run, but dispatcher reports timeout
**Detection**: Campaign results show `status: "error"`, `error: "context expired"`
**Mitigation**: The dispatcher's `httpClient.Timeout` is 2 minutes (line 42 of `dispatcher.go`), but `pollAgentStatus` uses context timeouts from `parseDuration(campaign.Guardrails.MaxDuration)`. The polling should be fine for campaigns with long maxDuration. If the 2-min httpClient timeout hits the initial POST, increase it:
```go
httpClient: &http.Client{
    Timeout: 5 * time.Minute,
},
```

### R5: Aperture token budget exhaustion

**Probability**: Low (most campaigns have aiApiBudget caps)
**Impact**: High -- all LLM calls blocked until budget resets
**Detection**: Agent responses contain "rate limited" or "budget exceeded"
**Mitigation**:
1. Monitor usage after each batch: `kubectl exec -n fuzzy-dev deploy/rj-gateway -- wget -qO- --post-data='{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"juggler_aperture_usage","arguments":{}}}' -H 'Content-Type: application/json' 'http://localhost:8080/mcp'`
2. If approaching limits, pause activation sprint and wait for reset
3. Use claude-haiku for low-priority campaigns (identity audits, health checks) to conserve budget

### R6: HexStrike policy denies tools needed by campaigns

**Probability**: High (memory notes say `network_posture`, `api_fuzz`, `sops_rotation_check`, `cve_monitor` are NOT in any grant)
**Impact**: Medium -- 4 campaigns blocked
**Detection**: ToolTrace shows "policy denied" errors for hs-network-posture, hs-sops-rotation, hs-cve-monitor, hs-gateway-pentest
**Mitigation**: Update the Dhall policy in `tinyland-inc/hexstrike-ai` to add these tools to a grant. This is a PR to the hexstrike-ai repo:
```bash
# In hexstrike-ai fork:
# deploy/policies/grants.dhall -- add Grant 2 with missing tools
# Push to main, wait for GHCR build, restart hexstrike deployment
```

### R7: rj-tool wrapper fails on argument values with spaces or special characters

**Probability**: Medium
**Impact**: Low -- affects specific campaigns with complex arguments
**Detection**: Tool calls return "parse error" or empty results
**Mitigation**: The rj-tool wrapper (line 47-71) handles JSON-escaping of quotes and backslashes but not all special characters. For campaigns that pass complex values (e.g., issue body with newlines), consider extending the wrapper to accept a JSON file instead of key=value pairs:
```bash
rj-tool --json <tool_name> '{"key":"value with spaces"}'
```
This would be a minor enhancement to `/workspace/bin/rj-tool`.

---

## Files Modified Summary

| File | Changes | Day |
|------|---------|-----|
| `deploy/adapters/ironclaw.go` | Expand campaign struct, build enriched prompt, parse exec() tool calls | 1-2 |
| `deploy/adapters/ironclaw_test.go` | Add enriched prompt test, exec tool pattern test | 1-2 |
| `deploy/adapters/hexstrike.go` | Tool schema registry, fix target parameter, per-target dispatch | 4 |
| `deploy/adapters/hexstrike_test.go` | Add correct parameter test | 4 |
| `deploy/adapters/tinyclaw.go` | Tool call counting, enriched prompt, expanded struct | 6 |
| `deploy/adapters/tinyclaw_test.go` | Add tool counting test, enriched prompt test | 6 |
| `deploy/adapters/prompt.go` | New shared prompt builder | 7 |
| `deploy/adapters/prompt_test.go` | New prompt builder tests | 7 |
| `agent_plane/check_progress.sh` | New activation tracking script | 8 |
