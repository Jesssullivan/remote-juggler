# Contributing to RemoteJuggler

Thank you for your interest in contributing to RemoteJuggler! This document provides guidelines and instructions for contributing.

## Table of Contents

- [Code of Conduct](#code-of-conduct)
- [Getting Started](#getting-started)
- [Development Setup](#development-setup)
- [Making Changes](#making-changes)
- [Code Style](#code-style)
- [Testing](#testing)
- [Submitting Changes](#submitting-changes)
- [Issue Reporting](#issue-reporting)

---

## Code of Conduct

This project adheres to our [Code of Conduct](CODE_OF_CONDUCT.md). By participating, you are expected to uphold this code. Please report unacceptable behavior to the maintainers.

---

## Getting Started

### Prerequisites

RemoteJuggler is a multi-language project. Depending on what you're working on, you'll need:

| Component | Language | Requirements |
|-----------|----------|--------------|
| CLI | Chapel | Chapel 2.0+, Make |
| HSM Library | C | GCC/Clang, TPM2-TSS (Linux) |
| GTK GUI | Rust | Rust 1.75+, GTK4, Libadwaita |
| Linux Tray | Go | Go 1.21+ |
| macOS Tray | Swift | Swift 5.9+, Xcode |
| Tests | Python | Python 3.10+, pytest |

### Quick Setup

```bash
# Clone the repository
git clone https://github.com/Jesssullivan/remote-juggler.git
cd remote-juggler

# Option 1: Use Nix (recommended for reproducibility)
nix develop

# Option 2: Install dependencies manually and use just
just deps
just build
just test
```

---

## Development Setup

### Using Nix (Recommended)

Nix provides a reproducible development environment with all dependencies:

```bash
# Enter development shell with all tools
nix develop

# Chapel-specific shell (Linux only, uses FHS environment)
nix develop .#chapel

# TPM development shell with swtpm
nix develop .#tpm
```

### Manual Setup

#### Chapel CLI

```bash
# macOS
brew install chapel

# Linux (via Nix or download from chapel-lang.org)
# See docs/NIX_CHAPEL_SETUP.md for Nix setup

# Build
just release
```

#### HSM Library

```bash
# Linux (TPM support)
sudo dnf install tpm2-tss-devel  # Fedora/RHEL
sudo apt install libtss2-dev     # Debian/Ubuntu

# Build
cd pinentry && make
```

#### GTK GUI (Linux)

```bash
# Install dependencies
sudo dnf install gtk4-devel libadwaita-devel

# Build
cd gtk-gui && cargo build --release
```

#### Tray Apps

```bash
# Linux (Go)
cd tray/linux && go build

# macOS (Swift)
cd tray/darwin && swift build -c release
```

---

## Making Changes

### Branch Naming

Use descriptive branch names with prefixes:

- `feat/` - New features
- `fix/` - Bug fixes
- `docs/` - Documentation changes
- `refactor/` - Code refactoring
- `test/` - Test additions or fixes
- `ci/` - CI/CD changes

Examples:
- `feat/yubikey-touch-detection`
- `fix/keychain-access-denied`
- `docs/improve-troubleshooting`

### Commit Messages

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <description>

[optional body]

[optional footer(s)]
```

**Types:**
- `feat` - New feature
- `fix` - Bug fix
- `docs` - Documentation
- `refactor` - Code refactoring
- `test` - Tests
- `ci` - CI/CD changes
- `chore` - Maintenance

**Examples:**
```
feat(hsm): add YubiKey touch policy detection

Detects touch policy from ykman and warns users
when signing will require physical interaction.

Closes #42
```

```
fix(keychain): handle access denied on macOS Sonoma

macOS 14+ requires explicit keychain access permissions.
Added retry with user prompt for keychain authorization.
```

---

## Code Style

### Chapel

- Use 2-space indentation
- Follow Chapel's naming conventions (camelCase for variables, PascalCase for types)
- Add doc comments for public functions using `/* */` style
- Keep lines under 100 characters

```chapel
/*
 * Switches to the specified identity.
 *
 * @param identity: Name of the identity to switch to
 * @param options: Switch options (setRemote, configureGPG)
 * @return: True if switch was successful
 */
proc switchIdentity(identity: string, options: SwitchOptions): bool {
  // Implementation
}
```

### C (HSM Library)

- Use 4-space indentation
- Follow Linux kernel style for naming (lowercase with underscores)
- Add header comments for functions
- Use `/* */` for multi-line comments

```c
/**
 * Store a PIN securely in the HSM.
 *
 * @param identity Identity name (e.g., "personal", "work")
 * @param pin      The PIN to store
 * @param pin_len  Length of the PIN
 * @return HSM_SUCCESS on success, error code otherwise
 */
int hsm_store_pin(const char *identity, const char *pin, size_t pin_len);
```

### Rust (GTK GUI)

- Use `rustfmt` with default settings
- Follow Rust API guidelines
- Use `clippy` and fix all warnings

```bash
cd gtk-gui
cargo fmt --check
cargo clippy -- -D warnings
```

### Go (Linux Tray)

- Use `gofmt`
- Follow Go conventions

```bash
cd tray/linux
go fmt ./...
go vet ./...
```

### Swift (macOS Tray)

- Use SwiftLint with provided configuration
- Follow Swift API design guidelines

---

## Testing

### Running Tests

```bash
# All tests
just test-all

# Unit tests only
just test

# Integration tests
just test-integration

# E2E tests (excludes hardware-dependent tests)
just test-e2e-ci

# E2E with GPG
just test-e2e-gpg

# E2E with TPM (requires swtpm)
just test-e2e-tpm

# MCP protocol tests
just test-e2e-mcp

# HSM library tests
just hsm-test
```

### Writing Tests

- Add unit tests for new functions
- Add E2E tests for new user-facing features
- Use pytest markers for hardware-dependent tests:
  - `@pytest.mark.tpm` - Requires TPM/swtpm
  - `@pytest.mark.secure_enclave` - Requires macOS SE
  - `@pytest.mark.gpg` - Requires GPG
  - `@pytest.mark.yubikey` - Requires physical YubiKey
  - `@pytest.mark.slow` - Tests taking >10 seconds

### Test Coverage

New features should include tests. Aim for:
- Unit test coverage for business logic
- E2E test for user-facing workflows
- Error case testing for edge conditions

---

## Submitting Changes

### Before Submitting

1. **Run tests locally:**
   ```bash
   just ci
   ```

2. **Check formatting:**
   ```bash
   just lint
   just fmt-check
   ```

3. **Update documentation** if you changed behavior

4. **Add changelog entry** in `CHANGELOG.md` under `[Unreleased]`

### Pull Request Process

1. **Create a merge request** on GitLab
2. **Fill out the MR template** with:
   - Summary of changes
   - Test plan
   - Related issues
3. **Ensure CI passes** - all automated checks must pass
4. **Request review** from maintainers
5. **Address feedback** - push additional commits to address review comments
6. **Squash if requested** - maintainers may ask you to squash commits

### MR Title Format

Use the same format as commit messages:
```
feat(hsm): add YubiKey touch policy detection
```

---

## Issue Reporting

### Bug Reports

When reporting bugs, include:

1. **RemoteJuggler version**: `remote-juggler --version`
2. **Operating system**: macOS version or Linux distro
3. **Steps to reproduce**: Numbered steps to trigger the bug
4. **Expected behavior**: What should happen
5. **Actual behavior**: What actually happened
6. **Logs**: Run with `--debug` and include relevant output
7. **Configuration**: Sanitized config (remove tokens/passwords)

### Feature Requests

For feature requests, include:

1. **Use case**: What problem does this solve?
2. **Proposed solution**: How should it work?
3. **Alternatives considered**: Other approaches you've thought of
4. **Impact**: How many users would benefit?

### Security Issues

**Do not open public issues for security vulnerabilities.**

Email security concerns to: security@tinyland.dev

Include:
- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

---

## Development Tips

### Debugging

```bash
# Run with debug output
remote-juggler --debug switch personal

# MCP server debug mode
remote-juggler --mode=mcp --debug

# HSM debug
REMOTE_JUGGLER_HSM_DEBUG=1 remote-juggler pin status
```

### Local Testing with swtpm

```bash
# Start swtpm manually
mkdir -p /tmp/swtpm
swtpm socket --tpmstate dir=/tmp/swtpm --ctrl type=tcp,port=2322 --tpm2

# Set environment
export TPM2TOOLS_TCTI="swtpm:host=localhost,port=2321"

# Run TPM tests
just test-e2e-tpm
```

### Building Documentation

```bash
# Preview docs locally
just docs-serve

# Build docs
just docs-build
```

---

## Recognition

Contributors are recognized in:
- `CHANGELOG.md` - for significant contributions
- GitHub/GitLab release notes
- Project documentation

---

## Questions?

- **Documentation**: https://remote-juggler.dev/docs
- **Issues**: https://github.com/Jesssullivan/remote-juggler/issues
- **Discussions**: https://github.com/Jesssullivan/remote-juggler/discussions

Thank you for contributing to RemoteJuggler!
