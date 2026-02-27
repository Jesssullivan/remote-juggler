# Agent Plane Progress Tracker

## Current Phase: 1 -- Tool Invocation
## Current Week: 0 (Planning)
## Last Updated: 2026-02-27

---

## Scorecard

| Metric | Baseline (W0) | Week 2 Target | Week 4 Target | Week 6 Target | Current | Status |
|--------|---------------|---------------|---------------|---------------|---------|--------|
| Campaigns with tool-backed results | ~5 | 15+ | 25+ | 35+ | ~5 | Not Started |
| Campaigns never executed | ~30 | < 20 | < 10 | < 5 | ~30 | Not Started |
| IronClaw tool calls (verified) | 0 | 5+ | 15+ | 20+ | 0 | Not Started |
| HexStrike tools working | 0/42 | 10/42 | 19/42 | 19/42 | 0/42 | Not Started |
| TinyClaw tool calls (verified) | untracked | 5+ | 15+ | 20+ | untracked | Not Started |
| Inter-agent campaign chains | 0 | 0 | 5+ | 5+ | 0 | Not Started |
| Agent-authored PRs merged | 0 | 0 | 0 | 3+ | 0 | Not Started |
| Findings with tool data | ~4 | 20+ | 35+ | 50+ | ~4 | Not Started |
| LLM confabulations caught | -- | 0 allowed | 0 allowed | 0 allowed | unmeasured | Not Started |
| Aperture metering accuracy | degraded | measured | within 10% | within 5% | degraded | Not Started |
| GitHub Discussions with content | 0 | 0 | 10+ | 20+ | 0 | Not Started |
| GitHub Issues from agents | 0 | 5+ | 10+ | 15+ | 0 | Not Started |
| Agent memory files populated | 0/3 | 2/3 | 3/3 | 3/3 | 0/3 | Not Started |
| Consecutive days without intervention | 0 | 2 | 3 | 5 | 0 | Not Started |

### Status Legend

| Symbol | Meaning |
|--------|---------|
| Not Started | Work has not begun |
| In Progress | Actively being worked |
| At Risk | Behind schedule or blocked |
| On Track | Progressing as planned |
| Done | Target met |

---

## Phase Checklist

### Phase 1: Tool Invocation (Weeks 1--2)

- [ ] HexStrike adapter stabilized (no CrashLoopBackOff for 48h)
- [ ] HexStrike OCaml MCP server diagnosed -- working or descoped
- [ ] IronClaw rj-tool wrapper smoke tested with 5+ tools
- [ ] TinyClaw dispatch endpoint verified end-to-end
- [ ] All three agents return valid `juggler_status` output
- [ ] Per-campaign result validation implemented (tool-call-required guard)
- [ ] 15+ campaigns have run at least once with real findings
- [ ] Aperture metering baseline measured
- [ ] Gate 1 review completed

### Phase 2: Agent Communication (Weeks 3--4)

- [ ] Cross-agent dispatch protocol designed
- [ ] `xa-*` campaign dependency wiring implemented
- [ ] At least 1 cross-agent chain executed end-to-end
- [ ] FeedbackHandler creating Issues from findings
- [ ] FeedbackHandler closing Issues for resolved findings
- [ ] Discussion publishing pipeline operational
- [ ] 10+ GitHub Issues created by agents
- [ ] Aperture metering reconciliation running
- [ ] Gate 2 review completed

### Phase 3: Self-Evolution (Weeks 5--6)

- [ ] `oc-self-evolve` campaign producing actionable suggestions
- [ ] `pc-self-evolve` campaign producing actionable suggestions
- [ ] `oc-prompt-audit` reviewing campaign quality
- [ ] At least 2 agent-authored PRs submitted
- [ ] At least 1 agent-authored PR merged
- [ ] Budget enforcement tested (campaign halted by token limit)
- [ ] Kill switch tested (global halt and recovery)
- [ ] 5 consecutive days without manual intervention
- [ ] Gate 3 review completed

---

## Campaign Execution Tracker

Track which campaigns have produced real results. Updated as campaigns run.

### Gateway-Direct (5 campaigns)

