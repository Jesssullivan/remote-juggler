#!/usr/bin/env bash
# Update the agent ecosystem status section in README.md and docs/agents/index.md.
#
# Queries GitHub Discussions (Agent Reports category) via GraphQL to extract
# the latest campaign run status for each campaign, then generates a markdown
# table and replaces content between AGENT-STATUS markers.
#
# Environment:
#   GH_TOKEN  - GitHub token with read:discussion permission
#   REPO      - Repository in owner/repo format (default: tinyland-inc/remote-juggler)

set -euo pipefail

REPO="${REPO:-tinyland-inc/remote-juggler}"
OWNER="${REPO%%/*}"
NAME="${REPO##*/}"

# Query latest Discussions from Agent Reports category.
query_discussions() {
    gh api graphql -f query='
query($owner: String!, $name: String!) {
  repository(owner: $owner, name: $name) {
    discussions(
      first: 50,
      orderBy: { field: CREATED_AT, direction: DESC },
      categoryId: null
    ) {
      nodes {
        title
        createdAt
        category { name }
        body
      }
    }
  }
}' -f owner="$OWNER" -f name="$NAME" 2>/dev/null || echo '{"data":{"repository":{"discussions":{"nodes":[]}}}}'
}

# Parse discussions into campaign status lines.
# Title format: [STATUS] Campaign Name | 2026-02-25 06:00 UTC
parse_status() {
    local json="$1"
    echo "$json" | jq -r '
        .data.repository.discussions.nodes[]
        | select(.category.name == "Agent Reports" or .category.name == "Weekly Digest" or .category.name == "Security Advisories")
        | .title + "\t" + .createdAt + "\t" + .body
    ' 2>/dev/null || true
}

# Extract key metric from body (first KPI row).
extract_metric() {
    local body="$1"
    echo "$body" | grep -oP '^\| \K[^|]+\| [^|]+' | head -1 | sed 's/  *//g' || echo ""
}

# Generate the status table.
generate_table() {
    local now
    now="$(date -u +"%Y-%m-%d %H:%M UTC")"

    cat <<HEADER
### Agent Ecosystem Status

Last updated: ${now}

| Campaign | Agent | Last Run | Status | Key Metric |
|----------|-------|----------|--------|------------|
HEADER

    # Known campaigns with their agents (static mapping -- campaigns don't change often).
    declare -A campaign_agents=(
        ["Gateway Health"]="gateway-direct"
        ["Gateway Smoketest"]="openclaw"
        ["Dependency Audit"]="openclaw"
        ["Credential Scan"]="hexstrike"
        ["MCP Regression"]="gateway-direct"
        ["Audit Completeness"]="cross-agent"
    )

    local discussions
    discussions="$(query_discussions)"

    for campaign_name in "Gateway Health" "Dependency Audit" "Credential Scan" "Gateway Smoketest" "MCP Regression" "Audit Completeness"; do
        local agent="${campaign_agents[$campaign_name]:-unknown}"
        local status="--"
        local last_run="--"
        local metric="--"

        # Find the most recent discussion matching this campaign name.
        local match
        match="$(echo "$discussions" | jq -r --arg name "$campaign_name" '
            .data.repository.discussions.nodes[]
            | select(.title | test($name; "i"))
            | .title + "|||" + .createdAt
        ' 2>/dev/null | head -1 || true)"

        if [[ -n "$match" ]]; then
            local title="${match%%|||*}"
            local created="${match##*|||}"

            # Extract status from title: [PASS], [FAIL], [TIMEOUT], [ERROR]
            if [[ "$title" =~ \[([A-Z]+)\] ]]; then
                status="${BASH_REMATCH[1]}"
            fi

            # Calculate relative time.
            local created_ts
            created_ts="$(date -d "$created" +%s 2>/dev/null || echo 0)"
            local now_ts
            now_ts="$(date +%s)"
            local diff_hours=$(( (now_ts - created_ts) / 3600 ))

            if [[ $diff_hours -lt 1 ]]; then
                last_run="<1h ago"
            elif [[ $diff_hours -lt 24 ]]; then
                last_run="${diff_hours}h ago"
            else
                local diff_days=$(( diff_hours / 24 ))
                last_run="${diff_days}d ago"
            fi
        fi

        echo "| ${campaign_name} | ${agent} | ${last_run} | ${status} | ${metric} |"
    done

    echo ""
    echo "*View all reports in [Discussions](https://github.com/${REPO}/discussions)*"
}

# Replace content between markers in a file.
splice_into_file() {
    local file="$1"
    local content="$2"
    local start_marker="<!-- AGENT-STATUS:START -->"
    local end_marker="<!-- AGENT-STATUS:END -->"

    if ! grep -q "$start_marker" "$file" 2>/dev/null; then
        echo "warning: markers not found in $file, skipping" >&2
        return
    fi

    awk -v start="$start_marker" -v end="$end_marker" -v content="$content" '
        $0 ~ start { print; print content; skip=1; next }
        $0 ~ end   { skip=0 }
        !skip       { print }
    ' "$file" > "${file}.tmp"
    mv "${file}.tmp" "$file"
    echo "updated: $file"
}

# Main
main() {
    local table
    table="$(generate_table)"

    # Update README.md.
    if [[ -f "README.md" ]]; then
        splice_into_file "README.md" "$table"
    fi

    # Update docs/agents/index.md.
    if [[ -f "docs/agents/index.md" ]]; then
        splice_into_file "docs/agents/index.md" "$table"
    fi
}

main "$@"
