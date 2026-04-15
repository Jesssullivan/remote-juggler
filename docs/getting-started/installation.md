---
title: "Installation"
description: "Multi-platform installation instructions for RemoteJuggler including automated install, binary download, building from source, and IDE integration setup."
category: "operations"
llm_priority: 3
keywords:
  - install
  - download
  - binary
  - build
  - macos
  - linux
---

# Installation

RemoteJuggler provides multiple installation methods for different platforms and use cases.

## Automated Installation (Recommended)

The install script handles platform detection, binary download, and checksum verification:

```bash
curl -fsSL https://raw.githubusercontent.com/Jesssullivan/remote-juggler/main/install.sh | bash
```

The script performs these operations:

1. Detects platform (linux amd64/arm64)
2. Downloads pre-built binary from GitHub releases
3. Verifies SHA256 checksum
4. Installs to `~/.local/bin`

!!! note "macOS"
    Native macOS binaries are not yet available. Use npm/npx or build from source.

### Version and Channel Selection

```bash
# Install specific version
curl -fsSL https://raw.githubusercontent.com/Jesssullivan/remote-juggler/main/install.sh | bash -s -- --version 2.2.0

# Install from stable channel (falls back to beta if stable has no assets)
curl -fsSL https://raw.githubusercontent.com/Jesssullivan/remote-juggler/main/install.sh | bash -s -- --channel stable

# Environment variable override
REMOTE_JUGGLER_VERSION=2.2.0 curl -fsSL https://raw.githubusercontent.com/Jesssullivan/remote-juggler/main/install.sh | bash
```

### Uninstall via Script

```bash
curl -fsSL https://raw.githubusercontent.com/Jesssullivan/remote-juggler/main/install.sh | bash -s -- --uninstall
```

## Package Managers

### Homebrew (macOS/Linux)

Install via the Tinyland Homebrew tap:

```bash
# Add the tap
brew tap tinyland/tools https://github.com/tinyland-inc/homebrew-tap.git

# Install
brew install remote-juggler

# Update
brew upgrade remote-juggler
```

#### Shell Completions

Homebrew automatically installs shell completions. To enable them:

```bash
# Bash (add to ~/.bashrc)
source $(brew --prefix)/etc/bash_completion.d/remote-juggler.bash

# Zsh (add to ~/.zshrc)
fpath=($(brew --prefix)/share/zsh/site-functions $fpath)
autoload -Uz compinit && compinit

# Fish - completions auto-load
```

### Debian/Ubuntu

```bash
# Download .deb package from releases
curl -LO https://github.com/Jesssullivan/remote-juggler/releases/download/v2.2.0/remote-juggler_2.2.0_amd64.deb

# Install
sudo dpkg -i remote-juggler_2.2.0_amd64.deb
sudo apt-get install -f  # Fix dependencies if needed
```

### Fedora/RHEL/Rocky

```bash
# Download .rpm package from releases
curl -LO https://github.com/Jesssullivan/remote-juggler/releases/download/v2.2.0/remote-juggler-2.2.0-1.x86_64.rpm

# Install
sudo dnf install remote-juggler-2.2.0-1.x86_64.rpm
```

### Nix (Linux)

```bash
# Ephemeral (try it out)
nix run github:Jesssullivan/remote-juggler -- --version

# Install to profile
nix profile install github:Jesssullivan/remote-juggler

# In flake.nix
{
  inputs.remote-juggler.url = "github:Jesssullivan/remote-juggler";
  # ...
  environment.systemPackages = [ inputs.remote-juggler.packages.${system}.default ];
}
```

## Binary Download

