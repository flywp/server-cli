#!/bin/bash
#
# Install the latest release of fly. Run it as root:
#   curl -sL https://raw.githubusercontent.com/flywp/server-cli/main/install.sh | sudo bash

set -euo pipefail

REPO="flywp/server-cli"
TARGET="/usr/local/bin/fly"
AGENT_UNIT="/etc/systemd/system/fly-agent.service"
TEMP_DIR=""

error_exit() {
    echo -e "\033[31mERROR: $1\033[0m" >&2
    exit 1
}

success_msg() {
    echo -e "\033[32m$1\033[0m"
}

info_msg() {
    echo -e "\033[34m$1\033[0m"
}

cleanup() {
    if [ -n "$TEMP_DIR" ]; then
        rm -rf "$TEMP_DIR"
    fi
}
trap cleanup EXIT

check_sudo() {
    if [ "$(id -u)" -ne 0 ]; then
        error_exit "This script requires sudo privileges. Please run with sudo."
    fi
}

# Determine OS and architecture
determine_platform() {
    info_msg "Detecting system platform..."

    if [ ! -f /etc/os-release ]; then
        error_exit "Cannot detect OS. This script only supports Ubuntu."
    fi
    # shellcheck disable=SC1091
    . /etc/os-release
    if [ "${ID:-}" != "ubuntu" ]; then
        error_exit "This script only supports Ubuntu. Detected OS: ${ID:-unknown}"
    fi
    info_msg "Detected Ubuntu version: ${VERSION_ID:-unknown}"

    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    case "$(uname -m)" in
        x86_64) ARCH="amd64" ;;
        aarch64 | arm64) ARCH="arm64" ;;
        *) error_exit "Unsupported architecture: $(uname -m). Only amd64 and arm64 are supported." ;;
    esac

    NAME="fly-${OS}-${ARCH}"
    info_msg "Using OS: $OS, Architecture: $ARCH"
}

# Get the tag of the latest release from the GitHub API
get_release_info() {
    info_msg "Fetching latest release information from GitHub..."

    local response="$TEMP_DIR/release.json"
    local status
    status=$(curl -sSL -H "User-Agent: FlyWP-Installer" -o "$response" -w '%{http_code}' \
        "https://api.github.com/repos/${REPO}/releases/latest") ||
        error_exit "Failed to access the GitHub API. Please check your internet connection."

    case "$status" in
        200) ;;
        403 | 429) error_exit "GitHub API rate limit exceeded. Please try again later." ;;
        *) error_exit "The GitHub API answered with HTTP $status." ;;
    esac

    # The API answers with one key on each line.
    TAG_NAME=$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$response" | head -n 1)
    if [ -z "$TAG_NAME" ]; then
        error_exit "Failed to determine the latest release version."
    fi

    DOWNLOAD_BASE="https://github.com/${REPO}/releases/download/${TAG_NAME}"
    info_msg "Latest release version: $TAG_NAME"
}

download_release() {
    info_msg "Downloading ${NAME}.tar.gz..."
    curl -fsSL -o "$TEMP_DIR/${NAME}.tar.gz" "${DOWNLOAD_BASE}/${NAME}.tar.gz" ||
        error_exit "Failed to download ${DOWNLOAD_BASE}/${NAME}.tar.gz."
    info_msg "Download completed successfully."
}

# Examine the download with the checksum file of the release. The file has
# one line for each archive: "<sha256>  <file name>".
verify_download() {
    info_msg "Verifying the download with checksums.txt..."

    curl -fsSL -o "$TEMP_DIR/checksums.txt" "${DOWNLOAD_BASE}/checksums.txt" ||
        error_exit "Failed to download checksums.txt of ${TAG_NAME}. The download cannot be checked, so it is not installed."

    if ! (cd "$TEMP_DIR" && grep " ${NAME}.tar.gz\$" checksums.txt | sha256sum -c --status -); then
        error_exit "The checksum of ${NAME}.tar.gz does not agree with checksums.txt. The download is not installed."
    fi

    info_msg "Checksum verified."
}

install_binary() {
    # Extract only the binary, and do not keep the owner from the archive:
    # a binary that root runs must not belong to a different user.
    tar --no-same-owner -xzf "$TEMP_DIR/${NAME}.tar.gz" -C "$TEMP_DIR" "$NAME" ||
        error_exit "The archive does not contain ${NAME}."

    # On a server with the monitoring agent, /usr/local/bin/fly is a link to
    # the binary of the agent (~fly/.fly/bin/fly). Install through the link and
    # keep the owner of the binary, so that the CLI and the agent use one binary.
    local dest="$TARGET" owner="0:0"
    if [ -L "$TARGET" ]; then
        dest=$(readlink -f "$TARGET")
        owner=$(stat -c '%u:%g' "$dest" 2>/dev/null || echo "0:0")
    fi

    info_msg "Installing to ${dest}..."
    install -m 0755 -o "${owner%%:*}" -g "${owner##*:}" "$TEMP_DIR/$NAME" "${dest}.new" ||
        error_exit "Failed to install the binary to ${dest}. Check your permissions."
    # A rename is atomic: a running fly never sees a partial binary.
    mv -f "${dest}.new" "$dest"

    if [ -f "$AGENT_UNIT" ]; then
        info_msg "Restarting the monitoring agent..."
        systemctl restart fly-agent || error_exit "fly is installed, but the monitoring agent did not restart."
    fi

    command -v fly >/dev/null || error_exit "Installation failed: 'fly' command not found in PATH."

    success_msg "Installation of fly ${TAG_NAME} completed successfully!"
    info_msg "Verify with 'fly version'"
}

main() {
    echo "===== FlyWP Server CLI Installer ====="

    check_sudo
    determine_platform

    TEMP_DIR=$(mktemp -d)
    get_release_info
    download_release
    verify_download
    install_binary
}

main
