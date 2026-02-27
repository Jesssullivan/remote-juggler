# Agent Plane Epic: From Scaffolding to Substrate

## 6-Week Timeline: March 3 -- April 11, 2026

---

## Vision Statement

RemoteJuggler's agent plane is the substrate on which autonomous agents operate.
It is one gateway, one policy layer, one credential store -- and every agent,
regardless of its upstream lineage, plugs into the same set of MCP tools, the
same Aperture metering, the same campaign lifecycle. The architecture parallels
bcachefs: do it once, do it right, let the unified abstraction compound over
time. Three agents (IronClaw, HexStrike, TinyClaw) share 53 gateway tools, a
common findings format, and a single feedback loop from campaign execution to
GitHub Issues to merged improvements. The agent plane is not a future ambition.
The infrastructure is 90% deployed. This epic closes the last 10% -- the part
where agents actually invoke tools, talk to each other, and improve themselves.

---

## Current Baseline (Week 0 -- February 27, 2026)

Hard numbers from the system audit:

| Metric | Value | Notes |
|--------|-------|-------|
| Total campaigns defined | 48 | 47 enabled, 1 manual-only (`xa-provision-agent`) |
| Campaigns with verified tool calls | ~5 | `cc-gateway-health`, `hs-cred-exposure`, `oc-identity-audit`, `xa-platform-health`, `pc-identity-audit` |
| Campaigns never executed | ~30 | All `lastRun: null` in index.json |
| IronClaw tool invocations | 0 | rj-tool wrapper exists but no evidence of use |
| HexStrike tool invocations | Errors | All 42 OCaml MCP tools return errors; adapter CrashLoopBackOff noted |
| TinyClaw (PicoClaw) tool invocations | Untracked | Closest to working; dispatch endpoint verified |
| Inter-agent conversations | 0 | No cross-agent protocol exists yet |
| Self-modifications (agent-authored PRs merged) | 0 | `oc-self-evolve` and `pc-self-evolve` campaigns defined but never run |
| Findings backed by tool data | ~4 | Handful of E2E-verified campaign results |
| Aperture metering accuracy | Degraded | SSE webhook connected but S3 ingestion incomplete |
| GitHub Discussions with agent comments | 0 of ~145 | Discussions exist as stubs with no content |
| Gateway tools available | 53 | 17 gateway-native + 36 Chapel |
| HexStrike native tools | 42 | OCaml MCP server, F*-verified, all currently broken |
| Agent memory files populated | 0/3 | All three MEMORY.md files have empty Observations sections |

### Agent Readiness Summary

| Agent | Pod Status | Tools Connected | Campaign Dispatch | Findings Produced |
|-------|-----------|----------------|-------------------|-------------------|
| IronClaw (OpenClaw) | 3/3 Running | Via rj-tool wrapper (untested) | Adapter localhost:8080 | None verified |
| HexStrike-AI | 2/2 Running | 42 native (broken) + adapter proxy | K8s Service | Errors only |
| TinyClaw (PicoClaw) | 2/2 Running | Adapter tool proxy | POST /api/dispatch | Closest to working |
| Gateway-Direct | N/A (tools only) | 53 MCP tools | Direct execution | cc-gateway-health passes |

---

## Phase Map

| Phase | Weeks | Theme | Inflection Point |
|-------|-------|-------|-----------------|
| 1 | 1--2 | Tool Invocation | First campaign where every agent produces findings backed by real tool output |
| 2 | 3--4 | Agent Communication | First inter-agent conversation: one agent's finding triggers another agent's campaign |
| 3 | 5--6 | Self-Evolution | First agent-authored campaign improvement merged to main |

---

## Week-by-Week Milestones

### Week 1 (March 3--7): Fix What Is Broken

Fix the three critical blockers: HexStrike adapter CrashLoopBackOff, IronClaw's
never-exercised rj-tool wrapper, and TinyClaw's untracked tool calls. By Friday,
all three agents can execute a single-tool smoke test (`juggler_status`) via
their respective dispatch mechanisms and return structured output. The gateway
health campaign (`cc-gateway-health`) runs on schedule and its result is
verifiable in Setec.

**Done when**: `juggler_status` returns valid JSON from all three agents in a
single orchestrated test.

### Week 2 (March 10--14): Campaigns Produce Real Findings

Expand from smoke tests to real campaigns. Prioritize the 18 campaigns that have
the most complete definitions: `hs-cred-exposure`, `oc-dep-audit`,
`oc-identity-audit`, `pc-identity-audit`, `oc-docs-freshness`, and their
dependencies. Fix tool errors as they surface. Implement per-campaign result
validation: findings must contain tool call evidence (not LLM confabulation).

