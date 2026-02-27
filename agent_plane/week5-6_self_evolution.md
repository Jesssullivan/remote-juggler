# Week 5-6: Self-Evolution & Production Hardening

**Epic**: Agent Plane Autonomy (6-week)
**Sprint dates**: Week 5-6 of 6
**Predecessor**: Weeks 1-4 (scaffolding, adapter plumbing, campaign definitions, Aperture integration)

---

## Inflection Point

The inflection point is the first moment an agent autonomously reads the results of its
own previous campaign run from Setec, identifies a concrete deficiency in its campaign
definition (e.g., the timeout is too short, a process step is vague, a tool is missing),
creates a branch with a modified campaign JSON, and opens a PR for human review.

This is observable and verifiable: a GitHub PR authored by `rj-agent-bot[bot]` that modifies
a file under `test/campaigns/`, with a body that cites specific data from a prior Setec result.
Until that PR exists, the system is not self-evolving -- it is executing scripts.

---

## Current State (Start of Week 5)

### What Works
- 48 campaign definitions loaded (47 enabled, 1 disabled: `xa-provision-agent`)
- Campaign runner dispatches to 3 agents (IronClaw, TinyClaw, HexStrike-AI) + gateway-direct
- Adapter sidecars translate campaign protocol to native agent APIs
- Collector stores results in Setec at `{setecKey}/latest` and `{setecKey}/runs/{runID}`
- Publisher creates GitHub Discussions for non-health-check results
- FeedbackHandler creates/closes GitHub issues based on findings
- Aperture egress service routes LLM calls; SSE + webhook metering partially operational
- GitHub App (`rj-agent-bot`, App ID 2945224) has R/W deploy keys on all 3 agent repos

### What is Broken or Dormant
1. **Memory files are templates**: `deploy/fork-dockerfiles/ironclaw/workspace/memory/MEMORY.md` has zero real observations. "Observations" and "Known Issues" sections are placeholder text.
2. **Self-evolve timeouts are fatal**: `oc-self-evolve.json` has `maxDuration: "5m"`, `pc-self-evolve.json` has `maxDuration: "3m"`. IronClaw alone takes ~90s to start. One LLM round-trip with tool calls to Setec + GitHub takes 30-60s. These campaigns cannot complete a meaningful cycle.
3. **No previous-run context**: The adapter's `Dispatch()` method (in `deploy/adapters/ironclaw.go:80`) builds the prompt purely from `campaign.Process` steps. It never fetches the previous result from Setec. Each dispatch is amnesiac.
4. **Feedback loops disabled on self-evolve**: Both `oc-self-evolve` and `pc-self-evolve` have `createIssues: false`, `createPRs: false`, `closeResolvedIssues: false`. The agent cannot act on what it learns.
5. **S3 metering has credential gaps**: `aperture_s3.go` requires `aperture_s3_access_key` and `aperture_s3_secret_key` in gateway config. `apply.sh` must export `TF_VAR_aperture_s3_access_key`/`TF_VAR_aperture_s3_secret_key` (reuses `AWS_ACCESS_KEY_ID`). If those env vars are unset, `ApertureS3Ingester.Configured()` returns true (bucket and region are set) but S3 requests get 403.
6. **`xa-provision-agent` has conflicting guardrails**: `readOnly: true` but the process steps require `github_update_file`, `github_create_pr`, etc. It cannot both be read-only and create repos/files.
7. **No HexStrike self-evolve**: HexStrike-AI has 7 campaigns but no `hs-self-evolve`. The formal verification stack (F*/Dhall/Futhark) is an island with no evolutionary loop.
8. **Token budget is declared but not enforced**: `aiApiBudget.maxTokens` exists in campaign JSON but `scheduler.go:RunCampaign()` never checks it against actual usage. The budget is cosmetic.
9. **Dual runner deduplication risk**: If two campaign-runner replicas exist (scaling, rolling update), both evaluate `isDue()` independently, potentially dispatching the same campaign twice.

---

## Day-by-Day Plan

### Day 1: Previous-Run Context Injection

**Goal**: Every agent dispatch includes a summary of what happened last time.

#### 1a. Add `FetchPreviousResult` to Collector

**File**: `/home/jsullivan2/git/RemoteJuggler/test/campaigns/runner/collector.go`

Add a new method that fetches the latest result from Setec:

```go
// FetchPreviousResult retrieves the most recent result for a campaign from Setec.
// Returns nil if no previous result exists (first run).
func (c *Collector) FetchPreviousResult(ctx context.Context, campaign *Campaign) (*CampaignResult, error) {
    key := campaign.Outputs.SetecKey + "/latest"
    resp, err := c.dispatcher.callTool(ctx, "juggler_setec_get", map[string]any{
        "name": key,
    })
    if err != nil {
        return nil, nil // No previous result is not an error
    }

    // Parse MCP response wrapper
    var mcp struct {
        Content []struct {
            Text string `json:"text"`
        } `json:"content"`
    }
    if err := json.Unmarshal(resp, &mcp); err != nil || len(mcp.Content) == 0 {
        return nil, nil
    }

    var result CampaignResult
    if err := json.Unmarshal([]byte(mcp.Content[0].Text), &result); err != nil {
        return nil, nil
    }
    return &result, nil
}
```

#### 1b. Thread Previous Result Through Scheduler to Dispatcher

**File**: `/home/jsullivan2/git/RemoteJuggler/test/campaigns/runner/scheduler.go`

In `RunCampaign()`, after the kill switch check and before dispatch, fetch previous result:

```go
// Fetch previous run result for context injection.
var prevResult *CampaignResult
if s.collector != nil {
    prevResult, _ = s.collector.FetchPreviousResult(ctx, campaign)
}

// Pass to dispatcher
dispatchResult, err := s.dispatcher.Dispatch(ctx, campaign, runID, prevResult)
```

Update `Dispatcher.Dispatch()` and `dispatchToAgent()` signatures to accept `*CampaignResult`.

#### 1c. Inject Previous-Run Summary into Adapter Prompts

**File**: `/home/jsullivan2/git/RemoteJuggler/deploy/adapters/adapter.go`

Extend `CampaignRequest` to carry previous result context:

