# Sprint: From Theater to Reality

## Date: 2026-03-02
## Supersedes: EPIC.md (theatrical metrics), progress.md (self-reported numbers)

---

## The Problem

The agent platform is a Potemkin village. The progress tracker says "93 findings,
47/47 campaigns, Gate 3 CONDITIONAL GO." The reality:

| Claim | Reality |
|-------|---------|
| "93 findings with tool data" | Findings are LLM confabulations. IronClaw reports "Critical command injection in Remote.chpl lines 712-717" without ever reading that file. |
| "47/47 campaigns running" | 4 campaigns ran in 48h. HexStrike returns in 0s ("skipped gateway tool" x6). IronClaw produces 0 tool calls. |
| "8 cross-agent chains" | xa-finding-handoff runs IronClaw which hallucinates findings, then oc-handoff-response hallucinates responses to the hallucinated findings. |
| "36+ GitHub Issues from agents" | Issues contain confabulated vulnerability reports. No agent has read the code it's reporting on. |
| "1 agent-authored PR merged" | PR #120 was the only merge. PRs #283-285 destroyed files. |
| "50+ Discussions with content" | Empty boilerplate templates. Zero discussion comments. |
| "Kill switch tested" | Kill switch was accidentally left on, blocking campaigns for hours. |
| "Budget enforcement verified" | Budget counted response bytes, not tokens. Meaningless metric. |

### Root Causes

1. **IronClaw never calls rj-tool**: The adapter sends a prompt that *describes* rj-tool,
   but OpenClaw's internal agent loop reads workspace files and hallucinates instead of
   executing `exec("/workspace/bin/rj-tool ...")`.
2. **HexStrike adapter has no LLM**: It iterates the tool list and short-circuits every
   `juggler_*` / `github_*` tool with "skipped (gateway tool)". Duration = 0s.
3. **TinyClaw never dispatched**: tools_proxy.go exists but `fetchGatewayTools()` is never
   called. Skills loader finds 0 skills despite workspace files existing.
4. **No verification**: Campaigns report "success" with 0 tool calls and 0 duration.
   The scheduler treats this as a pass.
5. **Confabulation is undetectable**: The "tool-call-required validation" checks for
   `tool_calls > 0`, but IronClaw's OpenClaw runs tools internally (invisible in response),
   so heuristic counting returns 0 and passes validation anyway.
6. **Write path is theoretical**: `github_patch_file` exists. FeedbackHandler
   `ProcessPRFindings()` exists. But no agent has ever produced findings with `fixable: true`
   + `remediation_hints` because agents are never told this format.

### What Actually Works

- Gateway: 59 MCP tools, all functional when called directly
- `cc-gateway-health`: Gateway-direct campaign that tests tools without an agent (works)
- `cc-mcp-regression`: 43/43 tool responses clean (tests gateway, not agents)
- IronClaw makes real LLM calls (Sonnet, ~$0.10/call, 2-5 min). It just doesn't use tools.
- Infrastructure is real: K8s pods, GHCR images, Setec secrets, Aperture metering, tofu CI

---

## Design Principles (from research)

### From SICA (Self-Improving Coding Agent)
**One real thing before scaling.** SICA achieved 17% to 53% SWE-bench by running 15
self-improvement iterations on a single task type. Not by running 53 campaigns simultaneously.
The edit-test-fix loop is the fundamental unit. An agent reads code, makes a change, runs
tests, and iterates until tests pass. Everything else is noise.

### From Reflexion
**Persistent memory of failures.** Reflexion stores verbal self-critiques in an episodic
memory buffer. The agent re-reads its own failure analysis before attempting the next task.
Our agents have MEMORY.md files that are empty. Memory is not optional decoration -- it
is the mechanism by which an agent avoids repeating mistakes.

### From AlphaEvolve
**Automated verification, not self-reported metrics.** AlphaEvolve saves 0.7% of Google's
compute because every proposed optimization is verified by running the actual workload.
Our "findings" are unverified LLM claims. The fix: nothing counts unless it passes an
automated check that the agent does not control.

