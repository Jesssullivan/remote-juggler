#!/bin/sh
# RemoteJuggler MCP Client Configuration Script
#
# Detects installed MCP clients (Claude Code, Cursor, VS Code, Windsurf,
# JetBrains) and configures RemoteJuggler as an MCP/ACP server for each.
#
# Usage:
#   ./scripts/configure-mcp-clients.sh
#   curl -fsSL https://raw.githubusercontent.com/Jesssullivan/remote-juggler/main/scripts/configure-mcp-clients.sh | sh

set -eu

# Colors
if [ -t 1 ]; then
  GREEN='\033[0;32m'
  YELLOW='\033[0;33m'
  BLUE='\033[0;34m'
  DIM='\033[2m'
  NC='\033[0m'
else
  GREEN='' YELLOW='' BLUE='' DIM='' NC=''
fi

# Detect OS for paths
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"

CONFIGURED=0
SKIPPED=0

# MCP config snippet
MCP_CONFIG='{
  "mcpServers": {
    "remote-juggler": {
      "command": "remote-juggler",
      "args": ["--mode=mcp"]
    }
  }
}'

# VS Code uses a slightly different format
VSCODE_MCP_CONFIG='{
  "servers": {
    "remote-juggler": {
      "command": "remote-juggler",
      "args": ["--mode=mcp"]
    }
  }
}'

# JetBrains ACP config
ACP_CONFIG='{
  "agent_servers": {
    "RemoteJuggler": {
      "command": "remote-juggler",
      "args": ["--mode=acp"],
      "env": {},
      "use_idea_mcp": false,
      "use_custom_mcp": false
    }
  }
}'

configure_client() {
  name="$1"
  config_dir="$2"
  config_file="$3"
  config_content="$4"

  if [ ! -d "$config_dir" ]; then
    printf "  %s%-12s%s Not installed (no %s)\n" "$DIM" "$name:" "$NC" "$config_dir"
    SKIPPED=$((SKIPPED + 1))
    return 0
  fi

  if [ -f "$config_file" ] && grep -q "remote-juggler" "$config_file" 2>/dev/null; then
    printf "  %s%-12s%s Already configured\n" "$GREEN" "$name:" "$NC"
    return 0
  fi

  mkdir -p "$(dirname "$config_file")"
  printf '%s\n' "$config_content" > "$config_file"
  printf "  %s%-12s%s Configured at %s\n" "$GREEN" "$name:" "$NC" "$config_file"
  CONFIGURED=$((CONFIGURED + 1))
}

printf '%bRemoteJuggler MCP Client Configuration%b\n' "$BLUE" "$NC"
printf '========================================\n\n'

# Check binary is available
if ! command -v remote-juggler >/dev/null 2>&1; then
  printf '%bWarning:%b remote-juggler not found in PATH\n' "$YELLOW" "$NC"
  printf '  Install first: curl -fsSL https://raw.githubusercontent.com/Jesssullivan/remote-juggler/main/install.sh | bash\n\n'
fi

# Claude Code
configure_client "Claude Code" "$HOME/.claude" "$HOME/.claude/.mcp.json" "$MCP_CONFIG"

# Cursor
configure_client "Cursor" "$HOME/.cursor" "$HOME/.cursor/mcp.json" "$MCP_CONFIG"

# VS Code
if [ "$OS" = "darwin" ]; then
  vscode_dir="$HOME/Library/Application Support/Code/User"
else
  vscode_dir="$HOME/.config/Code/User"
fi
configure_client "VS Code" "$vscode_dir" "${vscode_dir}/mcp.json" "$VSCODE_MCP_CONFIG"

# Windsurf
configure_client "Windsurf" "$HOME/.windsurf" "$HOME/.windsurf/mcp.json" "$MCP_CONFIG"

# JetBrains (ACP)
if [ "$OS" = "darwin" ]; then
  jb_dir="$HOME/Library/Application Support/JetBrains"
else
  jb_dir="$HOME/.jetbrains"
fi
configure_client "JetBrains" "$jb_dir" "${jb_dir}/acp.json" "$ACP_CONFIG"

printf '\n'
if [ "$CONFIGURED" -gt 0 ]; then
  printf '%bConfigured %d client(s).%b\n' "$GREEN" "$CONFIGURED" "$NC"
fi
if [ "$SKIPPED" -gt 0 ]; then
  printf '%s%d client(s) not installed.%s\n' "$DIM" "$SKIPPED" "$NC"
fi

printf '\nMCP Server: remote-juggler --mode=mcp\n'
printf 'ACP Server: remote-juggler --mode=acp\n'
