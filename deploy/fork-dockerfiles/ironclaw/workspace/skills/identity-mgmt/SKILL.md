---
name: identity-mgmt
description: RemoteJuggler identity management tools for querying, switching, and validating identities
version: "2.0"
tags: [identity, ssh, gpg, credentials, validation]
---

# Identity Management

Use this skill to query and manage git identities via the RemoteJuggler Chapel tools exposed through rj-gateway.

**All tools are accessed via exec and the rj-tool wrapper:**

```bash
exec("/workspace/bin/rj-tool <tool_name> [key=value ...]")
```

## Identity Query
- `exec("/workspace/bin/rj-tool juggler_status")` returns current identity context, auth status, and active configuration
- `exec("/workspace/bin/rj-tool juggler_list_identities provider=all")` lists all configured identities across providers
- `exec("/workspace/bin/rj-tool juggler_config_show")` displays the full RemoteJuggler configuration

## Identity Validation
- `exec("/workspace/bin/rj-tool juggler_validate identity=rj-agent-bot")` tests SSH + credential connectivity for a given identity
- Validates: SSH key exists, SSH auth succeeds, token is valid, GPG signing available

## Token Management
- `exec("/workspace/bin/rj-tool juggler_token_verify")` verifies the current token's validity and scopes
- `exec("/workspace/bin/rj-tool juggler_token_get")` retrieves the active token for the current identity

## GPG & Signing
- `exec("/workspace/bin/rj-tool juggler_gpg_status")` checks GPG/SSH signing readiness
- Reports: signing key availability, trust level, expiry status

## Bot Identity Details
- Identity name: `rj-agent-bot`
- GitHub App ID: 2945224
- SSH key path: `/home/agent/.ssh/id_ed25519`
- Email: `2945224+rj-agent-bot[bot]@users.noreply.github.com`

## Identity Drift Detection
When running identity audits:
1. Query current identity with `exec("/workspace/bin/rj-tool juggler_status")`
2. Compare against expected state in IDENTITY.md
3. Flag any discrepancies: wrong key, expired token, missing GPG
4. Log findings for the identity audit campaign