### From Claude Code
**Simple architecture beats complex orchestration.** Claude Code is a single-threaded
master loop: read → edit → test → iterate. It does not have 53 campaigns, 4 agent types,
and 3 adapters. It has one loop. Our agents should have one loop.

### The Nakajima Principle
**Design RULES AND TESTS that govern what changes are allowed.** An agent's autonomy
boundary is defined by what tests exist to verify its output. No tests = no autonomy.

---

## 6-Phase Sprint

### Phase 0: Honest Baseline (Day 1) -- COMPLETED 2026-03-02

**Status**: DONE. /proof endpoint deployed, confabulation detection active, 51 campaigns disabled.

**Results**:
- `/proof` endpoint live at `sha-fbbf9f3` with `ProofEntry` struct and `CheckConfabulation()`
- 6 new tests (115 total campaign runner tests, all pass)
- `ic-proof-of-life` campaign created with explicit tool-forcing prompt
- 51 theatrical campaigns disabled, only `cc-gateway-health` + `ic-proof-of-life` enabled
- Actual tool chains documented in `agent_plane/tool-chains.md`

**Goal**: Establish ground truth metrics that cannot be gamed.

**Actions**:
1. Deploy a `/proof` endpoint on campaign-runner that returns raw evidence:
   - For each campaign: last run timestamp, duration, raw tool call log (not count),
     actual gateway audit entries (not self-reported), stderr output
   - If duration < 1s and tool_calls > 0, flag as SUSPICIOUS
   - If findings exist but tool_trace is empty or all "skipped", flag as CONFABULATED
2. Add a `confabulation_check` to FeedbackHandler:
   - A finding about file X is rejected unless the agent called `github_fetch` on file X
     (or `exec("cat X")` for IronClaw) in the same campaign run
   - This is the AlphaEvolve principle: verify claims against evidence
3. Disable all campaigns except `cc-gateway-health` (the only honest one)
4. Document the actual tool call chain for each agent type

**Metrics**:
| Metric | How Measured | Cannot Be Gamed Because |
|--------|-------------|------------------------|
| Gateway audit entries from agents | `/audit` endpoint, filtered by caller != campaign-runner | Gateway logs every MCP call server-side |
| Agent LLM cost (real $) | Aperture billing or Anthropic dashboard | Money is real |
| Tool calls verified in gateway log | Cross-reference campaign tool_trace with gateway audit | Two independent logs must agree |
| Confabulation rate | findings_without_evidence / total_findings | Server-side evidence check |

**Go/NoGo**: Proceed when `/proof` endpoint deployed and confabulation check rejects the
current IronClaw findings as CONFABULATED.

**Ask**: Are you comfortable disabling all campaigns except cc-gateway-health while we fix
the foundations?

---

### Phase 1: One Agent, One Real Tool Call (Days 2-5) -- GO 2026-03-02

**Status**: FIRST VERIFIED TOOL CALL. IronClaw called `github_fetch` via rj-tool, returned
real content from `gateway/main.go` (8 func declarations, exact match verified).

**Evidence** (ic-proof-of-life-1772473032, 2026-03-02T17:37:12Z):
- tool_calls=1 (github_fetch via exec/rj-tool)
- duration=95s (real Sonnet LLM call)
- confabulated=false (no flags)
- Output: 8 functions listed, all match `grep "^func " gateway/main.go` exactly
- File SHA: d00e0969e1eb54aae9dfc88a597552207003ffd6

**Key fix**: Explicit tool-forcing language in campaign prompt:
  "You MUST use the exec tool to call rj-tool. Do NOT guess or fabricate file contents."

**Goal**: IronClaw calls one gateway tool and returns the real result.

**Why IronClaw**: It's the closest to working. It makes real LLM calls. It has the rj-tool
wrapper. The problem is that OpenClaw's agent loop never executes `exec("/workspace/bin/rj-tool ...")`.

**Root Cause Investigation**:
- Read IronClaw adapter source (`deploy/adapters/ironclaw.go`) to understand prompt construction
- Read IronClaw workspace files (`deploy/fork-dockerfiles/ironclaw/workspace/TOOLS.md`) to
  understand what the agent sees
