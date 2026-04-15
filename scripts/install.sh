#!/usr/bin/env bash
#
# RemoteJuggler Universal Installer
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/Jesssullivan/remote-juggler/main/install.sh | bash
#   curl -fsSL https://raw.githubusercontent.com/Jesssullivan/remote-juggler/main/install.sh | bash -s -- --version 2.2.0
#
# Options:
#   --version VERSION    Install specific version (default: latest)
#   --prefix PATH        Installation prefix (default: ~/.local)
#   --no-verify          Skip checksum verification
#   --channel CHANNEL    Release channel: stable, beta, nightly (default: beta)
#   --help               Show this help message
#
# Environment:
#   REMOTE_JUGGLER_PREFIX    Override installation prefix
#   REMOTE_JUGGLER_VERSION   Override version to install
#   REMOTE_JUGGLER_CHANNEL   Override release channel
#
set -euo pipefail

# Configuration
REPO_URL="https://github.com/Jesssullivan/remote-juggler"
RELEASES_URL="${REPO_URL}/releases"
GPG_KEY_URL="${REPO_URL}/raw/main/keys/release-signing.asc"
DEFAULT_PREFIX="${HOME}/.local"
PROGRAM_NAME="remote-juggler"

# Colors (if terminal supports them)
if [[ -t 1 ]] && [[ -n "${TERM:-}" ]] && command -v tput &>/dev/null; then
    RED=$(tput setaf 1)
    GREEN=$(tput setaf 2)
    YELLOW=$(tput setaf 3)
    BLUE=$(tput setaf 4)
    BOLD=$(tput bold)
    RESET=$(tput sgr0)
else
    RED=""
    GREEN=""
    YELLOW=""
    BLUE=""
    BOLD=""
    RESET=""
fi

# Logging functions
info() { echo "${BLUE}==>${RESET} $*"; }
success() { echo "${GREEN}==>${RESET} $*"; }
warn() { echo "${YELLOW}Warning:${RESET} $*" >&2; }
error() { echo "${RED}Error:${RESET} $*" >&2; }
die() { error "$*"; exit 1; }

# Print usage
usage() {
    cat <<EOF
${BOLD}RemoteJuggler Universal Installer${RESET}

Usage:
  $0 [options]

Options:
  --version VERSION    Install specific version (default: latest)
  --prefix PATH        Installation prefix (default: ~/.local)
  --no-verify          Skip checksum verification
  --channel CHANNEL    Release channel: stable, beta, nightly (default: beta)
  --uninstall          Remove RemoteJuggler installation
  --help               Show this help message

Examples:
  # Install latest beta release
  curl -fsSL https://raw.githubusercontent.com/Jesssullivan/remote-juggler/main/install.sh | bash

  # Install specific version
  curl -fsSL https://raw.githubusercontent.com/Jesssullivan/remote-juggler/main/install.sh | bash -s -- --version 2.2.0

  # Install to /usr/local (requires sudo)
  curl -fsSL https://raw.githubusercontent.com/Jesssullivan/remote-juggler/main/install.sh | sudo bash -s -- --prefix /usr/local

  # Uninstall
  curl -fsSL https://raw.githubusercontent.com/Jesssullivan/remote-juggler/main/install.sh | bash -s -- --uninstall
EOF
}

# Detect OS and architecture
detect_platform() {
    local os arch

    # Detect OS
    case "$(uname -s)" in
        Linux*)  os="linux" ;;
        Darwin*) os="darwin" ;;
        MINGW*|MSYS*|CYGWIN*) os="windows" ;;
        *)       die "Unsupported operating system: $(uname -s)" ;;
    esac

    # Detect architecture
    case "$(uname -m)" in
        x86_64|amd64)   arch="amd64" ;;
        aarch64|arm64)  arch="arm64" ;;
        armv7l)         arch="armv7" ;;
        *)              die "Unsupported architecture: $(uname -m)" ;;
    esac

    echo "${os}-${arch}"
}

