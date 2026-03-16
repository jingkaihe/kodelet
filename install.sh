#!/usr/bin/env bash
set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
NC='\033[0m'

GITHUB_REPO="jingkaihe/kodelet"
VERSION="${KODELET_VERSION:-latest}"
INSTALL_DIR="${KODELET_INSTALL_DIR:-$HOME/.local}"
INSTALL_METHOD="package"
INSTALL_METHOD_SOURCE="default"

OS=""
ARCH=""
VERSION_TAG=""
VERSION_NUMBER=""
PROFILE_FILE=""
EXPORT_CMD=""
NEEDS_SHELL_RELOAD=0
TEMP_DIR=""

usage() {
    cat <<'EOF'
Usage: install.sh [--package|--binary]

Installs Kodelet.

By default this uses package-based installation:
  - macOS: Homebrew
  - Linux: .deb or .rpm package

Options:
  --package   Force package-based installation
  --binary    Force standalone binary installation
  -h, --help  Show this help message

Environment:
  KODELET_VERSION      Version to install (default: latest)
  KODELET_INSTALL_DIR  Binary install prefix for --binary (default: ~/.local)
EOF
}

cleanup() {
    if [ -n "$TEMP_DIR" ] && [ -d "$TEMP_DIR" ]; then
        rm -rf "$TEMP_DIR"
    fi
}

trap cleanup EXIT

fail() {
    echo -e "${RED}$1${NC}" >&2
    exit 1
}

warn() {
    echo -e "${YELLOW}$1${NC}"
}

info() {
    echo -e "${GREEN}$1${NC}"
}

require_command() {
    if ! command -v "$1" >/dev/null 2>&1; then
        fail "Missing required command: $1"
    fi
}

parse_args() {
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --package)
                INSTALL_METHOD="package"
                INSTALL_METHOD_SOURCE="flag"
                ;;
            --binary)
                INSTALL_METHOD="binary"
                INSTALL_METHOD_SOURCE="flag"
                ;;
            -h|--help)
                usage
                exit 0
                ;;
            *)
                usage >&2
                fail "Unknown option: $1"
                ;;
        esac
        shift
    done
}

detect_platform() {
    OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
    case "$OS" in
        linux|darwin)
            ;;
        *)
            fail "Unsupported operating system: $OS. Kodelet currently supports linux and darwin (macOS)."
            ;;
    esac

    ARCH="$(uname -m)"
    case "$ARCH" in
        x86_64|amd64)
            ARCH="amd64"
            ;;
        arm64|aarch64)
            ARCH="arm64"
            ;;
        *)
            fail "Unsupported architecture: $ARCH. Kodelet currently supports x86_64/amd64 and arm64/aarch64."
            ;;
    esac
}

resolve_version_tag() {
    if [ -n "$VERSION_TAG" ]; then
        return 0
    fi

    if [ "$VERSION" = "latest" ]; then
        require_command curl
        VERSION_TAG="$(curl -fsSL "https://api.github.com/repos/${GITHUB_REPO}/releases/latest" | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"

        if [ -z "$VERSION_TAG" ]; then
            VERSION_TAG="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/${GITHUB_REPO}/releases/latest" | sed 's#.*/tag/##')"
        fi

        if [ -z "$VERSION_TAG" ]; then
            fail "Unable to resolve the latest Kodelet release version"
        fi
    else
        case "$VERSION" in
            v*)
                VERSION_TAG="$VERSION"
                ;;
            *)
                VERSION_TAG="v$VERSION"
                ;;
        esac
    fi

    VERSION_NUMBER="${VERSION_TAG#v}"
}

run_as_root() {
    if [ "$(id -u)" -eq 0 ]; then
        "$@"
        return 0
    fi

    if command -v sudo >/dev/null 2>&1; then
        sudo "$@"
        return 0
    fi

    return 1
}

detect_shell_profile() {
    local shell_type
    shell_type="$(basename "${SHELL:-sh}")"
    EXPORT_CMD=""

    case "$shell_type" in
        bash)
            PROFILE_FILE="$HOME/.bashrc"
            if [ "$OS" = "darwin" ] && [ -f "$HOME/.bash_profile" ]; then
                PROFILE_FILE="$HOME/.bash_profile"
            fi
            ;;
        zsh)
            PROFILE_FILE="$HOME/.zshrc"
            ;;
        fish)
            PROFILE_FILE="$HOME/.config/fish/config.fish"
            EXPORT_CMD="set -gx PATH \$PATH $INSTALL_DIR/bin"
            ;;
        *)
            PROFILE_FILE="$HOME/.profile"
            ;;
    esac

    if [ -z "$EXPORT_CMD" ]; then
        EXPORT_CMD="export PATH=\$PATH:$INSTALL_DIR/bin"
    fi
}

install_binary() {
    local download_url

    require_command curl
    resolve_version_tag

    mkdir -p "$INSTALL_DIR/bin"
    download_url="https://github.com/${GITHUB_REPO}/releases/download/${VERSION_TAG}/kodelet-${OS}-${ARCH}"

    echo "Downloading standalone binary from: $download_url"
    curl -fL "$download_url" -o "$INSTALL_DIR/bin/kodelet"
    chmod +x "$INSTALL_DIR/bin/kodelet"

    if [ -d "/usr/local/bin" ] && [ -w "/usr/local/bin" ]; then
        ln -sf "$INSTALL_DIR/bin/kodelet" /usr/local/bin/kodelet
        info "Kodelet installed successfully via standalone binary"
        echo "You can now run: kodelet"
        return 0
    fi

    warn "Unable to create symlink in /usr/local/bin"
    detect_shell_profile

    if echo ":${PATH}:" | grep -q ":${INSTALL_DIR}/bin:"; then
        info "${INSTALL_DIR}/bin is already in your PATH"
    else
        mkdir -p "$(dirname "$PROFILE_FILE")"
        touch "$PROFILE_FILE"

        if ! grep -Fq "$EXPORT_CMD" "$PROFILE_FILE" 2>/dev/null; then
            printf '\n# Added by Kodelet installer\n%s\n' "$EXPORT_CMD" >> "$PROFILE_FILE"
            NEEDS_SHELL_RELOAD=1
            info "Added Kodelet to your PATH in $PROFILE_FILE"
            echo "Run 'source $PROFILE_FILE' to update your current session or restart your terminal."
        else
            info "$PROFILE_FILE already contains the Kodelet PATH entry"
        fi
    fi

    info "Kodelet installed successfully at: $INSTALL_DIR/bin/kodelet"
}

