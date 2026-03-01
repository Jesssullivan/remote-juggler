# WS3: A2A Discussion Tools — Progress

**Started**: 2026-02-28
**Status**: IN PROGRESS
**Effort**: 5-7 days estimated

## Scorecard

| Metric | Before | Target | Current |
|--------|--------|--------|---------|
| Discussion tools implemented | 0 | 5 | 5 |
| Discussion tool tests | 0 | 12 | 12 |
| FindingRouter rules | 0 | 5 | 5 |
| Cross-agent handoffs | 0 | 1+ | 0 (labels ready, pending deploy) |
| Gateway tests passing | 90 | All | 143 (90 + 12 discussion + subtests) |
| Router tests passing | N/A | All | 109 (86 + 14 router + 9 PR) |

## Phase Checklist

- [x] Gateway Discussion read tools (list, get, search) + tests
- [x] Gateway Discussion write tools (reply, label) + tests
- [x] FindingRouter implementation + tests (5 rules, 14 tests)
- [x] Scheduler/publisher integration (router field + storeResult hook + formatBody rj-meta)
- [x] Campaign definitions (xa-finding-handoff, hs-handoff-response, oc-handoff-response)
- [x] Create routing labels on repo (15 labels: handoff:*, state:*, severity:*, agent:*)
- [ ] Deploy and integration test

## New Files

| File | Purpose |
|------|---------|
| `gateway/github_discussion_tools.go` | 5 tool handlers + GraphQL |
| `gateway/github_discussion_tools_test.go` | Unit tests |
| `test/campaigns/runner/router.go` | FindingRouter |
| `test/campaigns/runner/router_test.go` | Router tests |
| `test/campaigns/cross-agent/xa-finding-handoff.json` | Handoff campaign |
| `test/campaigns/hexstrike/hs-handoff-response.json` | HexStrike response |
| `test/campaigns/openclaw/oc-handoff-response.json` | IronClaw response |

## Daily Log

### 2026-02-28
- Created progress file
- Spec exists in week3-4_agent_communications.md (756 lines)
- Gateway tools: Implemented 5 Discussion tools using GraphQL API (doGraphQL helper)
  - DiscussionList: list with optional category filter
  - DiscussionGet: single discussion with comments
  - DiscussionSearch: via GraphQL search API with repo scoping
  - DiscussionReply: two-step (get node ID → addDiscussionComment mutation)
  - DiscussionLabel: three-step (lookup IDs → map names → addLabelsToLabelable mutation)
- Registered tools in mcp_proxy.go (gatewayToolNames + switch dispatch + audit logging)
- Added 5 tool definitions in tools.go with full inputSchema
- Tests: 12 passing (happy path + validation + error handling + GraphQL errors)
- Updated tools_test.go counts (18→23 gateway tools)
- All 143 gateway tests passing
- FindingRouter: 5 default rules (Security→HexStrike, Credentials→HexStrike, CodeQuality→IronClaw, Dependencies→IronClaw, UpstreamDrift→TinyClaw)
- RJMeta protocol: FormatRJMeta() + ParseRJMeta() with version "1" HTML comment blocks
- Router tests: 14 tests covering severity matching, label matching, campaign prefix, source agent, first-rule-wins, no-match, meta population, fingerprinting, multi-finding
- Scheduler integration: Added router field + storeResult hook (routes findings after Discussion publish)
- Publisher integration: formatBody appends rj-meta block to every Discussion
- Campaign definitions: 3 new (xa-finding-handoff, hs-handoff-response, oc-handoff-response)
- Updated index.json (50→53 campaigns)
- All 109 campaign runner tests passing
