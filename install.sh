#!/usr/bin/env bash
set -e

# Kodelet installation script
# Detects OS, architecture and platform to download the appropriate binary

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}Kodelet Installer${NC}"
echo "==============================="

# Detect OS
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
    linux)
        ;;
    darwin)
        ;;
    *)
        echo -e "${RED}Unsupported operating system: $OS${NC}"
        echo "Kodelet currently supports: linux, darwin (macOS)"
        exit 1
        ;;
esac

# Detect architecture
ARCH="$(uname -m)"
case "$ARCH" in
    x86_64)
        ARCH="amd64"
        ;;
    amd64)
        ;;
    arm64)
        ;;
    aarch64)
        ARCH="arm64"
        ;;
    *)
        echo -e "${RED}Unsupported architecture: $ARCH${NC}"
        echo "Kodelet currently supports: x86_64/amd64, arm64/aarch64"
        exit 1
        ;;
esac

# Version can be set via environment variable or default to latest
VERSION=${KODELET_VERSION:-"latest"}

echo -e "${GREEN}Detected OS: $OS${NC}"
echo -e "${GREEN}Detected architecture: $ARCH${NC}"
echo -e "${GREEN}Installing version: $VERSION${NC}"

# Create installation directory
INSTALL_DIR="$HOME/.kodelet"
mkdir -p "$INSTALL_DIR/bin"

# Download URL construction
if [ "$VERSION" = "latest" ]; then
    DOWNLOAD_URL="https://github.com/jingkaihe/kodelet/releases/latest/download/kodelet-$OS-$ARCH"
else
    DOWNLOAD_URL="https://github.com/jingkaihe/kodelet/releases/download/$VERSION/kodelet-$OS-$ARCH"
fi

echo "Downloading from: $DOWNLOAD_URL"

# Download binary
curl -L "$DOWNLOAD_URL" -o "$INSTALL_DIR/bin/kodelet"
chmod +x "$INSTALL_DIR/bin/kodelet"

# Create symlink in /usr/local/bin if possible, otherwise suggest adding to PATH
if [ -d "/usr/local/bin" ] && [ -w "/usr/local/bin" ]; then
    ln -sf "$INSTALL_DIR/bin/kodelet" /usr/local/bin/kodelet
    echo -e "${GREEN}Kodelet installed successfully!${NC}"
    echo "You can now run: kodelet"
else
    echo -e "${YELLOW}Unable to create symlink in /usr/local/bin${NC}"
    echo "Add Kodelet to your PATH by adding this line to your shell profile:"
    echo "export PATH=\$PATH:$INSTALL_DIR/bin"
    echo -e "${GREEN}Kodelet installed successfully at: $INSTALL_DIR/bin/kodelet${NC}"
fi

# Create config directory and sample config
CONFIG_DIR="$HOME/.kodelet"
mkdir -p "$CONFIG_DIR"

if [ ! -f "$CONFIG_DIR/config.yaml" ]; then
    cat > "$CONFIG_DIR/config.yaml" << EOF
# Kodelet configuration
model: "claude-3-7-sonnet-latest"
max_tokens: 8192
EOF
    echo "Created sample config at: $CONFIG_DIR/config.yaml"
fi

echo -e "${BLUE}Installation complete!${NC}"
echo "Remember to set your Anthropic API key:"
echo "export ANTHROPIC_API_KEY=\"your-key-here\""