```go
type CampaignRequest struct {
    Campaign       json.RawMessage `json:"campaign"`
    RunID          string          `json:"run_id"`
    PreviousResult *PreviousRun    `json:"previous_result,omitempty"`
}

type PreviousRun struct {
    Status       string `json:"status"`
    FindingCount int    `json:"finding_count"`
    ToolCalls    int    `json:"tool_calls"`
    RunID        string `json:"run_id"`
    FinishedAt   string `json:"finished_at"`
    Error        string `json:"error,omitempty"`
}
```

**File**: `/home/jsullivan2/git/RemoteJuggler/deploy/adapters/ironclaw.go`

In `IronclawBackend.Dispatch()`, prepend context to the prompt:

```go
if prevRun != nil {
    prompt += fmt.Sprintf("\n## Previous Run Context\n")
    prompt += fmt.Sprintf("- Status: %s\n", prevRun.Status)
    prompt += fmt.Sprintf("- Findings: %d\n", prevRun.FindingCount)
    prompt += fmt.Sprintf("- Tool calls: %d\n", prevRun.ToolCalls)
    prompt += fmt.Sprintf("- Finished: %s\n", prevRun.FinishedAt)
    if prevRun.Error != "" {
        prompt += fmt.Sprintf("- Error: %s\n", prevRun.Error)
    }
    prompt += "\nUse this context to avoid repeating the same analysis. Focus on what changed.\n\n"
}
```

Apply the same pattern to `PicoclawBackend.Dispatch()` and `HexstrikeBackend.Dispatch()`.

#### 1d. Add Run Counter

**File**: `/home/jsullivan2/git/RemoteJuggler/test/campaigns/runner/scheduler.go`

Add `runCounts map[string]int` to `Scheduler`. Increment per campaign dispatch. Include in context:

```
This is run #N of campaign {id}. Last run was {duration} ago.
```

#### Verification
- Unit test: `TestFetchPreviousResult` in `collector_test.go` with mock Setec response
- Unit test: `TestDispatchWithPreviousContext` in `ironclaw_test.go` verifying prompt contains previous-run section
- Integration: manually trigger `oc-gateway-smoketest`, check that the second run's prompt includes the first run's result summary

---

### Day 2: Memory System Activation

**Goal**: Self-evolve campaigns complete successfully and write real observations to MEMORY.md.

#### 2a. Fix Self-Evolve Timeouts

**File**: `/home/jsullivan2/git/RemoteJuggler/test/campaigns/openclaw/oc-self-evolve.json`

```diff
-    "maxDuration": "5m",
+    "maxDuration": "25m",
     ...
-      "maxTokens": 80000
+      "maxTokens": 200000
```

**File**: `/home/jsullivan2/git/RemoteJuggler/test/campaigns/picoclaw/pc-self-evolve.json`

```diff
-    "maxDuration": "3m",
+    "maxDuration": "20m",
     ...
-      "maxTokens": 50000
+      "maxTokens": 150000
```

**Rationale**: IronClaw startup is ~90s. A self-evolve cycle requires:
1. Fetch MEMORY.md from workspace (~10s tool call)
2. Query `juggler_campaign_status()` (~5s)
3. Query `juggler_audit_log()` (~5s)
4. Fetch previous results from Setec (~5s per campaign)
5. LLM reasoning on observations (~30-60s)
6. Write updated MEMORY.md via `github_update_file()` (~10s)
7. Store summary in Setec (~5s)

Minimum viable time: ~3-5 minutes of *active work* after the 90s startup. 5m total is impossible. 25m provides margin for retries and multi-step reasoning.

#### 2b. Create HexStrike Self-Evolve Campaign

**New file**: `/home/jsullivan2/git/RemoteJuggler/test/campaigns/hexstrike/hs-self-evolve.json`

```json
{
  "id": "hs-self-evolve",
  "name": "HexStrike Self-Evolution",
  "description": "Review own campaign results, update workspace, improve security scan accuracy. Maintains HexStrike-AI's persistent memory, tracks false positives, and evolves Dhall policies.",
  "agent": "hexstrike-ai",
  "trigger": {
    "schedule": "0 4 * * *",
    "event": "manual"
  },
  "targets": [
    {
      "forge": "github",
      "org": "tinyland-inc",
      "repo": "hexstrike-ai",
      "branch": "main"
    }
  ],
  "tools": [
    "github_fetch",
    "juggler_campaign_status",
    "juggler_setec_get",
    "juggler_setec_put",
    "juggler_audit_log"
  ],
  "process": [
    "Query juggler_campaign_status() for recent HexStrike campaign runs (hs-*)",
    "Fetch previous self-evolve result from Setec via juggler_setec_get(name='campaigns/hs-self-evolve/latest')",
    "Review each hs-* campaign's latest Setec result for recurring patterns: false positives, missed vulnerabilities, timeouts",
    "Query juggler_audit_log() for recent HexStrike tool call errors or policy denials",
    "Identify Dhall policy gaps: tools that are blocked but should be allowed, or vice versa",
    "Summarize learnings and update observations",
    "Store evolution summary in Setec via juggler_setec_put",
    "Report findings for any actionable improvements to campaigns or policies"
  ],
  "outputs": {
    "setecKey": "remotejuggler/campaigns/hs-self-evolve",
    "issueLabels": ["campaign", "self-evolution", "hexstrike"],
    "issueRepo": "tinyland-inc/remote-juggler"
  },
  "guardrails": {
    "maxDuration": "20m",
    "readOnly": false,
    "aiApiBudget": {
      "maxTokens": 150000
    }
  },
  "feedback": {
    "createIssues": false,
    "createPRs": false,
    "closeResolvedIssues": false
  },
  "metrics": {
    "successCriteria": "Campaign results reviewed, patterns consolidated, policy gaps identified",
    "kpis": [
      "patterns_identified",
      "policy_gaps_found",
      "false_positives_identified"
    ]
  }
}
```

Register in index.json:
```json
"hs-self-evolve": {
  "file": "hexstrike/hs-self-evolve.json",
  "enabled": true,
  "lastRun": null,
  "lastResult": null
}
```