**Done when**: 15+ campaigns have run at least once with tool-backed findings
stored in Setec.

### Week 3 (March 17--21): Cross-Agent Protocol

Design and implement the cross-agent dispatch protocol. An HexStrike credential
finding should automatically trigger an IronClaw remediation campaign. Define the
message format, routing rules, and the `xa-*` campaign wiring. Implement the
Discussion publishing pipeline so findings are visible without SSH access.

**Done when**: A finding from `hs-cred-exposure` triggers `oc-secret-request` or
`oc-credential-health` without human intervention.

### Week 4 (March 24--28): Feedback Loop Closes

The feedback loop from campaign execution to GitHub Issue to human review to
merged fix is operational. Agents create Issues with structured findings,
reference specific files and lines, and propose fixes. The campaign runner's
FeedbackHandler creates and closes Issues based on resolved/unresolved findings.
Discussions are populated with sanitized campaign summaries.

**Done when**: 10+ GitHub Issues created by agents, at least 3 closed as
resolved, and Discussions have substantive content.

### Week 5 (March 31--April 4): Self-Evolution Begins

Enable `oc-self-evolve` and `pc-self-evolve` campaigns. Agents analyze their own
campaign definitions, identify improvements (better tool selection, tighter
guardrails, more specific KPIs), and submit PRs via `github_create_pr`. The
`oc-prompt-audit` campaign reviews campaign quality and feeds back into the
evolution cycle.

**Done when**: At least 2 agent-authored PRs submitted that modify campaign
definitions or workspace files.

### Week 6 (April 7--11): Production Hardening

Harden the system for unsupervised operation. Fix Aperture metering accuracy.
Implement budget enforcement (token limits per campaign are real, not advisory).
Validate the kill switch. Run the full 47-campaign suite on schedule for 5
consecutive days without manual intervention. Document operational runbook.

**Done when**: All Definition of Done criteria below are met.

---

## Inflection Points (Go/No-Go Gates)

### Gate 1: End of Week 2 -- Are Tools Actually Being Invoked?

**Question**: Can all three agents execute MCP tools and produce structured
findings?

**Go criteria**:
- All three agents return valid `juggler_status` output
- 15+ campaigns have produced tool-backed findings
- Zero campaigns produce findings without corresponding tool call evidence
- HexStrike adapter is stable (no CrashLoopBackOff in 48 hours)

**No-Go actions**: If IronClaw or HexStrike cannot invoke tools, descope to
TinyClaw + Gateway-Direct only for Phases 2--3. File architecture issues for the
broken agents.

### Gate 2: End of Week 4 -- Are Agents Actually Communicating?

**Question**: Can one agent's output trigger another agent's work?

**Go criteria**:
- At least 1 cross-agent campaign chain has executed end-to-end
- Discussions contain agent-authored content (not just stubs)
- FeedbackHandler has created and closed at least 3 Issues
- Aperture metering reflects actual token usage per agent per campaign

**No-Go actions**: If cross-agent dispatch fails, fall back to human-triggered
dependency chains. Proceed with self-evolution on whichever agents work
independently.

### Gate 3: End of Week 6 -- Has The System Improved Itself?

**Question**: Have agents authored improvements that were merged?

**Go criteria**:
- At least 1 agent-authored PR merged to main
- Campaign definitions have been improved by agent feedback
- System has run 5 consecutive days without manual intervention
- No confabulated findings in the last 72 hours

**No-Go actions**: If self-evolution PRs are low quality, add human-in-the-loop
approval gates and extend the timeline by 2 weeks.

---

## Definition of Done (Week 6)

The agent plane is live when ALL of the following are true:

| Criterion | Target | Measurement |
|-----------|--------|-------------|
| Campaigns running with real tool results | 35+ of 47 | Setec result entries with tool call traces |
| Campaigns that have never run | < 5 | `lastRun: null` count in index.json |
| Inter-agent campaign chains completed | 5+ | Cross-agent trigger logs in campaign runner |
| Agent-authored PRs merged | 3+ | PRs with `rj-agent-bot` as author |
| Findings backed by tool data | 50+ | Setec findings with tool invocation evidence |
| LLM confabulations in findings | 0 | Validated by tool-call-required guard |
| Aperture metering accuracy | Within 5% of actual | Reconcile S3 exports vs. Setec aggregates |
| GitHub Discussions with content | 20+ | Non-empty Discussion posts from agent publishing |
| GitHub Issues from agents | 15+ | Issues with `agent-finding` label |
| Consecutive days without intervention | 5 | Campaign runner uptime log |
| Kill switch tested | Yes | Global halt exercised and recovered |
| Budget enforcement tested | Yes | At least 1 campaign halted by token limit |
| Agent memory files populated | 3/3 | Non-empty Observations in all MEMORY.md files |

