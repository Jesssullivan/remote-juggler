# Install RemoteJuggler

Choose the installation method that matches your platform and workflow. For detailed instructions including build prerequisites, uninstall, and troubleshooting, see the [full installation guide](getting-started/installation.md).

## Quick Install (Recommended)

One-liner for Linux (amd64/arm64):

```bash
curl -fsSL https://raw.githubusercontent.com/Jesssullivan/remote-juggler/main/install.sh | bash
```

This downloads the pre-built binary, verifies checksums, and installs to `~/.local/bin`.

!!! note "macOS"
    Native macOS binaries are not yet available. Use npm/npx or build from source.

## npm / npx

Works on any platform with Node.js 18+:

```bash
# Install globally
npm install -g @tummycrypt/remote-juggler@beta

# Or run directly without installing
npx @tummycrypt/remote-juggler@beta --version
npx @tummycrypt/remote-juggler@beta --mode=mcp
```

## Nix (Linux)

```bash
# Try it without installing
nix run github:Jesssullivan/remote-juggler -- --version

# Install to profile
nix profile install github:Jesssullivan/remote-juggler

# Use in a flake
# flake.nix inputs:
#   inputs.remote-juggler.url = "github:Jesssullivan/remote-juggler";
```

### Nix Home Manager

```nix
# In your Home Manager configuration:
imports = [ inputs.remote-juggler.homeManagerModules.default ];

programs.remote-juggler = {
  enable = true;
  gui.enable = true;   # GTK4 GUI (Linux only)
  mcp.enable = true;   # Configure MCP for AI agents
  mcp.clients = [ "claude-code" "cursor" "vscode" ];
};
```

## Debian / Ubuntu

```bash
# Download the specific release (no /latest — use the version tag)
curl -LO https://github.com/Jesssullivan/remote-juggler/releases/download/v2.2.0/remote-juggler_2.2.0_amd64.deb
sudo dpkg -i remote-juggler_2.2.0_amd64.deb
```

## RHEL / Rocky / Fedora

```bash
curl -LO https://github.com/Jesssullivan/remote-juggler/releases/download/v2.2.0/remote-juggler-2.2.0-1.x86_64.rpm
sudo rpm -i remote-juggler-2.2.0-1.x86_64.rpm
```

## From Source

Requires [Chapel](https://chapel-lang.org/) 2.7+:

```bash
git clone https://github.com/Jesssullivan/remote-juggler.git
cd remote-juggler
just release
# Binary at target/release/remote_juggler
```

---

## AI Agent Setup

RemoteJuggler includes a built-in MCP (Model Context Protocol) server with 36 tools for AI-assisted git identity management.

### Claude Code

The install script automatically configures Claude Code. To set up manually:

```json
// .mcp.json (project or ~/.claude/.mcp.json)
{
  "mcpServers": {
    "remote-juggler": {
      "command": "remote-juggler",
      "args": ["--mode=mcp"]
    }
  }
}
```

### Cursor / VS Code / Windsurf

Add to your MCP configuration:

=== "Cursor (~/.cursor/mcp.json)"

    ```json
    {
      "mcpServers": {
        "remote-juggler": {
          "command": "remote-juggler",
          "args": ["--mode=mcp"]
        }
      }
    }
    ```

=== "VS Code (~/.config/Code/User/mcp.json)"

    ```json
    {
      "mcpServers": {
        "remote-juggler": {
          "command": "remote-juggler",
          "args": ["--mode=mcp"]
        }
      }
    }
    ```

=== "Windsurf (~/.windsurf/mcp.json)"

    ```json
    {
      "mcpServers": {
        "remote-juggler": {
          "command": "remote-juggler",
          "args": ["--mode=mcp"]
        }
      }
    }
    ```

### JetBrains (ACP)

```json
// ~/.jetbrains/acp.json
{
  "servers": {
    "remote-juggler": {
      "command": "remote-juggler",
      "args": ["--mode=acp"]
    }
  }
}
```

### npx (Zero-Install MCP)

For environments without a persistent installation:

```json
{
  "mcpServers": {
    "remote-juggler": {
      "command": "npx",
      "args": ["-y", "@tummycrypt/remote-juggler@beta", "--mode=mcp"]
    }
  }
}
```

---

## Verify Installation

```bash
remote-juggler --version
remote-juggler status
remote-juggler list
```
