# Actual Agent Tool Call Chains (2026-03-02)

Where each agent's tool chain breaks, based on code audit and live observation.

---

## IronClaw (OpenClaw-based)

### Intended Chain
```
Campaign Runner                    Adapter Sidecar              OpenClaw Agent            rj-gateway
     |                                  |                           |                        |
     |-- POST /campaign --------------->|                           |                        |
     |                                  |-- POST /v1/responses ---->|                        |
     |                                  |   (prompt + exec tool)    |                        |
     |                                  |                           |-- exec("rj-tool ...") -|
     |                                  |                           |   [internal loop]       |-- MCP tools/call
     |                                  |                           |<- tool result ----------|
     |                                  |                           |-- more exec calls ----->|
     |                                  |<-- final message ---------|                        |
     |<-- poll /status (completed) -----|                           |                        |
```

### Actual Chain (observed)
```
Campaign Runner                    Adapter Sidecar              OpenClaw Agent
     |                                  |                           |
     |-- POST /campaign --------------->|                           |
     |                                  |-- POST /v1/responses ---->|
     |                                  |   (prompt + exec tool)    |
     |                                  |                           |-- [NO tool calls]
     |                                  |                           |-- generates text from training data
     |                                  |                           |-- confabulates findings with
     |                                  |                           |   specific file names and line numbers
     |                                  |<-- final message ---------|
     |<-- poll /status (completed) -----|
     |   tool_calls=0, findings=4       |
     |   status="success" (!)           |
```

### Where It Breaks

**Step**: OpenClaw deciding whether to call the `exec` tool
**Why**: The adapter provides the exec tool definition (ironclaw.go:196-214) and the
prompt says "Call tools via exec command" (ironclaw.go:145), but the campaign process
steps are vague ("Fetch the target file or component referenced in the finding"). The
LLM decides it can generate a plausible security analysis without calling tools. OpenClaw's
/v1/responses API supports tool calling, but the LLM simply doesn't choose to use it.

**Evidence**:
- Adapter logs show 1-2 min duration per dispatch (real LLM call, ~$0.10)
- Response has `tool_calls=0` in adapter status
- Findings contain specific file/line references that are confabulated
- Gateway audit log has 0 entries from the adapter

### Fix Path
1. Make tool usage MANDATORY in the prompt: "You MUST call exec to read the file BEFORE
   reporting any finding. Findings without a corresponding tool call will be rejected."
2. Create a proof-of-life campaign that requires returning actual file content
3. If OpenClaw still doesn't call tools, replace with simpler agent loop

---

## HexStrike-AI (OCaml MCP + Go gateway)

### Intended Chain
```
Campaign Runner                    Adapter Sidecar              HexStrike Gateway      OCaml MCP
     |                                  |                           |                     |
     |-- POST /campaign --------------->|                           |                     |
     |                                  |-- for each tool:          |                     |
     |                                  |   POST /mcp (tools/call)->|-- fwd to OCaml ---->|
     |                                  |                           |                     |-- F*-verified
     |                                  |                           |<- result -----------|
     |                                  |<- tool results ----------|                      |
     |<-- poll /status (completed) -----|                           |                     |
```

### Actual Chain (observed)
```
Campaign Runner                    Adapter Sidecar
     |                                  |
     |-- POST /campaign --------------->|
     |                                  |-- for each tool in campaign.Tools:
     |                                  |   if tool starts with "juggler_" or "github_":
     |                                  |     skip → "skipped (gateway tool)"
     |                                  |   else:
     |                                  |     POST /mcp → [OCaml tool runs, but irrelevant]
     |<-- poll /status (completed) -----|
     |   tool_calls=6, all "skipped"    |
     |   duration=0s, status="success"  |
```

### Where It Breaks

**Step**: hexstrike.go:82-89 — explicit skip of all `juggler_*` and `github_*` tools
**Why**: The adapter was designed to skip "gateway tools" and only call HexStrike's native
OCaml tools. But ALL campaign tools for handoff responses are gateway tools (github_fetch,
github_list_alerts, etc.). So it skips everything.

**Also**: No LLM is involved. The adapter iterates the tool list mechanically.
HexStrike has no agent loop — it's just a tool execution engine.

**Evidence**:
- All tool traces show "skipped (gateway tool)"
- Duration is milliseconds (no HTTP calls made)
- No LLM invocation anywhere in the code path

### Fix Path
1. Remove the skip filter OR route gateway tools through rj-gateway
2. Add an LLM to the dispatch chain (HexStrike currently has none)
3. Alternative: repurpose HexStrike as a tool-only service, not an agent

---

## TinyClaw (PicoClaw-based, Anthropic SDK)

### Intended Chain
```
Campaign Runner                    K8s Service                  TinyClaw Agent
     |                                  |                           |
     |-- health check GET /health ----->|                           |
     |   (gates dispatch)               |                           |
     |-- POST /campaign --------------->|                           |
     |                                  |-- POST /api/dispatch ---->|
     |                                  |   {content, session_key}  |
     |                                  |                           |-- Anthropic SDK call
     |                                  |                           |-- tool_use if configured
     |                                  |<-- {content, finish} -----|
     |<-- poll /status (completed) -----|                           |
```

### Actual Chain (observed)
```
Campaign Runner                    K8s Service
     |                                  |
     |-- health check GET /health ----->|  ← succeeds
     |                                  |
     |   ... no campaigns dispatched in 48h ...
```

### Where It Breaks

**Step 1**: Campaign scheduling — TinyClaw campaigns have daily/weekly schedules that
may not have aligned with the 36h observation window. However, `pc-credential-health`
fires daily at 07:00 UTC and `pc-self-evolve` at 03:00 UTC — both SHOULD have fired.

**Step 2**: tools_proxy.go exists in the TinyClaw codebase but `fetchGatewayTools()` is
never called. The dispatch endpoint accepts prompts but doesn't inject tool definitions.
TinyClaw can generate text but can't call MCP tools.

**Step 3**: No rj-tool equivalent. IronClaw has `/workspace/bin/rj-tool`. TinyClaw has
nothing to bridge to the gateway.

**Evidence**:
- Adapter logs: "adapter[tinyclaw]: listening on :8080" (one line in 36h)
- Zero dispatches to TinyClaw in adapter logs
- tools_proxy.go exists but is dead code

### Fix Path
1. Investigate why TinyClaw campaigns aren't being scheduled
2. Wire fetchGatewayTools() into the dispatch handler
3. Create a TinyClaw equivalent of rj-tool, or use Anthropic SDK tool_use natively

---

## Gateway-Direct

### Chain (actually works)
```
Campaign Runner                    rj-gateway
     |                                  |
     |-- POST /mcp (tools/call) ------->|  ← direct MCP, no agent
     |<-- JSON-RPC result --------------|
     |-- POST /mcp (next tool) -------->|
     |<-- JSON-RPC result --------------|
```

This is the ONLY working chain. It bypasses all agents and calls gateway tools directly.
`cc-gateway-health` uses this path and correctly reports 5 tool calls.

**Limitation**: No LLM involved. Can only test tool availability, not agent reasoning.

---

## Summary

| Agent | LLM Calls | Tool Calls | Findings | Status |
|-------|-----------|------------|----------|--------|
| IronClaw | Real (~$0.10/call, 1-2 min) | 0 (never calls exec) | Confabulated | Broken |
| HexStrike | None (no LLM in chain) | 0 (all skipped) | None | Broken |
| TinyClaw | Unknown (never dispatched) | 0 (no tool bridge) | None | Dead |
| Gateway-Direct | None (tool-only) | Real (5/call) | None | Works |