- Read OpenClaw logs to understand why `exec` tool is not called
- Hypothesis: The system prompt describes rj-tool but doesn't give the agent a reason to
  call it. The campaign prompt asks "analyze security" but doesn't say "use rj-tool to
  read files first"

**Fix Strategy** (incremental, not big-bang):

1. **Step 1**: Create a test campaign `ic-proof-of-life` that gives IronClaw a task it
   MUST use tools for:
   ```
   "Read the file gateway/main.go using exec('/workspace/bin/rj-tool github_fetch
   owner=tinyland-inc repo=remote-juggler path=gateway/main.go'). Report the first
   10 lines of the file. Do NOT guess -- you must use the tool."
   ```
   This is the simplest possible test: can the agent execute a command and return its output?

2. **Step 2**: If Step 1 works, create `ic-read-and-report` that asks IronClaw to:
   - Read a specific file via rj-tool
   - Count the number of functions in it
   - Report the function names
   - Verify by cross-referencing with `github_fetch` output in gateway audit

3. **Step 3**: If Step 2 works, modify one existing security campaign (`hs-cred-exposure`
   routing through IronClaw) to require tool evidence before reporting findings.

**Verification (automated, not self-reported)**:
```bash
# After ic-proof-of-life runs:
# 1. Gateway audit must contain github_fetch call from adapter
kubectl exec deploy/rj-gateway -c rj-gateway -- wget -qO- http://localhost:8080/audit | \
  python3 -c "import json,sys; d=json.load(sys.stdin); print([e for e in d if 'github_fetch' in str(e)])"

# 2. Campaign result must contain actual file content (not hallucination)
kubectl exec deploy/ironclaw-agent -c campaign-runner -- wget -qO- 'http://localhost:8081/status' | \
  python3 -c "import json,sys; d=json.load(sys.stdin); r=d.get('ic-proof-of-life',{}); print(r)"

# 3. Cross-reference: gateway log timestamp must fall within campaign run window
```

**Go/NoGo**:
- **GO**: IronClaw's `ic-proof-of-life` campaign produces output that matches `github_fetch`
  result in gateway audit log. Two independent data sources agree.
- **NOGO**: If IronClaw cannot call rj-tool after 3 days of prompt engineering, PIVOT:
  Replace OpenClaw with a simpler agent loop (Claude Code-style single-thread) that calls
  gateway tools directly via HTTP. OpenClaw's internal agent loop is a black box we can't
  control.
- **ASK**: If the NoGo triggers, do you want to (a) keep debugging OpenClaw, (b) replace
  IronClaw's agent engine, or (c) focus on TinyClaw instead?

---

### Phase 2: Read-Edit-Test Loop (Days 6-12) -- GO 2026-03-02

**Evidence**: PR #340 (ic-edit-test-1772479412)
- IronClaw read identity.go via github_fetch, analyzed parseCapabilities/HasCapability
- Wrote 131-line test file (gateway/identity_test.go) with table-driven tests
- Created branch `ic-add-identity-tests`, pushed file, created PR #340
- **Gateway Tests: PASS** — `go test ./...` green on the new test file
- 7 parseCapabilities cases + 6 HasCapability cases, all passing
- Duration: 1m18s, real LLM call + real tool calls via OpenClaw internal loop