---

## Architecture Principles

These principles govern every decision in this epic. They are drawn from the
bcachefs parallel: a filesystem that took years longer than alternatives but
delivered a fundamentally better abstraction.

### 1. Correctness Over Speed

bcachefs chose copy-on-write and checksums when ext4 chose speed. We choose
identity verification and policy enforcement over raw throughput. Every tool call
is attributed, every finding is validated against tool output, every credential
access is audited. If an agent produces a finding without tool evidence, the
finding is rejected -- even if it happens to be correct.

### 2. Unified Abstraction

bcachefs is one filesystem that replaces the LVM + ext4 + mdraid stack. The
agent plane is one gateway that all agents use, one policy engine (Aperture) that
all LLM calls traverse, one credential store (Setec) that all secrets flow
through. There is no "HexStrike-only" tool path or "IronClaw-only" credential
source. The adapter sidecar pattern ensures every agent, regardless of its native
API format, speaks the same MCP protocol.

### 3. Verification as Culture

F*-verified tools in HexStrike are not a gimmick -- they are the standard the
entire plane aspires to. Every campaign has guardrails (`maxDuration`, `readOnly`,
`killSwitch`, `aiApiBudget`). Every finding format is validated. Every PR from an
agent must pass the same CI that human PRs pass. Trust is earned through
verification, not granted by default.

### 4. Self-Healing

The agent plane does not just detect problems -- it fixes them. A credential
exposure finding triggers a rotation campaign. A stale dependency finding
triggers an update PR. A broken tool finding triggers a diagnostic campaign. The
feedback loop is the fundamental unit of value: observe, report, fix, verify.

---

## Risk Register (Known at Planning)

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| HexStrike OCaml MCP server fundamentally broken | Medium | High | Week 1 diagnosis; fallback to adapter-only tools |
| IronClaw rj-tool wrapper has undiscovered bugs | High | Medium | Dedicated smoke test suite in Week 1 |
| Civo storage quota (48/50 Gi) blocks PVC creation | Medium | High | Reclaim llama-models PVC (10Gi unused) |
| Aperture S3 ingestion never reconciles accurately | Medium | Medium | Add reconciliation job; accept webhook-only data |
| Agent confabulations pass as findings | High | High | Tool-call-required validation gate |
| Campaign token budgets too low for real work | Medium | Low | Calibrate budgets from Week 1--2 actual usage |
| Cross-agent dispatch creates infinite loops | Low | Critical | Depth limit on dependency chains; kill switch |
| Nix-based HexStrike container has no shell for debugging | Medium | Low | busybox sidecar for debug; already in place |

---

## File Index

All detailed plans and tracking documents for this epic:

| File | Purpose |
|------|---------|
| [`EPIC.md`](EPIC.md) | This file -- master overview and architecture |
| [`progress.md`](progress.md) | Live progress tracker, scorecard, daily log |
| [`week1-2_tool_invocation.md`](week1-2_tool_invocation.md) | Phase 1: Close the tool invocation loop |
| [`week3-4_agent_communications.md`](week3-4_agent_communications.md) | Phase 2: Agent communication platform |
| [`week5-6_self_evolution.md`](week5-6_self_evolution.md) | Phase 3: Self-evolution and production hardening |

### Related Documentation

| File | Purpose |
|------|---------|
| [`docs/agents/index.md`](../docs/agents/index.md) | Agent ecosystem overview |
| [`docs/agents/campaigns.md`](../docs/agents/campaigns.md) | Campaign schema and execution flow |
| [`docs/agents/aperture.md`](../docs/agents/aperture.md) | Aperture LLM proxy integration |
| [`test/campaigns/index.json`](../test/campaigns/index.json) | Campaign registry (48 campaigns) |

### Agent Workspace Files

| Agent | Key Files |
|-------|-----------|
| IronClaw | [`deploy/fork-dockerfiles/ironclaw/workspace/`](../deploy/fork-dockerfiles/ironclaw/workspace/) -- SOUL.md, TOOLS.md, IDENTITY.md, skills/ |
| HexStrike | [`deploy/fork-dockerfiles/hexstrike-ai/workspace/`](../deploy/fork-dockerfiles/hexstrike-ai/workspace/) -- AGENT.md, TOOLS.md, SOUL.md |
| TinyClaw | [`deploy/fork-dockerfiles/picoclaw/workspace/`](../deploy/fork-dockerfiles/picoclaw/workspace/) -- AGENT.md, TOOLS.md, SOUL.md, skills/ |
