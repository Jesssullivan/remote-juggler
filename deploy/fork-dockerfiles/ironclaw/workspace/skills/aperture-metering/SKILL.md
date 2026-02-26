---
name: aperture-metering
description: Aperture AI gateway awareness for token budgeting, model selection, and usage tracking
version: "2.0"
tags: [aperture, metering, tokens, budget, cost]
---

# Aperture Metering & Token Budgeting

Use this skill when you need to monitor token consumption, manage budgets, or make model selection decisions.

## Usage Queries (via rj-tool)

All tools are accessed via exec:
```bash
exec("/workspace/bin/rj-tool <tool_name> [key=value ...]")
```

- `exec("/workspace/bin/rj-tool juggler_aperture_usage")` returns aggregate usage across all agents
- `exec("/workspace/bin/rj-tool juggler_aperture_usage agent=ironclaw")` filters to this agent's usage
- `exec("/workspace/bin/rj-tool juggler_aperture_usage campaign_id=oc-dep-audit")` filters to a specific campaign

## Token Budget Awareness
- Your LLM calls route through Aperture (`ANTHROPIC_BASE_URL` points to Aperture)
- Aperture meters every request: input tokens, output tokens, model, timestamp
- Budget thresholds should be tracked in memory/MEMORY.md

## Model Selection Strategy
- **High-complexity tasks** (upstream sync, CodeQL fix, architecture review): use primary model (Sonnet)
- **Low-complexity tasks** (health checks, dead code scan, simple lookups): prefer Haiku via sub-agents
- **Budget exceeded**: switch all non-critical campaigns to Haiku
- Model failover is configured: Sonnet primary with Haiku fallback (survives rate limits)

## Usage Persistence
- Store snapshots: `exec("/workspace/bin/rj-tool juggler_setec_put name=agents/ironclaw/usage-snapshot value='{...}'")`
- Retrieve snapshots: `exec("/workspace/bin/rj-tool juggler_setec_get name=agents/ironclaw/usage-snapshot")`
- Compare current vs previous to detect consumption trends

## Reporting
- Include token usage in weekly digest (oc-weekly-digest campaign)
- Report: total tokens, per-campaign breakdown, cost estimate, model distribution