# Check for required commands
check_dependencies() {
    local missing=()

    for cmd in curl; do
        if ! command -v "$cmd" &>/dev/null; then
            missing+=("$cmd")
        fi
    done

    if [[ ${#missing[@]} -gt 0 ]]; then
        die "Missing required commands: ${missing[*]}"
    fi
}

# Get latest version from GitHub API
get_latest_version() {
    local channel="${1:-beta}"
    local api_url="https://api.github.com/repos/Jesssullivan/remote-juggler/releases"

    # Fetch releases
    local releases
    releases=$(curl -fsSL "$api_url" 2>/dev/null) || die "Failed to fetch releases"

    # Parse latest version based on channel
    local version
    case "$channel" in
        stable)
            # Stable releases: no pre-release suffix, must have assets
            version=$(echo "$releases" | grep -o '"tag_name":"v[0-9.]*"' | head -1 | cut -d'"' -f4 | sed 's/^v//')
            # Check if stable release has assets; if not, fall back to beta
            if [[ -n "$version" ]]; then
                local asset_count
                asset_count=$(echo "$releases" | python3 -c "
import json, sys
releases = json.load(sys.stdin)
for r in releases:
    if r['tag_name'] == 'v$version' and not r['prerelease']:
        print(len(r.get('assets', [])))
        break
" 2>/dev/null || echo "0")
                if [[ "$asset_count" == "0" ]]; then
                    warn "Stable release v${version} has no downloadable assets."
                    warn "Falling back to latest beta release."
                    version=$(echo "$releases" | grep -o '"tag_name":"v[0-9.]*-\(beta\|rc\)[.0-9]*"' | head -1 | cut -d'"' -f4 | sed 's/^v//')
                fi
            fi
            ;;
        beta)
            # Beta releases: -beta or -rc suffix
            version=$(echo "$releases" | grep -o '"tag_name":"v[0-9.]*-\(beta\|rc\)[.0-9]*"' | head -1 | cut -d'"' -f4 | sed 's/^v//')
            ;;
        nightly)
            # Nightly: use latest tag regardless
            version=$(echo "$releases" | grep -o '"tag_name":"[^"]*"' | head -1 | cut -d'"' -f4 | sed 's/^v//')
            ;;
        *)
            die "Unknown channel: $channel. Use: stable, beta, nightly"
            ;;
    esac

    if [[ -z "$version" ]]; then
        die "No ${channel} release found"
    fi

    echo "$version"
}

# Download file with progress
download() {
    local url="$1"
    local output="$2"

    info "Downloading: $(basename "$output")"

    if command -v wget &>/dev/null; then
        wget -q --show-progress -O "$output" "$url" || return 1
    else
        curl -fSL --progress-bar -o "$output" "$url" || return 1
    fi
}

# Verify checksum
verify_checksum() {
    local binary="$1"
    local checksums="$2"

    if [[ "${VERIFY}" != "true" ]]; then
        warn "Skipping checksum verification (--no-verify)"
        return 0
    fi

    info "Verifying checksum..."

    local expected_hash
    expected_hash=$(grep "$(basename "$binary")" "$checksums" | awk '{print $1}')

    if [[ -n "$expected_hash" ]]; then
        local actual_hash
        if command -v sha256sum &>/dev/null; then
            actual_hash=$(sha256sum "$binary" | awk '{print $1}')
        elif command -v shasum &>/dev/null; then
            actual_hash=$(shasum -a 256 "$binary" | awk '{print $1}')
        else
            warn "No SHA256 tool found. Skipping checksum verification."
            return 0
        fi

        if [[ "$expected_hash" == "$actual_hash" ]]; then
            success "Checksum verified: ${actual_hash:0:16}..."
        else
            die "Checksum mismatch! Expected: $expected_hash, Got: $actual_hash"
        fi
    else
        warn "Binary not found in checksums file. Skipping verification."
    fi
}