| Campaign | First Run | Last Run | Findings | Tool Calls | Status |
|----------|-----------|----------|----------|------------|--------|
| `cc-gateway-health` | verified | recent | -- | health check | Working |
| `cc-mcp-regression` | never | -- | -- | -- | Not Started |
| `cc-identity-switch` | never | -- | -- | -- | Not Started |
| `cc-config-sync` | never | -- | -- | -- | Not Started |
| `cc-cred-resolution` | never | -- | -- | -- | Not Started |

### IronClaw / OpenClaw (22 campaigns)

| Campaign | First Run | Last Run | Findings | Tool Calls | Status |
|----------|-----------|----------|----------|------------|--------|
| `oc-identity-audit` | verified | recent | issue #86 | yes | Working |
| `oc-gateway-smoketest` | never | -- | -- | -- | Not Started |
| `oc-dep-audit` | never | -- | -- | -- | Not Started |
| `oc-coverage-gaps` | never | -- | -- | -- | Not Started |
| `oc-docs-freshness` | never | -- | -- | -- | Not Started |
| `oc-license-scan` | never | -- | -- | -- | Not Started |
| `oc-dead-code` | never | -- | -- | -- | Not Started |
| `oc-ts-strict` | never | -- | -- | -- | Not Started |
| `oc-a11y-check` | never | -- | -- | -- | Not Started |
| `oc-weekly-digest` | never | -- | -- | -- | Not Started |
| `oc-issue-triage` | never | -- | -- | -- | Not Started |
| `oc-prompt-audit` | never | -- | -- | -- | Not Started |
| `oc-codeql-fix` | never | -- | -- | -- | Not Started |
| `oc-wiki-update` | never | -- | -- | -- | Not Started |
| `oc-upstream-sync` | never | -- | -- | -- | Not Started |
| `oc-self-evolve` | never | -- | -- | -- | Not Started |
| `oc-fork-review` | never | -- | -- | -- | Not Started |
| `oc-credential-health` | never | -- | -- | -- | Not Started |
| `oc-secret-request` | never | -- | -- | -- | Not Started |
| `oc-token-budget` | never | -- | -- | -- | Not Started |
| `oc-ts-package-audit` | never | -- | -- | -- | Not Started |
| `oc-infra-review` | never | -- | -- | -- | Not Started |

### HexStrike (7 campaigns)

| Campaign | First Run | Last Run | Findings | Tool Calls | Status |
|----------|-----------|----------|----------|------------|--------|
| `hs-cred-exposure` | verified | recent | issue #83 | yes | Working |
| `hs-cve-monitor` | never | -- | -- | -- | Not Started |
| `hs-dep-vuln` | never | -- | -- | -- | Not Started |
| `hs-network-posture` | never | -- | -- | -- | Not Started |
| `hs-gateway-pentest` | never | -- | -- | -- | Not Started |
| `hs-sops-rotation` | never | -- | -- | -- | Not Started |
| `hs-container-vuln` | never | -- | -- | -- | Not Started |

### TinyClaw / PicoClaw (5 campaigns)

| Campaign | First Run | Last Run | Findings | Tool Calls | Status |
|----------|-----------|----------|----------|------------|--------|
| `pc-identity-audit` | verified | recent | issue #90 | yes | Working |
| `pc-credential-health` | never | -- | -- | -- | Not Started |
| `pc-self-evolve` | never | -- | -- | -- | Not Started |
| `pc-ts-package-scan` | never | -- | -- | -- | Not Started |
| `pc-upstream-sync` | never | -- | -- | -- | Not Started |

### Cross-Agent (9 campaigns)

| Campaign | First Run | Last Run | Findings | Tool Calls | Status |
|----------|-----------|----------|----------|------------|--------|
| `xa-platform-health` | verified | recent | issue #85 | yes | Working |
| `xa-identity-audit` | never | -- | -- | -- | Not Started |
| `xa-audit-completeness` | never | -- | -- | -- | Not Started |
| `xa-cred-lifecycle` | never | -- | -- | -- | Not Started |
| `xa-acl-enforcement` | never | -- | -- | -- | Not Started |
| `xa-fork-health` | never | -- | -- | -- | Not Started |
| `xa-token-budget` | never | -- | -- | -- | Not Started |
| `xa-upstream-drift` | never | -- | -- | -- | Not Started |
| `xa-provision-agent` | never | -- | -- | -- | Disabled |