#### 2c. Design Memory Write Protocol

Memory lives in two tiers:

| Tier | Storage | What goes here | Retention |
|------|---------|----------------|-----------|
| **Hot memory** | Setec (`campaigns/{id}/latest`) | Last run result, findings, KPIs | Overwritten each run |
| **Warm memory** | Agent repo MEMORY.md (via `github_update_file`) | Consolidated patterns, known false positives, architectural observations | Persistent, agent-curated |
| **Cold memory** | Setec (`campaigns/{id}/runs/{runID}`) | Historical run results | Indefinite, timestamped keys |

Self-evolve campaigns are the *only* campaigns that write to warm memory. All other campaigns write to hot/cold memory via the Collector. Self-evolve reads hot memory from multiple campaigns, distills patterns, and writes to warm memory.

#### 2d. Enable Self-Evolve Feedback Hooks

**File**: `/home/jsullivan2/git/RemoteJuggler/test/campaigns/openclaw/oc-self-evolve.json`

```diff
   "feedback": {
-    "createIssues": false,
-    "createPRs": false,
+    "createIssues": true,
+    "createPRs": false,
     "closeResolvedIssues": false
   }
```

Start conservative: issues only (no PRs). The agent can report "MEMORY.md needs section X" as a finding/issue. PR creation comes Day 3.

#### Verification
- Manually trigger `oc-self-evolve` via `POST /trigger?campaign=oc-self-evolve`
- Verify it runs >5 minutes without timeout
- Verify Setec key `remotejuggler/campaigns/oc-self-evolve/latest` contains a result with `status: "success"` and `tool_calls > 0`
- Verify no "context expired" errors in campaign runner logs

---

### Day 3: Campaign Self-Modification (The Inflection Point)

**Goal**: An agent reads its own campaign definition, proposes improvements, and opens a PR.

#### 3a. Add `github_fetch` for Campaign JSON to Self-Evolve Process

**File**: `/home/jsullivan2/git/RemoteJuggler/test/campaigns/openclaw/oc-self-evolve.json`

Add to `process` array:

```json
"Fetch your own campaign definitions from tinyland-inc/remote-juggler via github_fetch (path: test/campaigns/openclaw/) to identify improvements",
"If a campaign has suboptimal timeouts, unclear process steps, missing tools, or should target additional repos, propose changes",
"For non-trivial improvements, create a branch (sid/oc-self-evolve-{date}) and PR via github_create_branch + github_update_file + github_create_pr"
```

Add `github_create_branch`, `github_update_file`, `github_create_pr` to `tools` array.

**File**: `/home/jsullivan2/git/RemoteJuggler/test/campaigns/openclaw/oc-self-evolve.json`

```diff
   "feedback": {
     "createIssues": true,
-    "createPRs": false,
+    "createPRs": true,
     "closeResolvedIssues": false
   }
```

Update guardrails to allow specific branch patterns:

```diff
   "guardrails": {
     "maxDuration": "25m",
     "readOnly": false,
+    "allowedBranches": ["sid/oc-self-evolve-*"],
+    "requireApproval": true,
     "aiApiBudget": {
       "maxTokens": 200000
     }
   }
```

#### 3b. Implement Branch Pattern Enforcement in Adapter

**File**: `/home/jsullivan2/git/RemoteJuggler/deploy/adapters/adapter.go`

Before dispatching to the agent backend, extract guardrails from the campaign JSON and pass `allowedBranches` as a constraint in the prompt:

```go
if len(guardrails.AllowedBranches) > 0 {
    prompt += fmt.Sprintf("\n## Branch Constraints\n")
    prompt += fmt.Sprintf("You may ONLY create branches matching these patterns: %v\n", guardrails.AllowedBranches)
    prompt += "Any PR must target the repository's default branch and require human approval.\n"
}
```

This is prompt-level enforcement (the agent can technically ignore it). Hard enforcement requires intercepting `github_create_branch` tool calls at the gateway level -- tracked as a future hardening task.

#### 3c. Diff Size Limits