# Install binary from downloaded file
install_binary() {
    local binary_file="$1"
    local prefix="$2"
    local bindir="${prefix}/bin"

    info "Installing to ${bindir}..."

    # Create directories
    mkdir -p "$bindir"

    # Install binary
    install -m 755 "$binary_file" "${bindir}/${PROGRAM_NAME}"

    success "Installed ${PROGRAM_NAME} to ${bindir}/${PROGRAM_NAME}"
}

# Install shell completions to standard locations
install_completions() {
    local prefix="$1"

    # Generate completions from the installed binary if possible
    local binary="${prefix}/bin/${PROGRAM_NAME}"
    if [[ ! -x "$binary" ]]; then
        return 0
    fi

    # Bash completions
    local bash_dir="${HOME}/.local/share/bash-completion/completions"
    mkdir -p "$bash_dir"
    if "$binary" completions bash > "${bash_dir}/${PROGRAM_NAME}" 2>/dev/null; then
        success "Installed bash completions"
    fi

    # Zsh completions
    local zsh_dir="${HOME}/.zsh/completions"
    mkdir -p "$zsh_dir"
    if "$binary" completions zsh > "${zsh_dir}/_${PROGRAM_NAME}" 2>/dev/null; then
        success "Installed zsh completions"
    fi

    # Fish completions
    local fish_dir="${HOME}/.config/fish/completions"
    mkdir -p "$fish_dir"
    if "$binary" completions fish > "${fish_dir}/${PROGRAM_NAME}.fish" 2>/dev/null; then
        success "Installed fish completions"
    fi
}

# Post-install setup
post_install() {
    local prefix="$1"
    local bindir="${prefix}/bin"

    # Check if bindir is in PATH
    if [[ ":${PATH}:" != *":${bindir}:"* ]]; then
        warn "${bindir} is not in your PATH"
        echo ""
        echo "Add to your shell profile:"
        echo ""
        echo "  # For bash (~/.bashrc)"
        echo "  export PATH=\"${bindir}:\$PATH\""
        echo ""
        echo "  # For zsh (~/.zshrc)"
        echo "  export PATH=\"${bindir}:\$PATH\""
        echo ""
        echo "  # For fish (~/.config/fish/config.fish)"
        echo "  fish_add_path ${bindir}"
        echo ""
    fi

    # Run setup wizard suggestion
    echo ""
    success "Installation complete!"
    echo ""
    echo "Next steps:"
    echo "  1. Restart your shell or run: source ~/.bashrc (or ~/.zshrc)"
    echo "  2. Run the setup wizard: ${PROGRAM_NAME} setup"
    echo "  3. List identities: ${PROGRAM_NAME} list"
    echo ""
    echo "Documentation: https://tinyland-inc.github.io/remote-juggler"
    echo "Report issues: ${REPO_URL}/issues"
}

# Uninstall
uninstall() {
    local prefix="${1:-$DEFAULT_PREFIX}"
    local bindir="${prefix}/bin"

    info "Uninstalling RemoteJuggler..."

    # Remove binary
    if [[ -f "${bindir}/${PROGRAM_NAME}" ]]; then
        rm -f "${bindir}/${PROGRAM_NAME}"
        success "Removed ${bindir}/${PROGRAM_NAME}"
    fi

    # Remove completions directory
    local share_dir="${prefix}/share/${PROGRAM_NAME}"
    if [[ -d "$share_dir" ]]; then
        rm -rf "$share_dir"
        success "Removed ${share_dir}"
    fi

    # Remove completion symlinks
    rm -f "${HOME}/.local/share/bash-completion/completions/${PROGRAM_NAME}" 2>/dev/null || true
    rm -f "${HOME}/.zsh/completions/_${PROGRAM_NAME}" 2>/dev/null || true
    rm -f "${HOME}/.config/fish/completions/${PROGRAM_NAME}.fish" 2>/dev/null || true

    echo ""
    success "Uninstall complete!"
    echo ""
    echo "Note: User configuration preserved at ~/.config/remote-juggler/"
    echo "To remove configuration: rm -rf ~/.config/remote-juggler"
}

