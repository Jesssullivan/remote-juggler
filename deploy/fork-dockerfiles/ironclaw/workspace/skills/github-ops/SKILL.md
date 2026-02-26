---
name: github-ops
description: GitHub operations for file management, branch creation, pull requests, and security alerts
version: "2.0"
tags: [github, git, pr, security, codeql]
---

# GitHub Operations

Use this skill for all GitHub repository interactions. All operations are authenticated via the gateway's GitHub token (resolved from Setec).

**All tools are accessed via exec and the rj-tool wrapper:**

```bash
exec("/workspace/bin/rj-tool <tool_name> [key=value ...]")
```

## File Operations
- `exec("/workspace/bin/rj-tool github_fetch owner=tinyland-inc repo=ironclaw path=package.json")` fetches file contents
  - Returns decoded content, SHA, and size
  - Add `ref=<branch>` to target a specific branch or commit

## Code Scanning
- `exec("/workspace/bin/rj-tool github_list_alerts owner=tinyland-inc repo=ironclaw")` lists CodeQL alerts
  - Filter by state: `state=open`, `state=closed`, `state=dismissed`, `state=fixed`
  - Filter by severity: `severity=critical`, `severity=high`, `severity=medium`, `severity=low`
- `exec("/workspace/bin/rj-tool github_get_alert owner=tinyland-inc repo=ironclaw alert_number=1")` gets full details

## Branch & PR Workflow
1. `exec("/workspace/bin/rj-tool github_create_branch owner=tinyland-inc repo=ironclaw branch_name=fix/issue-42")`
   - Default base: `main`. Override with `base=dev`
2. `exec("/workspace/bin/rj-tool github_update_file owner=tinyland-inc repo=ironclaw path=README.md content='...' message='Update readme' branch=fix/issue-42")`
   - Handles SHA resolution automatically (update existing or create new)
3. `exec("/workspace/bin/rj-tool github_create_pr owner=tinyland-inc repo=ironclaw title='Fix issue 42' head=fix/issue-42")`
   - Default base: `main`. Override with `base=dev`

## Request Secret Provisioning
- `exec("/workspace/bin/rj-tool juggler_request_secret name=brave-api-key reason='Web search capability' urgency=medium")`
  - Creates a labeled issue on tinyland-inc/remote-juggler requesting the secret

## Bot Identity
- All operations are attributed to `rj-agent-bot[bot]` (GitHub App ID 2945224)
- Git commits use: `rj-agent-bot[bot] <2945224+rj-agent-bot[bot]@users.noreply.github.com>`
- SSH key at `/home/agent/.ssh/id_ed25519`

## Common Patterns
- **Fix CodeQL alert**: list_alerts -> fetch file -> create_branch -> update_file -> create_pr
- **Update config**: fetch current -> create_branch -> update_file -> create_pr
- **Reference comparison**: fetch from reference project -> compare with our repo
