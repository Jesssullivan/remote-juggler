# Agent Web Access

## Interaction Modes

| Mode | Access Method | Use Case |
|------|--------------|----------|
| **Browser (human)** | Tailscale client + HTTPS | Interactive WebChat (IronClaw), Dashboard (HexStrike) |
| **Claude Code (MCP)** | `rj-gateway` MCP config | 43 tools via `/mcp`, campaign trigger via `juggler_campaign_status` |
| **API (programmatic)** | `POST /campaign`, `POST /webhook` | Campaign runner endpoints on :8081, webhook HMAC validation |

## Per-Agent Tailnet Services

| Agent | Tailnet Hostname | Port | Exposed UI |
|-------|-----------------|------|-----------|
| IronClaw | `ironclaw.taila4c78d.ts.net` | 18789 | Built-in WebChat (full interactive) |
| PicoClaw | — | — | Health endpoint only (no UI) |
| HexStrike-AI | `hexstrike.taila4c78d.ts.net` | 8888 | Flask live dashboard |

All exposed via Tailscale Operator Service annotations (`tailscale.com/expose`, `tailscale.com/hostname`).

## Unified Portal

**URL:** `https://rj-gateway.taila4c78d.ts.net/portal`

The gateway hosts a single-page portal aggregating:

- Agent health status (polls each adapter `/health`)
- Campaign results (from Setec via `handleCampaignStatusTool`)
- Recent audit log (reuses `audit.go`)
- Aperture usage metrics (reuses `metering.go`)
- Links to each agent's tailnet URL

## ACL Grants

| Role | Portal | IronClaw WebChat | Campaign Trigger | Secret Access |
|------|--------|-----------------|-----------------|--------------|
| `autogroup:member` | View | View | No | No |
| `group:dollhouse-admins` | View | Interact | All | Read/Write |
| `tag:ci-agent` | No | No | Webhook | Read |

## Architecture

```
Browser/Claude Code
        |
        v
  rj-gateway (tsnet TLS :443 + in-cluster HTTP :8080)
        |
        +-- /portal         → Go embed HTML dashboard
        +-- /portal/api     → JSON data for portal widgets
        +-- /mcp            → MCP JSON-RPC (43 tools)
        +-- /resolve        → Composite secret resolution
        |
        +-- IronClaw pod (ironclaw + adapter + campaign-runner)
        |     adapter :8080 → ironclaw :18789 (WebSocket/HTTP chat)
        |
        +-- TinyClaw pod (tinyclaw + adapter)
        |     adapter :8080 → tinyclaw :18790 (dispatch API)
        |
        +-- HexStrike-AI pod (hexstrike-ai + adapter)
              adapter :8080 → hexstrike-ai :8888 (FastMCP)
```

Each real agent runs in its own pod with an **adapter sidecar** that translates
the campaign runner's dispatch protocol (`POST /campaign`, `GET /status`,
`GET /health`) into the agent's native API format.