Download pre-built binaries from the [Releases page](https://github.com/Jesssullivan/remote-juggler/releases):

| Platform | Architecture | Asset Name |
|----------|--------------|------------|
| Linux | x86_64 | `remote-juggler-linux-amd64` |
| Linux | ARM64 | `remote-juggler-linux-arm64` |

```bash
# Example: Download and install
curl -LO https://github.com/Jesssullivan/remote-juggler/releases/download/v2.2.0/remote-juggler-linux-amd64
install -m 755 remote-juggler-linux-amd64 ~/.local/bin/remote-juggler
```

### Verify Downloads

Always verify downloads with per-file or aggregate checksums:

```bash
# Download checksum
curl -LO https://github.com/Jesssullivan/remote-juggler/releases/download/v2.2.0/remote-juggler-linux-amd64.sha256

# Verify
echo "$(cat remote-juggler-linux-amd64.sha256)  remote-juggler-linux-amd64" | sha256sum -c -

# Or use the aggregate checksums file
curl -LO https://github.com/Jesssullivan/remote-juggler/releases/download/v2.2.0/SHA256SUMS.txt
sha256sum -c SHA256SUMS.txt --ignore-missing
```

### Linux AppImage (GUI)

```bash
curl -LO https://github.com/Jesssullivan/remote-juggler/releases/download/v2.2.0/remote-juggler-gui-v2.2.0-x86_64.AppImage
chmod +x remote-juggler-gui-v2.2.0-x86_64.AppImage
./remote-juggler-gui-v2.2.0-x86_64.AppImage
```

## Build from Source

Building requires Chapel 2.7.0 or later.

```bash
# Clone repository
git clone https://github.com/Jesssullivan/remote-juggler.git
cd remote-juggler

# Build with just
just build    # Debug build -> target/debug/
just release  # Optimized build -> target/release/

# Install to ~/.local/bin
just install
```

### Build Prerequisites

| Dependency | Version | Purpose |
|------------|---------|---------|
| Chapel | 2.7+ | Main CLI compiler |
| Rust | 1.75+ | GTK GUI (optional) |
| Go | 1.21+ | Linux tray app (optional) |
| Swift | 5.9+ | macOS tray app (optional) |
| TPM2-TSS | 4.0+ | TPM support (Linux, optional) |

### macOS Build Requirements

macOS builds link against Security.framework for Keychain integration. The Makefile handles this automatically:

```makefile
ifeq ($(UNAME_S),Darwin)
  CHPL_LDFLAGS = --ldflags="-framework Security -framework CoreFoundation"
endif
```

### Build with Nix

```bash
# Enter development shell
nix develop

# Build all packages
nix build .#remote-juggler
nix build .#pinentry-remotejuggler
nix build .#remote-juggler-gui

# Build for specific platform
nix build .#packages.x86_64-linux.remote-juggler
nix build .#packages.aarch64-darwin.remote-juggler
```

## Installation Locations

The install script searches for directories in PATH:

1. `~/.local/bin` (preferred)
2. `~/bin`
3. `~/.bin`

If none exist, it creates `~/.local/bin` and prompts you to add it to PATH.

## Configuration Directory

Configuration files are stored following XDG Base Directory spec:

```
${XDG_CONFIG_HOME:-$HOME/.config}/remote-juggler/
  config.json       # Main configuration
```

## IDE Integration

### Claude Code

The installer creates slash commands at `~/.claude/commands/`:

- `/juggle <identity>` - Switch identity
- `/identity <action>` - List/detect/validate identities
- `/remotes` - View remote configuration

Add to your `.mcp.json` for MCP tool access:

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

### JetBrains

For IntelliJ-based IDEs, add to `~/.jetbrains/acp.json`:

```json
{
  "agent_servers": {
    "RemoteJuggler": {
      "command": "remote-juggler",
      "args": ["--mode=acp"]
    }
  }
}
```

## Uninstallation

Remove the binary and configuration:

```bash
rm ~/.local/bin/remote-juggler
rm -rf ~/.config/remote-juggler
rm ~/.claude/commands/{juggle,identity,remotes}.md
```

## Troubleshooting

### Binary not found after install

Add the install directory to your PATH:

```bash
# bash/zsh
echo 'export PATH="$PATH:$HOME/.local/bin"' >> ~/.bashrc

# fish
fish_add_path ~/.local/bin
```

### Permission denied

Ensure the binary is executable:

```bash
chmod +x ~/.local/bin/remote-juggler
```

### macOS Gatekeeper warning

If macOS blocks the binary ("App is damaged and can't be opened"):

```bash
xattr -d com.apple.quarantine ~/.local/bin/remote-juggler
```

Verify code signature:

```bash
codesign -dv --verbose=4 /usr/local/bin/remote-juggler
spctl -a -vvv -t install RemoteJuggler.app
```

### Linux: Missing shared libraries

```bash
# Check dependencies
ldd $(which remote-juggler)

# Install missing libraries
sudo apt install libgcc-s1  # Debian/Ubuntu
sudo dnf install libgcc     # Fedora
```

### Flatpak: Can't access SSH keys

Flatpak sandboxing may prevent SSH access. Grant permissions:

```bash
flatpak override --user --filesystem=~/.ssh:ro dev.tinyland.RemoteJuggler
```
