# Campaigns

Campaigns are declarative JSON definitions that describe automated tasks for agents to execute. Each campaign specifies triggers, tools, process steps, guardrails, and success metrics.

## Active Campaigns

### Maintenance & Monitoring

| Campaign | Agent | Schedule | Budget | Description |
|----------|-------|----------|--------|-------------|
| `cc-gateway-health` | gateway-direct | Hourly | 5K tokens | Verifies all 43 MCP tools respond correctly |
| `cc-mcp-regression` | gateway-direct | On push | 5K tokens | Regression tests for tool schemas and behavior |
| `oc-gateway-smoketest` | openclaw | Manual | 10K tokens | Quick 3-tool validation of gateway health |
| `oc-dep-audit` | openclaw | Weekly Mon 2am | 100K tokens | Cross-repo dependency version divergence analysis |
| `xa-audit-completeness` | cross-agent | Dependent | 20K tokens | Validates audit coverage across all tool calls |

### Security

| Campaign | Agent | Schedule | Budget | Description |
|----------|-------|----------|--------|-------------|
| `hs-cred-exposure` | hexstrike | Weekly Sun 1am | 80K tokens | Credential exposure scan across 10 repos |
| `hs-cve-monitor` | hexstrike | Daily 3am | 60K tokens | CVE monitoring against dependency manifests |
| `hs-dep-vuln` | hexstrike | Dependent on dep-audit | 80K tokens | Deep vulnerability analysis of flagged dependencies |

### Analysis & Reporting

| Campaign | Agent | Schedule | Budget | Description |
|----------|-------|----------|--------|-------------|
| `oc-weekly-digest` | openclaw | Weekly Mon 6am | 30K tokens | Aggregated weekly ecosystem summary |
| `oc-issue-triage` | openclaw | Daily 8am | 40K tokens | Classify and triage untriaged issues |
| `oc-prompt-audit` | openclaw | Monthly 1st | 50K tokens | Self-improvement: analyze campaign quality |
| `oc-docs-freshness` | openclaw | Weekly | 40K tokens | Documentation staleness across 11 repos |
| `oc-coverage-gaps` | openclaw | Weekly | 40K tokens | Test coverage gap analysis |
| `oc-license-scan` | openclaw | Weekly | 30K tokens | License compliance for OSS dependencies |

## Campaign Definition Schema

Campaign definitions are JSON files in `test/campaigns/`. Key fields:

```json
{
  "id": "campaign-id",
  "name": "Human-Readable Name",
  "agent": "openclaw|hexstrike|gateway-direct",
  "trigger": {
    "schedule": "0 6 * * 1",
    "event": "manual|push|pull_request|dependent",
    "dependsOn": ["other-campaign-id"],
    "pathFilters": ["gateway/*.go"]
  },
  "tools": ["juggler_campaign_status", "github_fetch"],
  "process": ["Step 1: ...", "Step 2: ..."],
  "guardrails": {
    "maxDuration": "15m",
    "readOnly": true,
    "killSwitch": "remotejuggler/campaigns/global-kill",
    "aiApiBudget": { "maxTokens": 30000 }
  },
  "feedback": {
    "createIssues": true,
    "closeResolvedIssues": true
  },
  "metrics": {
    "kpis": ["repos_scanned", "findings_count"]
  }
}
```

See `test/campaigns/schema.json` for the full JSON Schema definition.

## Execution Flow

1. **Campaign Runner** evaluates triggers every 60 seconds
2. Due campaigns are dispatched to the appropriate agent
3. Agent executes process steps using MCP tools through rj-gateway
4. Results are stored in Setec (latest + timestamped history)
5. Feedback handler creates/closes GitHub issues for findings
6. Publisher posts sanitized results to GitHub Discussions
7. README agent status table is updated via repository_dispatch