Add to the self-evolve prompt (in the adapter's campaign context injection):

```
## Self-Modification Guardrails
- Maximum diff size: 500 lines
- Only modify files under test/campaigns/ and agent workspace files
- Never modify adapter code, gateway code, or Terraform
- Never merge your own PRs -- all PRs require human review
- Include a rationale citing specific Setec data (run ID, finding count, error message)
```

#### 3d. PR Template for Self-Modification

Add to the self-evolve process steps:

```json
"When creating a PR, use this body format: '## Campaign Self-Modification\n\n**Agent**: {agent}\n**Trigger**: Self-evolve run {runID}\n**Evidence**: Setec key {setecKey} shows {summary}\n\n### Changes\n{description}\n\n### Rationale\n{why this improves the campaign}'"
```

#### Verification
- The **inflection point test**: Trigger `oc-self-evolve` manually. If the previous run had a timeout or tool failure, the agent should create a PR fixing the campaign JSON.
- Check `gh pr list --repo tinyland-inc/remote-juggler --label self-evolution`
- Verify PR is authored by `rj-agent-bot[bot]`
- Verify PR modifies only files under `test/campaigns/`
- Verify PR body cites a specific Setec key and run ID

---

### Day 4: Aperture Metering Fix & Token Budget Enforcement

**Goal**: Accurate per-agent token accounting with hard budget enforcement.

#### 4a. Fix S3 Credential Propagation

**File**: `/home/jsullivan2/git/RemoteJuggler/deploy/tofu/apply.sh`

Ensure S3 credentials are exported:

```bash
# Aperture S3 metering credentials (reuse AWS credentials)
export TF_VAR_aperture_s3_access_key="${AWS_ACCESS_KEY_ID}"
export TF_VAR_aperture_s3_secret_key="${AWS_SECRET_ACCESS_KEY}"
```

**Verification**:
```bash
# After apply, check gateway logs for S3 polling
kubectl logs deployment/rj-gateway -n fuzzy-dev | grep "aperture-s3"
# Should see: "aperture-s3: polling s3://..." NOT "aperture-s3: list returned 403"
```

#### 4b. Verify Webhook Metering Path

**File**: `/home/jsullivan2/git/RemoteJuggler/gateway/aperture_webhook.go`

Audit the webhook handler to verify it:
1. Extracts `agent` and `campaign_id` from the Aperture webhook payload
2. Passes them through to `MeterStore.Record()` with a `DedupeKey` set to the Aperture `capture_id`
3. The SSE path and webhook path both use the same composite dedup key, so double-counting is prevented

Run the existing 9 aperture tests + 13 webhook tests to confirm:
```bash
cd /home/jsullivan2/git/RemoteJuggler/gateway && go test -run "TestAperture|TestWebhook" -v ./...
```

#### 4c. Implement Token Budget Enforcement

**File**: `/home/jsullivan2/git/RemoteJuggler/test/campaigns/runner/scheduler.go`

In `RunCampaign()`, after dispatch completes, check actual token usage against budget:

```go
// Check token budget compliance (post-hoc, not preventive).
if campaign.Guardrails.AIApiBudget != nil && campaign.Guardrails.AIApiBudget.MaxTokens > 0 {
    // Query Aperture for actual usage during this run.
    usage, err := s.queryRunUsage(ctx, campaign.ID, runID)
    if err == nil && usage > campaign.Guardrails.AIApiBudget.MaxTokens {
        log.Printf("campaign %s: BUDGET EXCEEDED (%d tokens used, %d max)",
            campaign.ID, usage, campaign.Guardrails.AIApiBudget.MaxTokens)
        result.KPIs["budget_exceeded"] = true
        result.KPIs["actual_tokens"] = usage
        result.KPIs["max_tokens"] = campaign.Guardrails.AIApiBudget.MaxTokens
    }
}
```

Budget enforcement is **post-hoc** (check after run) for now. Pre-emptive enforcement (killing an agent mid-run) requires deeper Aperture integration (budget headers in proxy requests) -- tracked for a future sprint.

#### 4d. Enhanced `juggler_aperture_usage` Output

**File**: `/home/jsullivan2/git/RemoteJuggler/gateway/tools.go` (the `handleApertureUsageTool` function)

Extend the response to include per-agent breakdown:

```json
{
  "total": {
    "input_tokens": 450000,
    "output_tokens": 120000,
    "tool_calls": 340,
    "period": "since_last_flush"
  },
  "by_agent": {
    "ironclaw": { "input_tokens": 200000, "output_tokens": 60000, "tool_calls": 150 },
    "picoclaw": { "input_tokens": 100000, "output_tokens": 30000, "tool_calls": 90 },
    "hexstrike-ai": { "input_tokens": 150000, "output_tokens": 30000, "tool_calls": 100 }
  },
  "by_campaign": {
    "oc-self-evolve": { "input_tokens": 50000, "output_tokens": 15000 },
    ...
  }
}
```

This data already exists in `MeterStore.Query()` -- it just needs to be formatted into the response.

#### Verification
- `juggler_aperture_usage` returns non-empty `by_agent` section
- Trigger a campaign, then query: `juggler_aperture_usage(campaign_id="oc-gateway-smoketest")` returns token counts >0
- Deliberately set a low budget (`maxTokens: 100`) on a test campaign, run it, confirm `budget_exceeded: true` in Setec result

---

### Day 5: Cross-Agent Knowledge Sharing

**Goal**: Agents can read each other's memory and share findings via a common index.

#### 5a. Cross-Agent Memory Reading

Each agent's MEMORY.md is in its own repo:
- IronClaw: `tinyland-inc/ironclaw` at `workspace/memory/MEMORY.md`
- TinyClaw: `tinyland-inc/picoclaw` at `workspace/memory/MEMORY.md`
- HexStrike: `tinyland-inc/hexstrike-ai` at `workspace/memory/MEMORY.md`

Agents already have `github_fetch` in their tool list. Add a cross-agent memory reading step to each self-evolve campaign:

```json
"Fetch other agents' MEMORY.md via github_fetch to identify cross-cutting patterns",
"Check tinyland-inc/ironclaw workspace/memory/MEMORY.md for IronClaw's observations",
"Check tinyland-inc/picoclaw workspace/memory/MEMORY.md for TinyClaw's observations",
"Check tinyland-inc/hexstrike-ai workspace/memory/MEMORY.md for HexStrike's observations",
"If another agent found something relevant to your domain, note it in your MEMORY.md"
```

#### 5b. Shared Findings Index in Setec

Create a Setec key `remotejuggler/findings/index` that maps fingerprints to resolution status:

```json
{
  "findings": {
    "fp-cred-exposure-gateway-env": {
      "first_seen": "2026-02-28T02:00:00Z",
      "last_seen": "2026-03-01T02:00:00Z",
      "severity": "high",
      "agent": "hexstrike-ai",
      "campaign": "hs-cred-exposure",
      "status": "open",
      "resolution": null
    },
    "fp-dead-code-tools-chpl-l342": {
      "first_seen": "2026-03-01T04:00:00Z",
      "severity": "low",
      "agent": "ironclaw",
      "campaign": "oc-dead-code",
      "status": "resolved",
      "resolution": "Removed in PR #125"
    }
  }
}
```

**File**: `/home/jsullivan2/git/RemoteJuggler/test/campaigns/runner/collector.go`

Add `UpdateFindingsIndex()` called from `storeResult()` when findings are present:

```go
func (c *Collector) UpdateFindingsIndex(ctx context.Context, campaign *Campaign, findings []Finding) error {
    // Fetch current index
    // Merge new findings (by fingerprint, dedup)
    // Store updated index
    // Return count of new findings added
}
```

#### 5c. Resolution Memory

Add a process step to self-evolve campaigns:

```json
"Check Setec findings index (remotejuggler/findings/index) for resolved findings that match your domain",
"If a finding was resolved, update your MEMORY.md with the resolution pattern: 'When {problem}, the fix is {resolution}'"
```

This creates a feedback loop where one agent's resolution becomes another agent's knowledge.

#### Verification
- Trigger all three self-evolve campaigns in sequence
- Check each agent's MEMORY.md in their repo for cross-references to other agents
- Query Setec `remotejuggler/findings/index` and verify it contains entries from multiple agents
- Verify no agent overwrites another agent's entries in the index (fingerprint-based merge)

---

### Day 6: Formal Verification Expansion

**Goal**: Move Dhall/F* verification from HexStrike-only to system-wide campaign validation.

#### 6a. Campaign Schema Validation at Dispatch Time

**File**: `/home/jsullivan2/git/RemoteJuggler/test/campaigns/runner/scheduler.go`

Add pre-dispatch validation in `RunCampaign()`:

```go
func validateCampaign(campaign *Campaign) error {
    if campaign.ID == "" {
        return fmt.Errorf("campaign ID is empty")
    }
    if campaign.Agent == "" {
        return fmt.Errorf("campaign agent is empty")
    }
    if campaign.Guardrails.MaxDuration == "" {
        return fmt.Errorf("campaign maxDuration is empty")
    }
    d := parseDuration(campaign.Guardrails.MaxDuration)
    if d < 1*time.Minute {
        return fmt.Errorf("campaign maxDuration %s is less than 1 minute", campaign.Guardrails.MaxDuration)
    }
    if d > 2*time.Hour {
        return fmt.Errorf("campaign maxDuration %s exceeds 2 hour maximum", campaign.Guardrails.MaxDuration)
    }
    if campaign.Guardrails.ReadOnly && len(campaign.Guardrails.AllowedBranches) > 0 {
        return fmt.Errorf("readOnly campaign cannot have allowedBranches")
    }
    if !campaign.Guardrails.ReadOnly && campaign.Feedback.CreatePRs && len(campaign.Guardrails.AllowedBranches) == 0 {
        return fmt.Errorf("writable campaign with createPRs must specify allowedBranches")
    }
    return nil
}
```

This catches the `xa-provision-agent` bug (`readOnly: true` + write tools) at load time.

#### 6b. Adapter Contract Tests

**File**: `/home/jsullivan2/git/RemoteJuggler/deploy/adapters/adapter_test.go`

Add contract tests that verify prompt fidelity:

```go
func TestPromptContainsAllProcessSteps(t *testing.T) {
    // Load a real campaign JSON
    // Dispatch it to each backend
    // Verify the generated prompt contains every process step
    // Verify findings instruction is appended
    // Verify previous-run context is injected when provided
}

func TestFindingsExtractionRoundTrip(t *testing.T) {
    // Create a mock agent response with __findings__[...]__end_findings__
    // Verify extractFindings() correctly parses all fields
    // Verify campaign_id and run_id are stamped
    // Verify fingerprints survive the round-trip
}
```

#### 6c. Extend Dhall Type Checking to Campaign Schema

HexStrike already uses Dhall for policy definitions. Create a Dhall type for campaign definitions that can validate campaign JSON before it is loaded:

**New file**: `/home/jsullivan2/git/RemoteJuggler/test/campaigns/Campaign.dhall`

```dhall
let Agent = < ironclaw | picoclaw | hexstrike-ai | gateway-direct >
let Trigger = { schedule : Optional Text, event : Optional Text, dependsOn : Optional (List Text) }
let Guardrails = { maxDuration : Text, readOnly : Bool }
let Campaign = { id : Text, name : Text, agent : Agent, trigger : Trigger, guardrails : Guardrails }
in Campaign
```

This is opt-in and informational for now (Dhall runs as a CI check, not a runtime gate). The point is to extend HexStrike's formal methods culture to the broader campaign system.

Add a GitHub Actions workflow step to validate all campaign JSONs against the Dhall type:

```yaml
- name: Validate campaign schemas
  run: |
    for f in test/campaigns/**/*.json; do
      # Skip index.json and schema.json
      [[ "$(basename $f)" == "index.json" || "$(basename $f)" == "schema.json" ]] && continue
      echo "Validating $f..."
      # JSON schema validation
      npx ajv-cli validate -s test/campaigns/schema.json -d "$f"
    done
```

#### Verification
- `validateCampaign()` catches `xa-provision-agent`'s `readOnly`/write conflict
- All 48 campaign JSON files pass schema validation
- Contract tests pass: `cd deploy/adapters && go test -v ./...`
- Dhall type-checks without errors (if Dhall is available in CI)

---

### Day 7: Production Hardening

**Goal**: Eliminate operational hazards that would undermine autonomous operation.

#### 7a. Campaign Runner Deduplication

**Problem**: If two campaign-runner pods evaluate `isDue()` at the same cron minute, both dispatch the same campaign. The Collector stores both results (with different `runID`s), the Publisher creates two Discussions, and the FeedbackHandler might create duplicate issues.

**File**: `/home/jsullivan2/git/RemoteJuggler/test/campaigns/runner/scheduler.go`

Implement leader election via Setec (lightweight, no additional dependencies):

```go
func (s *Scheduler) acquireLock(ctx context.Context, campaignID string) (bool, error) {
    lockKey := fmt.Sprintf("campaigns/%s/lock", campaignID)
    lockValue := fmt.Sprintf("%s:%d", s.instanceID, time.Now().Unix())

    // Try to read existing lock
    existing, _ := s.collector.FetchLock(ctx, lockKey)
    if existing != "" {
        // Parse timestamp; if lock is <10 minutes old, another instance holds it
        parts := strings.SplitN(existing, ":", 2)
        if len(parts) == 2 {
            ts, err := strconv.ParseInt(parts[1], 10, 64)
            if err == nil && time.Now().Unix()-ts < 600 {
                return false, nil // Lock held by another instance
            }
        }
    }

    // Write our lock
    return true, s.collector.StoreLock(ctx, lockKey, lockValue)
}
```

Call `acquireLock()` in `RunCampaign()` before dispatch. If lock is held, skip.

**Alternative** (simpler): Enforce `replicas: 1` on the campaign-runner container in Terraform and use `Recreate` deployment strategy. This is already the pattern for agents (PVC RWO constraint). Document this as a hard requirement.

**File**: `/home/jsullivan2/git/RemoteJuggler/deploy/tofu/ironclaw.tf` (campaign-runner container spec)

Add comment and verify:
```hcl
# CRITICAL: campaign-runner must be replicas=1 to prevent duplicate dispatches.
# Leader election exists as a safety net but is not a substitute for single-replica.
replicas = 1
```

#### 7b. Rate Limiting

**File**: `/home/jsullivan2/git/RemoteJuggler/test/campaigns/runner/scheduler.go`

Add per-agent dispatch throttle:

```go
type RateLimit struct {
    maxPerHour int
    window     map[string][]time.Time // agent -> dispatch timestamps
}

func (r *RateLimit) Allow(agent string) bool {
    now := time.Now()
    cutoff := now.Add(-1 * time.Hour)

    // Clean old entries
    var recent []time.Time
    for _, t := range r.window[agent] {
        if t.After(cutoff) {
            recent = append(recent, t)
        }
    }
    r.window[agent] = recent

    if len(recent) >= r.maxPerHour {
        return false
    }
    r.window[agent] = append(r.window[agent], now)
    return true
}
```

Default: 10 campaigns per agent per hour. Configurable via environment variable `CAMPAIGN_RATE_LIMIT`.

#### 7c. Circuit Breaker

**File**: `/home/jsullivan2/git/RemoteJuggler/test/campaigns/runner/scheduler.go`

Track consecutive failures per agent:

```go
type CircuitBreaker struct {
    failures    map[string]int    // agent -> consecutive failure count
    trippedAt   map[string]time.Time
    threshold   int               // failures before tripping (default: 3)
    cooldown    time.Duration     // how long to wait before retrying (default: 1 hour)
}

func (cb *CircuitBreaker) RecordResult(agent, status string) {
    if status == "success" {
        cb.failures[agent] = 0
        delete(cb.trippedAt, agent)
        return
    }
    cb.failures[agent]++
    if cb.failures[agent] >= cb.threshold {
        cb.trippedAt[agent] = time.Now()
        log.Printf("CIRCUIT BREAKER: agent %s tripped after %d consecutive failures", agent, cb.failures[agent])
    }
}

func (cb *CircuitBreaker) IsOpen(agent string) bool {
    tripped, ok := cb.trippedAt[agent]
    if !ok {
        return false
    }
    if time.Since(tripped) > cb.cooldown {
        // Reset after cooldown
        delete(cb.trippedAt, agent)
        cb.failures[agent] = 0
        return false
    }
    return true
}
```

Check `cb.IsOpen(campaign.Agent)` before dispatch. If tripped, log and skip. The circuit resets after cooldown or on the next manual trigger.

#### 7d. Graceful Degradation: Gateway-Down Queue

If the gateway is unreachable during dispatch, queue the campaign for retry:

```go
type RetryQueue struct {
    mu    sync.Mutex
    queue []retryEntry
}

type retryEntry struct {
    campaign *Campaign
    addedAt  time.Time
    retries  int
}
```

In the main loop, drain the retry queue before evaluating new `isDue()` campaigns. Max 3 retries with exponential backoff (1m, 5m, 15m).

#### Verification
- Test dedup: run `scheduler.RunDue()` twice in the same minute in a test. Verify only one dispatch per campaign.
- Test rate limit: submit 15 campaigns for the same agent. Verify 10 succeed, 5 are throttled.
- Test circuit breaker: submit 3 campaigns that fail. Verify 4th is blocked. Wait cooldown. Verify 5th succeeds.
- Test retry: mock gateway returning 503. Verify campaign is queued and retried.

---

### Day 8: Provision-Agent Activation

**Goal**: The `xa-provision-agent` campaign can bootstrap a new agent from scratch.

#### 8a. Fix the readOnly/Write Conflict

**File**: `/home/jsullivan2/git/RemoteJuggler/test/campaigns/cross-agent/xa-provision-agent.json`

```diff
   "guardrails": {
     "maxDuration": "30m",
-    "readOnly": true
+    "readOnly": false,
+    "allowedBranches": ["sid/provision-*"],
+    "requireApproval": true
   }
```

#### 8b. Add Missing Feedback and Output Config

```diff
+  "feedback": {
+    "createIssues": true,
+    "createPRs": true,
+    "closeResolvedIssues": false,
+    "publishOnSuccess": true
+  },
+  "metrics": {
+    "successCriteria": "New agent repo created with workspace files, Dockerfile, GHCR workflow, and initial campaigns",
+    "kpis": [
+      "repos_created",
+      "files_pushed",
+      "workflows_created",
+      "campaigns_registered"
+    ]
+  }
```

#### 8c. Test with a Mock Agent

Do NOT provision a real agent in production for testing. Instead:

1. Create a temporary repo `tinyland-inc/agent-test-sandbox` (or use an existing test repo)
2. Trigger `xa-provision-agent` manually with campaign parameters pointing at the sandbox
3. Verify the agent creates: Dockerfile, workspace/AGENTS.md, workspace/IDENTITY.md, workspace/memory/MEMORY.md, .github/workflows/ghcr.yml
4. Delete the sandbox repo after verification

#### 8d. Document the Agent Onboarding Protocol

Add to the campaign's process steps a final documentation step:

```json
"Create a GitHub issue in tinyland-inc/remote-juggler documenting the new agent: name, purpose, repo URL, GHCR image tag, adapter type, and Terraform module path needed to deploy it"
```

The full onboarding sequence (documented, not automated):
1. `xa-provision-agent` creates repo + workspace + workflows
2. Human reviews and merges the provisioning PR
3. Human adds Terraform module in `deploy/tofu/` for the new agent (deployment, service, PVC, adapter sidecar)
4. Human adds campaign definitions in `test/campaigns/{agent}/`
5. Human updates `test/campaigns/index.json`
6. `tofu apply` brings the agent online
7. Campaign runner picks up new campaigns on next reload

#### Verification
- `xa-provision-agent` passes `validateCampaign()` (no readOnly/write conflict)
- Manual trigger on sandbox repo succeeds
- Created files match the template structure in existing agents
- Provisioning issue is created in `tinyland-inc/remote-juggler`

---

### Days 9-10: Full System Verification & State of the Agent Plane

**Goal**: Run every campaign, measure everything, produce a definitive assessment.

#### 9a. Sequential Campaign Sweep

Run all 48+ campaigns in controlled sequence to verify end-to-end operation:

```bash
# Phase 1: Health checks (gateway-direct, fast)
for c in cc-gateway-health cc-config-sync cc-cred-resolution cc-identity-switch cc-mcp-regression; do
  wget --post-data='' "http://localhost:8081/trigger?campaign=$c"
  sleep 30
done

# Phase 2: IronClaw campaigns (slowest agent, run in groups)
for c in oc-gateway-smoketest oc-dep-audit oc-coverage-gaps oc-dead-code oc-license-scan; do
  wget --post-data='' "http://localhost:8081/trigger?campaign=$c"
  sleep 120  # 2 min between dispatches (IronClaw needs time)
done

# Phase 3: HexStrike security scans
for c in hs-cred-exposure hs-dep-vuln hs-cve-monitor hs-container-vuln; do
  wget --post-data='' "http://localhost:8081/trigger?campaign=$c"
  sleep 90
done

# Phase 4: TinyClaw scans
for c in pc-identity-audit pc-credential-health pc-ts-package-scan; do
  wget --post-data='' "http://localhost:8081/trigger?campaign=$c"
  sleep 60
done

# Phase 5: Self-evolve (must run after other campaigns have produced results)
for c in oc-self-evolve pc-self-evolve hs-self-evolve; do
  wget --post-data='' "http://localhost:8081/trigger?campaign=$c"
  sleep 300  # 5 min between self-evolve runs
done

# Phase 6: Cross-agent
for c in xa-platform-health xa-identity-audit xa-token-budget xa-fork-health; do
  wget --post-data='' "http://localhost:8081/trigger?campaign=$c"
  sleep 90
done
```

#### 9b. Collect Metrics

After all campaigns complete, gather:

```bash
# Campaign results from Setec
for c in $(cat test/campaigns/index.json | jq -r '.campaigns | keys[]'); do
  echo "=== $c ==="
  # Query via gateway MCP
  curl -s -X POST http://localhost:8080/mcp -H 'Content-Type: application/json' \
    -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"juggler_setec_get\",\"arguments\":{\"name\":\"campaigns/$c/latest\"}}}" | jq .
done

# Aperture metering
curl -s -X POST http://localhost:8080/mcp -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"juggler_aperture_usage","arguments":{}}}'

# Discussion count
gh api graphql -f query='{ repository(owner:"tinyland-inc",name:"remote-juggler") { discussions(first:100) { totalCount } } }'
```

#### 9c. Generate "State of the Agent Plane" Report

Produce a structured report covering:

| Metric | Week 1 Baseline | Week 6 Actual | Target |
|--------|-----------------|---------------|--------|
| Campaigns defined | 48 | 49+ (added hs-self-evolve) | 48+ |
| Campaigns that ran successfully at least once | 5 (E2E verified) | 48+ | All enabled |
| Self-evolve campaigns completing <5min timeout | 0 | 3 | 3 |
| Agent-authored PRs | 0 | >=1 | >=1 |
| MEMORY.md with real observations | 0 | 3 | 3 |
| Aperture per-agent token breakdown available | No | Yes | Yes |
| Duplicate Discussions from dual runner | Unknown | 0 | 0 |
| Circuit breaker tested and working | No | Yes | Yes |
| `xa-provision-agent` tested | No | Yes (sandbox) | Yes |
| Cross-agent findings index entries | 0 | >0 | >0 |
| Token budget enforcement | Cosmetic only | Post-hoc check | Post-hoc |
| Campaign schema validation at dispatch | No | Yes | Yes |

#### 9d. Compare Against Week 1

Document specific before/after for each subsystem:

- **Adapters**: Week 1 = stateless prompt builder. Week 6 = context-aware with previous-run injection, branch constraints, and guardrail enforcement.
- **Self-evolve**: Week 1 = 3-5m timeout, no feedback hooks, template MEMORY.md. Week 6 = 20-25m timeout, PR creation enabled, real observations in memory.
- **Metering**: Week 1 = S3 403 errors, no per-agent breakdown. Week 6 = working S3 ingestion, per-agent/per-campaign token counts, budget enforcement.
- **Safety**: Week 1 = no dedup, no rate limits, no circuit breaker. Week 6 = Setec lock-based dedup, 10/hr/agent rate limit, 3-failure circuit breaker.

---

## Completion Metrics

Each metric below must be independently verifiable. "Verifiable" means a specific command or observation that produces a boolean pass/fail.

| # | Metric | Verification Command | Pass Criteria |
|---|--------|---------------------|---------------|
| 1 | Self-evolve campaigns complete without timeout | `juggler_setec_get(name="campaigns/oc-self-evolve/latest")` | `status != "timeout"` |
| 2 | MEMORY.md has real observations | `gh api repos/tinyland-inc/ironclaw/contents/workspace/memory/MEMORY.md` | "Observations" section is non-empty, contains dates |
| 3 | At least 1 agent-authored PR | `gh pr list --repo tinyland-inc/remote-juggler --label self-evolution --author "app/rj-agent-bot"` | Count >= 1 |
| 4 | Aperture per-agent token report | `juggler_aperture_usage()` | `by_agent` has entries for ironclaw, picoclaw, hexstrike-ai |
| 5 | All enabled campaigns ran at least once | Check Setec `campaigns/{id}/latest` for each | All 48+ have non-null `status` |
| 6 | Zero duplicate Discussions | `gh api graphql` query for discussions with same title | No duplicates within 1-hour window |
| 7 | Circuit breaker tested | Unit test `TestCircuitBreaker` passes | 3 failures trip, cooldown resets |
| 8 | Rate limiter tested | Unit test `TestRateLimit` passes | 11th dispatch in 1hr is blocked |
| 9 | `xa-provision-agent` runs without readOnly error | Manual trigger on sandbox | `status: "success"` |
| 10 | Schema validation catches invalid campaigns | Unit test `TestValidateCampaign` with invalid inputs | Returns error for readOnly+write, short timeout, etc. |
| 11 | Cross-agent findings index exists | `juggler_setec_get(name="findings/index")` | JSON with entries from multiple agents |
| 12 | Previous-run context appears in agent prompts | Check adapter logs for "Previous Run Context" | Present on second+ runs |

---

## The North Star

A **fully autonomous agent plane** means:

1. **Self-sustaining**: Campaigns run on schedule without human intervention. The scheduler dispatches, agents execute, results are stored, findings create issues, resolutions close issues. This cycle runs 24/7.

2. **Self-aware**: Every agent knows what it did last time, what it learned, and what changed. MEMORY.md is not a template -- it is a living document updated by the agent from real observations.

3. **Self-improving**: At least once per week, an agent identifies a deficiency in its own campaign definitions and proposes a concrete fix via PR. The improvement cites specific data from previous runs. A human approves or rejects, but the *proposal* is autonomous.

4. **Metered**: Every LLM token consumed by every agent is tracked, attributed to a specific campaign, and visible via `juggler_aperture_usage`. Token budgets are enforced (post-hoc in Week 6, pre-emptive in a future sprint).

5. **Resilient**: If an agent fails 3 times in a row, the circuit breaker trips and alerts. If the gateway goes down, campaigns are queued for retry. If two runners race, deduplication prevents double-dispatch. The system degrades gracefully, never catastrophically.

6. **Observable**: The state of every campaign is queryable via `juggler_campaign_status`. Every tool call is in the audit log. Every finding is in the findings index. A human can answer "what have the agents been doing?" in under 60 seconds.

In concrete terms at the end of Week 6: you should be able to leave the cluster running for a week, come back, and find that agents have (a) run all campaigns on schedule, (b) updated their memory with observations, (c) opened at least one PR improving a campaign, (d) consumed a trackable and budgeted amount of tokens, and (e) not created any duplicate noise. The system does useful work unsupervised.

---

## Risk Register

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| **Self-evolve agent creates broken campaign JSON** | Medium | High (breaks future dispatches) | Require schema validation in CI. `requireApproval: true` means no auto-merge. |
| **Token budget too low causes self-evolve to fail** | High | Medium (wasted run, no learnings) | Set generous budgets (200K tokens). Monitor actual usage for first 3 runs before tightening. |
| **S3 credentials still 403 after fix** | Low | Medium (metering gap) | Verify with `aws s3 ls` after credential propagation. Fall back to webhook-only metering. |
| **Setec lock race condition** | Low | Medium (rare duplicate dispatch) | Lock window is 10 minutes, campaigns take >5 minutes. Race window is <1 second at cron boundary. Single-replica is the primary defense. |
| **Agent writes to MEMORY.md but content is garbage** | Medium | Low (self-correcting next run) | Self-evolve process step: "Compact memory: remove duplicate entries, prune stale observations." Bad content gets pruned. |
| **Circuit breaker trips on transient failures** | Medium | Medium (campaigns delayed) | 1-hour cooldown is conservative. Add Setec-stored "recent failures" so manual reset is possible via `juggler_setec_put`. |
| **Cross-agent memory reading creates infinite loops** | Low | Low (agents read each other, update memory, trigger next read) | Self-evolve runs once daily (cron). Even if Agent A copies from Agent B, the next read is 24h later. No recursive triggering. |
| **`xa-provision-agent` creates a malformed repo** | Medium | Medium (cleanup needed) | Test exclusively on sandbox repo. Require human review of all provisioning PRs. |
| **Dhall type-checking rejects valid campaigns** | Low | Low (CI failure, easy to fix) | Dhall validation is opt-in and informational. Does not block deployment. |
| **Previous-run context bloats prompt, wastes tokens** | Medium | Low | Limit context to summary fields (status, finding count, error). Never include full findings in context. Cap at 500 chars. |

---

## File Change Summary

### Modified Files

| File | Change |
|------|--------|
| `test/campaigns/runner/collector.go` | Add `FetchPreviousResult()`, `UpdateFindingsIndex()`, `FetchLock()`, `StoreLock()` |
| `test/campaigns/runner/scheduler.go` | Add previous-run fetch, `validateCampaign()`, `RateLimit`, `CircuitBreaker`, `acquireLock()`, run counter |
| `test/campaigns/runner/dispatcher.go` | Accept `*CampaignResult` in `Dispatch()` and `dispatchToAgent()` |
| `deploy/adapters/adapter.go` | Extend `CampaignRequest` with `PreviousResult`, add guardrail prompt injection |
| `deploy/adapters/ironclaw.go` | Inject previous-run context and branch constraints into prompt |
| `deploy/adapters/picoclaw.go` | Inject previous-run context into prompt |
| `deploy/adapters/hexstrike.go` | Inject previous-run context into prompt |
| `test/campaigns/openclaw/oc-self-evolve.json` | Timeout 5m->25m, tokens 80K->200K, enable createIssues/createPRs, add allowedBranches, add github write tools, add self-modification process steps |
| `test/campaigns/picoclaw/pc-self-evolve.json` | Timeout 3m->20m, tokens 50K->150K, enable createIssues, add cross-agent memory steps |
| `test/campaigns/cross-agent/xa-provision-agent.json` | readOnly false, add allowedBranches, add feedback/metrics |
| `test/campaigns/index.json` | Add `hs-self-evolve` entry |
| `gateway/tools.go` | Enhance `juggler_aperture_usage` response with per-agent/per-campaign breakdown |
| `deploy/tofu/apply.sh` | Ensure S3 credential export |

### New Files

| File | Purpose |
|------|---------|
| `test/campaigns/hexstrike/hs-self-evolve.json` | HexStrike self-evolution campaign |
| `test/campaigns/Campaign.dhall` | Dhall type definition for campaign schema validation |
| `agent_plane/week5-6_self_evolution.md` | This plan document |

### New Tests

| File | Tests |
|------|-------|
| `test/campaigns/runner/collector_test.go` | `TestFetchPreviousResult`, `TestUpdateFindingsIndex` |
| `test/campaigns/runner/scheduler_test.go` | `TestValidateCampaign`, `TestRateLimit`, `TestCircuitBreaker`, `TestAcquireLock` |
| `deploy/adapters/ironclaw_test.go` | `TestDispatchWithPreviousContext`, `TestPromptContainsAllProcessSteps` |
| `deploy/adapters/adapter_test.go` | `TestFindingsExtractionRoundTrip` |