---

## Daily Log

### 2026-02-27 (Week 0, Day 0)

**Focus**: Epic planning and audit synthesis
**Completed**: EPIC.md master overview, progress.md tracker, baseline measurements
**Blocked**: Nothing yet -- planning phase
**Metrics changed**: Baseline established (see scorecard)
**Next**: Begin Week 1 -- diagnose HexStrike adapter, smoke test IronClaw rj-tool

---

*Template for future entries:*

```
### YYYY-MM-DD (Week N, Day N)

**Focus**: [what was worked on]
**Completed**: [what got done]
**Blocked**: [what is stuck and why]
**Metrics changed**: [which scorecard numbers moved and to what]
**Next**: [tomorrow's plan]
```

---

## Decisions Log

Architectural and strategic decisions made during the epic.

| Date | Decision | Context | Alternatives Considered |
|------|----------|---------|------------------------|
| 2026-02-27 | 6-week phased approach (tools, comms, evolution) | Audit showed 90% infra / 10% function gap | Big-bang enablement (too risky), single-agent-first (too slow) |
| 2026-02-27 | Tool-call-required validation for all findings | Confabulation risk is highest-impact failure mode | Trust-but-verify (insufficient), manual review only (doesn't scale) |
| 2026-02-27 | Go/No-Go gates at weeks 2, 4, 6 | Avoid sunk-cost on broken agents | Fixed timeline with no gates (risky), continuous evaluation (no clear decision points) |

---

## Risk Register

Active risks tracked throughout the epic. Resolved risks move to the bottom.

### Active Risks

| ID | Risk | Likelihood | Impact | Mitigation | Owner | Status |
|----|------|-----------|--------|------------|-------|--------|
| R1 | HexStrike OCaml MCP server is fundamentally broken | Medium | High | Week 1 deep diagnosis; fallback to adapter-only (15 gateway tools) | -- | Open |
| R2 | IronClaw rj-tool wrapper has undiscovered serialization bugs | High | Medium | Dedicated smoke test matrix (all 53 tools) in Week 1 | -- | Open |
| R3 | Civo storage quota blocks new PVCs (48/50 Gi used) | Medium | High | Reclaim llama-models PVC (10Gi, unused) before Week 1 | -- | Open |
| R4 | Aperture S3 ingestion never reconciles with webhook data | Medium | Medium | Build reconciliation cronjob; accept webhook-only as fallback | -- | Open |
| R5 | Agents confabulate findings that look plausible | High | High | Tool-call-required validation gate; reject findings without traces | -- | Open |
| R6 | Campaign token budgets too low for real execution | Medium | Low | Measure actual usage in Weeks 1--2; adjust budgets before Week 3 | -- | Open |
| R7 | Cross-agent dispatch creates infinite dependency loops | Low | Critical | Max depth limit (3) on dependency chains; kill switch tested Week 1 | -- | Open |
| R8 | Pre-commit hook blocks agent bot Co-Authored-By lines | Low | Low | Agents commit as sole author via GitHub App; no co-author lines | -- | Open |

### Resolved Risks

| ID | Risk | Resolution | Date |
|----|------|-----------|------|
| -- | -- | -- | -- |

---

## Weekly Summary

Updated at the end of each week with a brief retrospective.

### Week 0 (Feb 27 -- Mar 2): Planning

- Epic plan created with 3 phases, 6 milestones, 3 go/no-go gates
- Baseline audit: 48 campaigns, ~5 working, ~30 never run
- All three agent memory files confirmed empty
- Detail plans assigned: tool invocation (W1-2), comms (W3-4), evolution (W5-6)

### Week 1 (Mar 3--7): [Pending]

### Week 2 (Mar 10--14): [Pending]

### Week 3 (Mar 17--21): [Pending]

### Week 4 (Mar 24--28): [Pending]

### Week 5 (Mar 31--Apr 4): [Pending]

### Week 6 (Apr 7--11): [Pending]
