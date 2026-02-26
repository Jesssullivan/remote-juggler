---
name: rj-platform
description: RemoteJuggler platform integration for credential resolution, secret management, and audit trails
version: "2.0"
tags: [credentials, secrets, audit, platform]
---

# RemoteJuggler Platform Integration

Use this skill when you need to interact with the RemoteJuggler credential and secret management platform.

**All tools are accessed via exec and the rj-tool wrapper** (MCP servers are not available directly):

```bash
exec("/workspace/bin/rj-tool <tool_name> [key=value ...]")
```

## Credential Resolution
- `exec("/workspace/bin/rj-tool juggler_resolve_composite query=<secret-name>")` resolves a secret from multiple backends (env, SOPS, KDBX, Setec) with configurable precedence
- The `query` parameter accepts secret names like `github-token`, `anthropic-api-key`, `brave-api-key`
- Sources are checked in precedence order; the first match wins

## Secret Store (Setec)
- `exec("/workspace/bin/rj-tool juggler_setec_list")` lists all available secrets
- `exec("/workspace/bin/rj-tool juggler_setec_get name=<bare-name>")` retrieves a secret value. Use bare names without `remotejuggler/` prefix
- `exec("/workspace/bin/rj-tool juggler_setec_put name=<bare-name> value=<value>")` stores a secret. Use bare names

## Audit Trail
- `exec("/workspace/bin/rj-tool juggler_audit_log count=20")` retrieves recent credential access entries
- Each entry shows: caller identity, action, query, source, and whether access was allowed
- Use this to verify credential access patterns and detect anomalies

## Campaign Status
- `exec("/workspace/bin/rj-tool juggler_campaign_status")` lists all campaign results
- `exec("/workspace/bin/rj-tool juggler_campaign_status campaign_id=oc-dep-audit")` gets results for a specific campaign

## Aperture Metering
- `exec("/workspace/bin/rj-tool juggler_aperture_usage")` shows LLM token usage across all agents
- `exec("/workspace/bin/rj-tool juggler_aperture_usage agent=ironclaw")` shows usage for a specific agent

## Key Gotchas
- **Always use exec()** with `/workspace/bin/rj-tool` — MCP tools are NOT available as native tools
- `juggler_resolve_composite` uses `query` parameter, NOT `name`
- `juggler_setec_put`/`get` use `name` parameter with bare names (client adds `remotejuggler/` prefix)
- All operations are audit-logged automatically
- String values with spaces: use quotes `title='My Title'`
