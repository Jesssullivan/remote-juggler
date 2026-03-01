# WS1: TinyClaw Rename — Progress

**Started**: 2026-02-28
**Status**: COMPLETE
**Effort**: 1.5 days estimated, ~1 day actual

## Scorecard

| Metric | Before | Target | Current |
|--------|--------|--------|---------|
| Files with "picoclaw" (code/config) | 57 | 0 (excluding repo names) | 0 |
| Pods healthy after rename | N/A | All | Pending deploy |
| Adapter tests passing | Yes | Yes | Yes (0.129s) |
| Runner tests passing | Yes | Yes | Yes (13.7s) |

## Phase Checklist

- [x] Step 1: Tofu variable + resource rename (ATOMIC)
  - tinyclaw.tf (renamed from picoclaw.tf), variables.tf, agents.tf, campaigns.tf, ironclaw.tf, terraform.tfvars, apply.sh (with fallback)
- [x] Step 2: Adapter + runner code rename
  - tinyclaw.go, tinyclaw_test.go, adapter.go, main.go, dispatcher.go, runner main.go, runner_test.go
- [x] Step 3: Campaign migration
  - Renamed directory, updated schema.json, index.json, 8 campaign JSONs. Preserved repo name refs.
- [x] Step 4: Workflow + fork + portal
  - tofu-apply.yml, workspace-sync.yml, push-to-forks.sh, portal.go, fork-dockerfiles, agent-pr template, hexstrike_server.py K8s ref
- [x] Step 5: Documentation sweep
  - 6 agent_plane/ docs, 3 memory files, progress.md, web-access.md

## Legitimate Remaining "picoclaw" References

These are correct and should NOT be changed:
- `"repo": "picoclaw"` — GitHub repo `tinyland-inc/picoclaw` name unchanged
- `sipeed/picoclaw` — upstream reference project
- `/home/picoclaw/.picoclaw` — container internal paths (baked into image)
- `exec picoclaw` — upstream binary name in entrypoint
- `apply.sh` Setec key fallback — backward compatibility

## Verification

```bash
# Only repo names and container paths remain
grep -rn "picoclaw" deploy/ test/ gateway/ .github/ --include='*.go' --include='*.tf' --include='*.json' --include='*.yml' --include='*.yaml' --include='*.sh' | grep -v "tinyland-inc/picoclaw" | grep -v "sipeed/picoclaw" | grep -v "/home/picoclaw/" | grep -v "exec picoclaw"
# Expected: only apply.sh fallback line

# Tests pass
cd deploy/adapters && go test ./...   # OK 0.129s
cd test/campaigns/runner && go test ./...  # OK 13.7s
```

## Daily Log

### 2026-02-28
- Created progress file
- Inventory: 57 files, ~197 references
- Step 1: Renamed picoclaw.tf → tinyclaw.tf, updated all tofu variables/resources
- Step 2: Renamed adapter Go files, updated dispatcher and runner. Tests pass.
- Step 3: Renamed campaign directory, updated schema/index/8 campaign JSONs
- Step 4: Updated workflows, fork deployment, portal, reference deployments
- Step 5: Updated 6 agent_plane/ docs, 3 memory files, 2 other docs
- WS1 COMPLETE — all code/config references renamed, only repo names and container paths remain
