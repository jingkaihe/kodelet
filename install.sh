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

    # Detect shell type and appropriate profile file
    SHELL_TYPE=$(basename "$SHELL")
    case "$SHELL_TYPE" in
        bash)
            PROFILE_FILE="$HOME/.bashrc"
            # On macOS, bash might use .bash_profile instead
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

    # Default export command for bash/zsh/others
    if [ -z "$EXPORT_CMD" ]; then
        EXPORT_CMD="export PATH=\$PATH:$INSTALL_DIR/bin"
    fi

    # Check if the path is already in the profile
    if [ -f "$PROFILE_FILE" ] && grep -q "$INSTALL_DIR/bin" "$PROFILE_FILE"; then
        echo -e "${GREEN}Path already in $PROFILE_FILE${NC}"
    else
        # Add the PATH export to shell profile
        echo "" >> "$PROFILE_FILE"
        echo "# Added by Kodelet installer" >> "$PROFILE_FILE"
        echo "$EXPORT_CMD" >> "$PROFILE_FILE"
        echo -e "${GREEN}Added Kodelet to your PATH in $PROFILE_FILE${NC}"
        echo "Run 'source $PROFILE_FILE' to update your current session or restart your terminal."
    fi

    echo -e "${GREEN}Kodelet installed successfully at: $INSTALL_DIR/bin/kodelet${NC}"
fi

echo -e "${BLUE}Installation complete!${NC}"

echo -e "${BLUE}Setting up Kodelet...${NC}"
if [ -z "${ANTHROPIC_API_KEY}" ]; then
  "./bin/kodelet" init
else
  echo -e "${GREEN}✅ ANTHROPIC_API_KEY already set. Skipping initialization.${NC}"
  echo -e "${BLUE}You can run 'kodelet init' manually if you want to change configuration.${NC}"
fi
