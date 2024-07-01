#!/bin/bash

set -e

# Determine OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

if [ "$ARCH" == "x86_64" ]; then
    ARCH="amd64"
elif [[ "$ARCH" == "aarch64" || "$ARCH" == "arm64" ]]; then
    ARCH="arm64"
else
    echo "Unsupported architecture: $ARCH"
    exit 1
fi

# Get latest release from GitHub API
LATEST_RELEASE=$(curl -s https://api.github.com/repos/flywp/server-cli/releases/latest)
TAG_NAME=$(echo "$LATEST_RELEASE" | grep -oP '"tag_name": "\K(.*)(?=")')
DOWNLOAD_URL=$(echo "$LATEST_RELEASE" | grep -oP '"browser_download_url": "\K(.*'${OS}'-'${ARCH}'.tar.gz)(?=")')

if [ -z "$DOWNLOAD_URL" ]; then
    echo "No suitable binary found for OS: $OS and architecture: $ARCH"
    exit 1
fi

# Download the latest release
TEMP_DIR=$(mktemp -d)
DOWNLOAD_FILE="$TEMP_DIR/fly-$OS-$ARCH.tar.gz"

echo "Downloading $DOWNLOAD_URL..."
curl -L -o "$DOWNLOAD_FILE" "$DOWNLOAD_URL"

# Extract and install
echo "Extracting $DOWNLOAD_FILE..."
tar -xzf "$DOWNLOAD_FILE" -C "$TEMP_DIR"

echo "Installing to /usr/local/bin/fly..."
sudo mv "$TEMP_DIR/fly-$OS-$ARCH" /usr/local/bin/fly
sudo chmod +x /usr/local/bin/fly

# Clean up
rm -rf "$TEMP_DIR"

echo "Installation completed! Verify with 'fly version'."