# Main installation flow
main() {
    local version=""
    local prefix="${REMOTE_JUGGLER_PREFIX:-$DEFAULT_PREFIX}"
    local channel="${REMOTE_JUGGLER_CHANNEL:-beta}"
    local do_uninstall="false"
    VERIFY="true"

    # Parse arguments
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --version)
                version="$2"
                shift 2
                ;;
            --prefix)
                prefix="$2"
                shift 2
                ;;
            --channel)
                channel="$2"
                shift 2
                ;;
            --no-verify)
                VERIFY="false"
                shift
                ;;
            --uninstall)
                do_uninstall="true"
                shift
                ;;
            --help|-h)
                usage
                exit 0
                ;;
            *)
                die "Unknown option: $1"
                ;;
        esac
    done

    # Handle uninstall
    if [[ "$do_uninstall" == "true" ]]; then
        uninstall "$prefix"
        exit 0
    fi

    echo "${BOLD}RemoteJuggler Installer${RESET}"
    echo ""

    # Check dependencies
    check_dependencies

    # Detect platform
    local platform
    platform=$(detect_platform)
    info "Detected platform: ${platform}"

    # Darwin is not yet supported with native binaries
    if [[ "$platform" == darwin-* ]]; then
        warn "Native macOS binaries are not yet available."
        echo ""
        echo "Install via alternative methods:"
        echo "  npm install -g @tummycrypt/remote-juggler"
        echo "  nix profile install github:Jesssullivan/remote-juggler  # Linux only"
        echo ""
        echo "Or build from source (requires Chapel 2.7+):"
        echo "  git clone ${REPO_URL}.git && cd remote-juggler && just release"
        exit 1
    fi

    # Get version
    if [[ -z "$version" ]]; then
        version="${REMOTE_JUGGLER_VERSION:-}"
    fi
    if [[ -z "$version" ]]; then
        info "Fetching latest ${channel} version..."
        version=$(get_latest_version "$channel")
    fi
    info "Installing version: ${version}"

    # Construct download URL for bare binary
    local binary_name="${PROGRAM_NAME}-${platform}"
    local binary_url="${REPO_URL}/releases/download/v${version}/${binary_name}"
    local checksum_url="${binary_url}.sha256"
    local checksums_url="${REPO_URL}/releases/download/v${version}/SHA256SUMS.txt"

    # Create temp directory
    local tmpdir
    tmpdir=$(mktemp -d)
    trap "rm -rf '$tmpdir'" EXIT

    # Download binary
    local binary_file="${tmpdir}/${binary_name}"
    download "$binary_url" "$binary_file" || die "Failed to download ${binary_name} from ${binary_url}"

    # Download and verify checksums
    local checksums="${tmpdir}/SHA256SUMS.txt"
    if download "$checksums_url" "$checksums" 2>/dev/null; then
        verify_checksum "$binary_file" "$checksums"
    elif download "$checksum_url" "${tmpdir}/checksum.sha256" 2>/dev/null; then
        # Fall back to per-file checksum
        local expected
        expected=$(awk '{print $1}' "${tmpdir}/checksum.sha256")
        local actual
        if command -v sha256sum &>/dev/null; then
            actual=$(sha256sum "$binary_file" | awk '{print $1}')
        elif command -v shasum &>/dev/null; then
            actual=$(shasum -a 256 "$binary_file" | awk '{print $1}')
        fi
        if [[ -n "${actual:-}" && "$expected" == "$actual" ]]; then
            success "Checksum verified: ${actual:0:16}..."
        elif [[ -n "${actual:-}" ]]; then
            die "Checksum mismatch! Expected: $expected, Got: $actual"
        fi
    else
        warn "Could not download checksums. Skipping verification."
    fi

    # Install
    install_binary "$binary_file" "$prefix"
    install_completions "$prefix"
    post_install "$prefix"
}

# Run main
main "$@"
