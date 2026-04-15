# Getting Started

This section covers installation, configuration, and first-time setup of RemoteJuggler.

## Overview

RemoteJuggler provides a unified interface for managing multiple git identities across providers. The installation process:

1. Downloads a platform-specific binary
2. Creates configuration at `~/.config/remote-juggler/config.json`
3. Imports existing identities from `~/.ssh/config`
4. Optionally configures IDE integrations (Claude Code, JetBrains)

## Requirements

- macOS (arm64 or amd64) or Linux (amd64)
- SSH keys configured per-identity in `~/.ssh/config`
- Optional: `glab` CLI for GitLab token operations
- Optional: `gh` CLI for GitHub token operations

## Quick Install

```bash
curl -sSL https://raw.githubusercontent.com/Jesssullivan/remote-juggler/main/install.sh | bash
```

This script handles platform detection, binary download, and initial configuration. See [Installation](installation.md) for detailed options.

## Verify Installation

```bash
# Check version
remote-juggler --version

# List detected identities
remote-juggler list

# Show current status
remote-juggler status
```

## Next Steps

- [Installation](installation.md) - Detailed installation options
- [Quick Start](quick-start.md) - Configure your first identity
- [Configuration](configuration.md) - Configuration file reference