**Bugs Fixed Along the Way**:
1. Attempt 1 (PR #327): Shell escaping broke multi-line Go code → file was just `-`
2. Fix: Added `--content-file` and `--json` modes to rj-tool
3. Attempt 2: rj-tool sed injection corrupted JSON (`\n`→newline, `\"`→bare quote)
4. Fix: Replaced sed with node/python JSON merge
5. Attempt 3: OpenClaw not ready (2ms connection refused, pod still starting)
6. Attempt 4: SUCCESS — PR #340 with passing CI

**Goal**: IronClaw reads a file, makes a change, and the change passes CI.

**Prerequisites**: Phase 1 GO. Agent can call tools and return real results.

**The Loop** (SICA pattern):
```
1. Read: Agent calls github_fetch to read a file
2. Analyze: Agent identifies something to change (typo, missing test, stale comment)
3. Edit: Agent calls github_patch_file to make the change
4. Test: CI runs on the resulting PR
5. Iterate: If CI fails, agent reads the failure, adjusts, tries again
```

**Implementation**:

1. **Campaign `ic-edit-test`**: Give IronClaw a concrete, verifiable task:
   - "Read `gateway/identity.go`. Find the function `GetAuditEntries`. Add a unit test
     for the case where `n=0`. Create a branch and PR via github_create_branch +
     github_patch_file + github_create_pr."
   - This is a REAL task. The test either passes or doesn't. No confabulation possible.

2. **CI gate**: The PR must pass `go test ./...` in CI. If it doesn't, the campaign
   result is FAILURE, not SUCCESS.

3. **Iteration**: If the PR fails CI, schedule a follow-up campaign that:
   - Reads the CI failure output (via `github_fetch` on the checks API)
   - Reads the file it modified
   - Creates a new commit fixing the issue
   - This is the SICA edit-test-fix loop

**Verification**:
- PR exists on GitHub with `rj-agent-bot[bot]` as author
- PR passes CI (green checks, not just "created")
- Diff is non-trivial (not just whitespace)
- The test it added actually tests something (human review)

**Metrics**:
| Metric | Target | Measurement |
|--------|--------|-------------|
| PRs created by agent | 1+ | GitHub API |
| PRs passing CI | 1+ | GitHub checks API |
| Edit-test-fix iterations | Tracked | Campaign run count for same branch |
| Time from task to merged PR | < 24h | Timestamps |

**Go/NoGo**:
- **GO**: At least 1 PR passes CI. The diff makes sense to a human reviewer.
- **NOGO**: Agent creates PRs that consistently fail CI and cannot self-correct after
  3 iterations. PIVOT: Reduce scope to read-only analysis with verified tool evidence
  (no write path yet).
- **ASK**: What repository should the agent make its first real edit to? (remote-juggler
  itself, or a test repo?)

---

### Phase 3: Persistent Memory + Learning (Days 13-18) -- GO 2026-03-02

**Status**: VERIFIED. Full memory loop working. Agent writes real reflections, receives them on next run, and references previous notes.

**Evidence** (two consecutive runs of ic-memory-test):
- Run 1 (ic-memory-test-1772484307): Agent called `github_fetch` (audit: 10.0.6.89, allowed=true), read `gateway/resolver.go` (5096 bytes), identified Resolver struct with 4 backends (env, sops, kdbx, setec), real function names (Resolve(), resolveSOPS(), resolveKDBX()). Wrote 841 bytes of reflection via `__memory__` markers.
- Run 2 (ic-memory-test-1772484617): Runner loaded 841 bytes of previous memory. Agent read same file (audit: 10.0.6.181, allowed=true). New reflection (960 bytes) **references previous run**: "Second analysis... focusing on MCP proxy implementation and Chapel integration as planned from previous run." Found NEW observations: code duplication between resolveSOPS()/resolveKDBX(), hardcoded request IDs.

**Implementation (v2 — marker extraction, not tool calls)**:
- v1 (requiring agent to call setec_put) FAILED: OpenClaw makes max 1-2 exec() tool calls per session, rj-tool shell word splitting truncates multi-word values
- v2: Adapter extracts text between `__memory__...__end_memory__` markers from agent output. Campaign runner writes to Setec automatically. Agent only needs to output text between markers.
- Memory stored at `remotejuggler/agents/{agent}/memory` in Setec
- Memory loaded by campaign runner before dispatch and injected as "Notes from Previous Runs" in prompt

**Key files**: `deploy/adapters/adapter.go` (extractReflection), `test/campaigns/runner/collector.go` (StoreAgentMemory), `test/campaigns/runner/scheduler.go` (pre/post memory hooks)

**Goal**: Agent remembers what it learned and applies it next time.

**Prerequisites**: Phase 2 GO. Agent can read, edit, test, iterate.

**The Reflexion Pattern**:

After each campaign run, the agent writes a self-critique to its MEMORY.md:
```markdown
## 2026-03-15: ic-edit-test run #3
### What happened
- Read gateway/identity.go, proposed test for GetAuditEntries(n=0)
- First attempt: test failed because I used wrong import path
- Second attempt: test passed but PR had merge conflict with main
- Third attempt: rebased and merged successfully

### What I learned
- Always check import paths before submitting (read go.mod first)
- Create branches from latest main, not from stale HEAD
- The gateway package uses "testing" + "github.com/stretchr/testify/assert"

### Rules for next time
1. Read go.mod before writing Go tests
2. Fetch latest main SHA before creating branch
3. Use testify/assert, not raw if/t.Error
```

**Implementation**:

1. Add a `post_run_reflection` step to campaign runner:
   - After a campaign completes, re-dispatch the agent with the prompt:
     "You just ran campaign X. Here is the result: [result]. Write a brief reflection
     in MEMORY.md about what worked, what didn't, and what rules you should follow
     next time. Use github_patch_file to append to your workspace MEMORY.md."
   - This creates the episodic memory buffer from Reflexion.

2. Add a `pre_run_context` step:
   - Before dispatching a campaign, read the agent's MEMORY.md and include it in the prompt
   - "Here are your notes from previous runs: [MEMORY.md contents]"

3. Verify memory is actually used:
   - MEMORY.md must be non-empty after 3 runs
   - Later runs should reference earlier lessons
   - Campaign performance should improve (fewer CI failures, fewer iterations)

**Metrics**:
| Metric | Target | Measurement |
|--------|--------|-------------|
| MEMORY.md size (bytes) | > 500 after 5 runs | github_fetch on workspace MEMORY.md |
| References to past runs in memory | 3+ | grep for date patterns in MEMORY.md |
| CI pass rate improvement | Run N+3 > Run N | GitHub checks over time |
| Repeated mistakes | Decreasing | Same error type in consecutive runs |

**Go/NoGo**: **GO** (2026-03-02)
- Memory is substantive (real function names, specific observations, not generic platitudes)
- Agent references past run: "as planned from previous run"
- New observations in second run: code duplication, hardcoded request IDs
- Memory stored in Setec (accessible across pod restarts, not tied to PVC)

---

### Phase 4: Skill Library + Capability Compounding (Days 19-25) -- IMPLEMENTED 2026-03-02

**Status**: CODE COMPLETE. Skill library infrastructure deployed. Awaiting live verification (3+ verified skills, agent reuse observed).

**Implementation**:
- `extractSkill()` + `__skill__`/`__end_skill__` text markers in adapter (same pattern as `__memory__`/`__findings__`)
- IronClaw: `loadSkills()` loads workspace SKILL.md files + dynamic skills from Setec injected into prompt
- TinyClaw: Dynamic skills from Setec injected alongside existing static workspace skills
- Collector: `GetAgentSkills()`/`StoreAgentSkill()` with upsert-by-name (parse `### Skill:` headers, replace or append)
- Skills stored at `remotejuggler/agents/{agent}/skills` in Setec
- Scheduler: loads skills pre-dispatch, stores new skills post-dispatch
- 3 skill-building campaigns: `ic-skill-file-analysis`, `ic-skill-test-writer`, `ic-skill-code-review`
- 11 new unit tests (49 adapter total, 109 runner total), all passing

**Key files**: `deploy/adapters/adapter.go` (extractSkill), `deploy/adapters/ironclaw.go` (loadSkills, skills injection), `deploy/adapters/tinyclaw.go` (dynamic skills), `test/campaigns/runner/collector.go` (GetAgentSkills, StoreAgentSkill, upsertSkill), `test/campaigns/runner/scheduler.go` (pre/post skill hooks)

**Goal**: Agent builds reusable skills that compound over time.

**Prerequisites**: Phase 3 GO. Agent has persistent memory and improving performance.

**The Voyager Pattern**:

Skills are verified, reusable procedures the agent has proven it can execute:

```markdown
# Skill: add-go-test
## Verified: 2026-03-20 (3 successful executions)
## Steps:
1. github_fetch the target Go file
2. github_fetch go.mod to get module path
3. Generate test using testify/assert (per MEMORY.md rule #3)
4. github_create_branch from latest main
5. github_patch_file to add test file
6. github_create_pr with test description
7. Wait for CI, read result, fix if needed
## Prerequisites: target file must be in gateway/ package
## Failure modes: import path mismatch (check go.mod first)
```

**Implementation**:

1. **Skill format**: Skills are markdown files in `/workspace/skills/` with structured
   sections: Steps, Prerequisites, Failure modes, Verification count.

2. **Skill verification**: A skill is "verified" after N successful executions (default 3).
   Before that, it's "experimental."

3. **Skill discovery**: When the agent encounters a new task type, it:
   - Checks if a matching skill exists
   - If yes, follows the skill steps
   - If no, attempts the task, and if successful, writes a new skill

4. **Skill catalog campaigns**:
   - `ic-skill-go-test`: Add tests to untested Go functions (builds `add-go-test` skill)
   - `ic-skill-fix-lint`: Fix linting errors (builds `fix-lint-error` skill)
   - `ic-skill-update-dep`: Update a Go dependency (builds `update-dependency` skill)

5. **Capability compounding**: Each new skill makes the agent more capable. After 10
   verified skills, the agent can handle a wider range of tasks autonomously.

**Metrics**:
| Metric | Target | Measurement |
|--------|--------|-------------|
| Verified skills | 3+ | Count files in /workspace/skills/ with verification >= 3 |
| Task completion rate with skills | > 70% | Campaigns using skills that pass CI |
| Task completion rate without skills | Baseline | Campaigns on novel tasks |
| Skill reuse count | 5+ total | References to skills in campaign logs |

**Go/NoGo**:
- **GO**: 3+ verified skills. Agent uses skills without being explicitly told to. Task
  completion rate with skills > without.
- **NOGO**: Skills are written but never used. Agent ignores skill library. PIVOT:
  Inject skills as mandatory procedure in campaign prompts (remove agent choice).
- **ASK**: Which task types should be the first skills? (testing, linting, dependency
  updates, documentation, or something else?)

---

### Phase 5: Multi-Agent Coordination (Days 26-32) — IMPLEMENTED 2026-03-02
**Goal**: Two agents collaborate on a task, with verified handoff.

**Status**: Code complete. TinyClaw agentic dispatch (direct Anthropic API + gateway tool execution) implemented in adapter. Cross-agent handoff campaigns created (IronClaw produces finding → Setec → TinyClaw verifies). 55 adapter tests passing (6 new). Awaiting deployment + live verification.

**Prerequisites**: Phase 4 GO. At least one agent has verified skills and persistent memory.

**Why This Is Phase 5, Not Phase 2**:

The existing "cross-agent" campaigns are theater: IronClaw hallucinates findings, then
itself (in a different campaign) hallucinates responses. Real coordination requires:
1. Agent A does real work and produces verified output
2. Agent B receives that output and does different real work
3. The combined result is better than either alone

Without Phases 1-4, there's nothing real to coordinate.

**Implementation**:

1. **Verified handoff protocol**:
   - Agent A (IronClaw) runs a security scan (reads actual code via tools)
   - Agent A writes findings to a GitHub Discussion with evidence (file paths, line numbers,
     tool call IDs that can be verified in gateway audit)
   - Agent B (TinyClaw or HexStrike) reads the Discussion, verifies the evidence by
     fetching the same files, and either confirms or disputes the finding
   - If confirmed, Agent B creates a fix PR

2. **Evidence chain**: Every finding in the handoff includes:
   ```json
   {
     "file": "gateway/identity.go",
     "line": 153,
     "evidence_tool_call_id": "audit-entry-12345",
     "claim": "allocation without bounds check",
     "raw_content": "result := make([]AuditEntry, n)"
   }
   ```
   The receiving agent can verify `evidence_tool_call_id` against the gateway audit.

3. **Second agent activation**:
   - Before this phase, we need to fix either TinyClaw or HexStrike
   - TinyClaw path: Wire `fetchGatewayTools()` into dispatch, inject skills
   - HexStrike path: Replace adapter tool-loop with LLM-backed dispatch
   - Choose whichever is cheaper to fix

**Metrics**:
| Metric | Target | Measurement |
|--------|--------|-------------|
| Verified handoffs (evidence chain intact) | 3+ | Discussions with cross-referenced audit IDs |
| Second agent real tool calls | 5+ | Gateway audit entries from second agent |
| Combined task completion | 1+ | PR created by Agent B from Agent A's finding |
| Disputed findings (Agent B disagrees) | Any | Shows real analysis, not rubber-stamping |

**Go/NoGo**:
- **GO**: At least 1 handoff where Agent B independently verifies Agent A's finding
  and creates a fix. Evidence chain is intact.
- **NOGO**: Second agent can't be made functional. PIVOT: Single-agent model where
  IronClaw handles everything. Multi-agent is not required for self-improvement.
- **ASK**: Which second agent to activate? TinyClaw (closer to working, Anthropic SDK
  native) or HexStrike (more powerful tools, but adapter needs LLM)?

---

### Phase 6: Self-Improvement Loop (Days 33-40)
**Goal**: Agent improves its own configuration, and the improvement is measurable.

**Prerequisites**: Phase 5 GO (or Phase 4 GO if single-agent pivot).

**The AlphaEvolve Pattern**:

The agent proposes changes to its own campaign definitions, workspace files, or skill
library. The change is accepted ONLY if it improves a measurable metric. The metric
is computed by the system, not self-reported by the agent.

**Implementation**:

1. **Self-improvement campaign** (`ic-self-improve`):
   - Agent reads its own campaign definitions (`test/campaigns/openclaw/*.json`)
   - Agent reads its performance history (from Setec: success rate, duration, CI pass rate)
   - Agent proposes a change (modified campaign prompt, new skill, adjusted guardrails)
   - Agent creates a PR with the change
   - PR is merged to a `sid/self-improve` branch (not main)
   - Campaign runner runs the modified campaign on the branch
   - If performance improves (metric delta > 0), PR is promoted to main
   - If performance degrades, PR is closed with explanation

2. **Metrics the agent can optimize**:
   - CI pass rate on PRs it creates
   - Time to first successful PR
   - Number of iterations needed per task
   - False positive rate in security findings (verified against CodeQL)

3. **Metrics the agent CANNOT modify**:
   - The verification checks (confabulation detection, evidence chain)
   - The CI pipeline itself
   - The gateway audit logging
   - The Nakajima Principle: tests govern the boundary

4. **Constitutional guardrails**:
   - Agent cannot modify its own verification checks
   - Agent cannot disable CI requirements on its PRs
   - Agent cannot modify the campaign runner code
   - Agent CAN modify: campaign prompts, skill files, MEMORY.md, workspace TOOLS.md
   - Human approval required for: changes to guardrails, budget increases, new tool access

**Metrics**:
| Metric | Target | Measurement |
|--------|--------|-------------|
| Self-improvement PRs proposed | 3+ | GitHub API |
| Self-improvement PRs that improved metrics | 1+ | Before/after comparison |
| Performance trend over 10 runs | Improving | Time-series of success rate |
| Constitutional violations attempted | 0 | Audit log of rejected modifications |

**Go/NoGo**:
- **GO**: Agent proposes a change that measurably improves its own performance. The
  improvement is verified by the system, not self-reported.
- **NOGO**: Agent's self-improvement proposals consistently degrade performance. PIVOT:
  Human-guided improvement with agent as research assistant (reads code, reports findings,
  human decides changes).
- **ASK**: What constitutes "measurably better"? (5% improvement threshold? Any positive
  delta? Statistical significance over 10 runs?)

---

## Dependency Graph

```
Phase 0 (Honest Baseline)
    |
    v
Phase 1 (One Real Tool Call)
    |
    v
Phase 2 (Read-Edit-Test Loop)
    |
    v
Phase 3 (Persistent Memory)
    |
    +----> Phase 4 (Skill Library)
    |          |
    |          v
    |     Phase 5 (Multi-Agent)
    |          |
    v          v
Phase 6 (Self-Improvement)
```

Each phase depends on the previous. There are no parallel workstreams because the
lesson from the theatrical approach is: do one thing that works before doing five
things that don't.

---

## Timeline

| Phase | Days | Calendar (starting Mar 3) | Gate |
|-------|------|--------------------------|------|
| 0 | 1 | Mar 3 | /proof endpoint live, confabulation check rejects current findings |
| 1 | 4 | Mar 4-7 | IronClaw calls github_fetch, result matches gateway audit |
| 2 | 7 | Mar 8-14 | IronClaw creates PR that passes CI |
| 3 | 6 | Mar 15-20 | MEMORY.md non-empty, CI pass rate improving |
| 4 | 7 | Mar 21-27 | 3+ verified skills, skill reuse observed |
| 5 | 7 | Mar 28-Apr 3 | Verified cross-agent handoff with evidence chain |
| 6 | 8 | Apr 4-11 | Self-improvement PR that measurably improves performance |

**Total: 40 days (8 weeks)**

This is longer than the original 6-week EPIC because it does less but does it for real.

---

## Hard Rules

1. **No campaign is "SUCCESS" with 0 tool calls and > 0 findings.**
   If an agent claims to find something without calling a tool to verify, the result
   is automatically marked CONFABULATED.

2. **No new campaigns until existing ones produce verified results.**
   We have 53 campaigns. We need 1 that works. Adding more is debt, not progress.

3. **Metrics are measured by the system, not reported by the agent.**
   Gateway audit log, CI checks API, GitHub PR state, Aperture billing -- these are
   ground truth. Agent self-reported "tool_calls: 5" is not.

4. **Each phase has exactly one success criterion.**
   Not a scorecard with 15 metrics. One thing that either happened or didn't.

5. **NoGo means PIVOT, not "try harder."**
   If Phase 1 fails, we don't spend 3 more weeks on OpenClaw. We replace the engine.

---

## What This Sprint Is NOT

- **Not 53 campaigns running on schedule.** We disabled all but 1.
- **Not a dashboard that shows green.** We deployed a `/proof` endpoint that shows red.
- **Not "agents filing issues."** We want agents that fix issues.
- **Not multi-agent orchestration.** We want one agent that works.
- **Not a demonstration.** This repo IS the lab. The agents are the researchers.
  Give them a workstation and a task, not a script and a stage.

---

## Appendix: Research Foundation

### SICA (Self-Improving Coding Agent)
- 17% to 53% SWE-bench through 15 self-improvement iterations
- Key: tight edit-test-fix loop, not broad campaign coverage
- Source: Ye et al. 2024, verified by multiple reproduction studies

### Reflexion (Shinn et al. 2023)
- Verbal self-critique stored in episodic memory buffer
- Agent re-reads failures before next attempt
- Key: memory must be structured and actionable, not generic

### AlphaEvolve (Google DeepMind, 2025)
- Evolutionary code optimization with automated verification
- 0.7% of Google's compute saved (production, not benchmark)
- Key: nothing accepted without automated verification passing

### Voyager (Wang et al. 2023)
- Minecraft agent builds skill library that compounds over time
- Skills are verified (3+ successful executions) before trusted
- Key: skills must be concrete procedures, not abstract capabilities

### Claude Code Architecture
- Single-threaded master loop: read -> edit -> test -> iterate
- No campaign scheduler, no multi-agent routing, no adapter sidecars
- Key: simplicity wins. Complex orchestration adds failure modes, not capability.

### The Nakajima Principle
- "Design RULES AND TESTS that govern what changes are allowed"
- Agent autonomy boundary = test coverage boundary
- No tests for a capability = no autonomy for that capability