detect_linux_package_format() {
    if command -v dpkg >/dev/null 2>&1 || command -v apt-get >/dev/null 2>&1 || command -v apt >/dev/null 2>&1; then
        echo "deb"
        return 0
    fi

    if command -v dnf >/dev/null 2>&1 || command -v yum >/dev/null 2>&1 || command -v zypper >/dev/null 2>&1 || command -v rpm >/dev/null 2>&1; then
        echo "rpm"
        return 0
    fi

    return 1
}

install_linux_package() {
    local package_format
    local asset_name
    local download_url
    local package_path

    require_command curl
    resolve_version_tag

    package_format="$(detect_linux_package_format)" || return 1
    asset_name="kodelet_${VERSION_NUMBER}_linux_${ARCH}.${package_format}"
    download_url="https://github.com/${GITHUB_REPO}/releases/download/${VERSION_TAG}/${asset_name}"

    TEMP_DIR="$(mktemp -d)"
    package_path="${TEMP_DIR}/${asset_name}"

    echo "Downloading package from: $download_url"
    curl -fL "$download_url" -o "$package_path"

    if [ "$package_format" = "deb" ]; then
        if command -v apt-get >/dev/null 2>&1; then
            run_as_root apt-get install -y "$package_path" || return 1
        elif command -v apt >/dev/null 2>&1; then
            run_as_root apt install -y "$package_path" || return 1
        elif command -v dpkg >/dev/null 2>&1; then
            run_as_root dpkg -i "$package_path" || return 1
        else
            return 1
        fi
    else
        if command -v dnf >/dev/null 2>&1; then
            run_as_root dnf install -y "$package_path" || return 1
        elif command -v yum >/dev/null 2>&1; then
            run_as_root yum install -y "$package_path" || return 1
        elif command -v zypper >/dev/null 2>&1; then
            run_as_root zypper --non-interactive install "$package_path" || return 1
        elif command -v rpm >/dev/null 2>&1; then
            run_as_root rpm -Uvh "$package_path" || return 1
        else
            return 1
        fi
    fi

    info "Kodelet installed successfully via ${package_format} package"
    echo "You can now run: kodelet"
}

install_macos_package() {
    require_command brew

    if [ "$VERSION" != "latest" ]; then
        warn "Homebrew installs the latest Kodelet release only; cannot install ${VERSION} via package mode"
        return 1
    fi

    echo "Installing Kodelet via Homebrew"
    brew tap jingkaihe/kodelet

    if brew list --formula kodelet >/dev/null 2>&1; then
        brew upgrade jingkaihe/kodelet/kodelet
    else
        brew install jingkaihe/kodelet/kodelet
    fi

    info "Kodelet installed successfully via Homebrew"
    echo "You can now run: kodelet"
}

install_package() {
    case "$OS" in
        darwin)
            install_macos_package
            ;;
        linux)
            install_linux_package
            ;;
        *)
            return 1
            ;;
    esac
}

show_next_steps() {
    echo -e "${BLUE}Setting up Kodelet...${NC}"

    if [ -z "${ANTHROPIC_API_KEY:-}" ]; then
        echo
        echo "Next steps:"
        echo

        if [ "$NEEDS_SHELL_RELOAD" -eq 1 ] && [ -n "$PROFILE_FILE" ]; then
            echo -e "  1. Update your shell environment:"
            echo -e "     ${GREEN}source $PROFILE_FILE${NC}"
            echo
            echo -e "  2. Initialize Kodelet:"
            echo -e "     ${GREEN}kodelet setup${NC}"
        else
            echo -e "  1. Initialize Kodelet:"
            echo -e "     ${GREEN}kodelet setup${NC}"
        fi
        echo
    else
        echo -e "${GREEN}ANTHROPIC_API_KEY already set. Skipping initialization.${NC}"
        echo -e "${BLUE}You can run 'kodelet setup' manually if you want to change configuration.${NC}"
    fi
}

main() {
    local package_status

    parse_args "$@"
    detect_platform

    echo -e "${BLUE}Kodelet Installer${NC}"
    echo "==============================="
    info "Detected OS: $OS"
    info "Detected architecture: $ARCH"
    info "Requested version: $VERSION"

    if [ "$INSTALL_METHOD" = "package" ]; then
        info "Preferred installation method: package"

        set +e
        install_package
        package_status=$?
        set -e

        if [ "$package_status" -ne 0 ]; then
            if [ "$INSTALL_METHOD_SOURCE" = "flag" ]; then
                fail "Package-based installation failed"
            fi

            warn "Package-based installation is unavailable on this system; falling back to standalone binary install"
            install_binary
        fi
    else
        info "Preferred installation method: binary"
        install_binary
    fi

    echo -e "${BLUE}Installation complete!${NC}"
    show_next_steps
}

main "$@"
