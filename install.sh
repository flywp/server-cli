#!/bin/bash
#
# Install the latest release of fly. Run it as root:
#   curl -fsSL https://raw.githubusercontent.com/flywp/server-cli/main/install.sh | sudo bash

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
    status=$(curl -sSL --connect-timeout 10 --max-time 60 --retry 3 -H "User-Agent: FlyWP-Installer" -o "$response" -w '%{http_code}' \
        "https://api.github.com/repos/${REPO}/releases/latest") ||
        error_exit "Failed to access the GitHub API. Please check your internet connection."

    case "$status" in
        200) ;;
        403 | 429) error_exit "GitHub API rate limit exceeded. Please try again later." ;;
        *) error_exit "The GitHub API answered with HTTP $status." ;;
    esac

    # The API answers with JSON on one line. Take the first tag_name.
    TAG_NAME=$(grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' "$response" | head -n 1 | sed 's/.*"\([^"]*\)"$/\1/') || true
    if [ -z "$TAG_NAME" ]; then
        error_exit "Failed to determine the latest release version."
    fi

    DOWNLOAD_BASE="https://github.com/${REPO}/releases/download/${TAG_NAME}"
    info_msg "Latest release version: $TAG_NAME"
}

download_release() {
    info_msg "Downloading ${NAME}.tar.gz..."
    curl -fsSL --connect-timeout 10 --max-time 300 --retry 3 -o "$TEMP_DIR/${NAME}.tar.gz" "${DOWNLOAD_BASE}/${NAME}.tar.gz" ||
        error_exit "Failed to download ${DOWNLOAD_BASE}/${NAME}.tar.gz."
    info_msg "Download completed successfully."
}

# Examine the download with the checksum file of the release. The file has
# one line for each archive: "<sha256>  <file name>".
verify_download() {
    info_msg "Verifying the download with checksums.txt..."

    curl -fsSL --connect-timeout 10 --max-time 60 --retry 3 -o "$TEMP_DIR/checksums.txt" "${DOWNLOAD_BASE}/checksums.txt" ||
        error_exit "Failed to download checksums.txt of ${TAG_NAME}. The download cannot be checked, so it is not installed."

    # The same rules as fly update: an optional "*" (binary mode) before the
    # name, CRLF line ends. Two different sums for the file fail the check.
    if ! (cd "$TEMP_DIR" && awk -v f="${NAME}.tar.gz" '{ sub(/\r$/, "") } $2 == f || $2 == "*" f { print $1 "  " f }' checksums.txt | sha256sum -c --status -); then
        error_exit "The checksum of ${NAME}.tar.gz does not agree with checksums.txt. The download is not installed."
    fi

    info_msg "Checksum verified."
}

install_binary() {
    # Extract only the binary, and do not keep the owner from the archive:
    # a binary that root runs must not belong to a different user.
    tar --no-same-owner -xzf "$TEMP_DIR/${NAME}.tar.gz" -C "$TEMP_DIR" "$NAME" ||
        error_exit "The archive does not contain ${NAME}."
    if [ ! -f "$TEMP_DIR/$NAME" ] || [ -L "$TEMP_DIR/$NAME" ]; then
        error_exit "${NAME} in the archive is not a regular file."
    fi

    if [ -f "$AGENT_UNIT" ]; then
        install_for_agent
    else
        install_for_root
    fi

    command -v fly >/dev/null || error_exit "Installation failed: 'fly' command not found in PATH."

    success_msg "Installation of fly ${TAG_NAME} completed successfully!"
    info_msg "Verify with 'fly version'"
}

# Without the monitoring agent, fly is a root file in /usr/local/bin. A link
# or a file that is there is replaced; a link is never followed.
install_for_root() {
    info_msg "Installing to ${TARGET}..."

    local tmp
    tmp=$(mktemp "$(dirname "$TARGET")/.fly-update-XXXXXX") ||
        error_exit "Failed to create a temporary file next to ${TARGET}. Check your permissions."
    if ! { cat "$TEMP_DIR/$NAME" >"$tmp" && chmod 0755 "$tmp" && chown 0:0 "$tmp"; }; then
        rm -f "$tmp"
        error_exit "Failed to write the binary next to ${TARGET}."
    fi
    # A rename is atomic: a running fly never sees a partial binary. -T
    # replaces a link itself, not the file it points to.
    mv -Tf "$tmp" "$TARGET" || { rm -f "$tmp"; error_exit "Failed to install the binary to ${TARGET}."; }
}

# With the monitoring agent, the binary is ~<user>/.fly/bin/fly, the command
# of fly-agent.service, and /usr/local/bin/fly is a link to it. The folder
# belongs to the agent user, so root does not write in it: the user could put
# a link to a root file there. The agent user writes the new binary, and root
# only gives it the file on stdin.
install_for_agent() {
    local user home bin
    user=$(sed -n 's/^User=//p' "$AGENT_UNIT" | tail -n 1)
    bin=$(sed -n 's/^ExecStart=\([^ ]*\).*/\1/p' "$AGENT_UNIT" | tail -n 1)

    if [ -z "$user" ] || [ "$user" = "root" ]; then
        error_exit "${AGENT_UNIT} has no User= for the monitoring agent."
    fi
    home=$(getent passwd "$user" | cut -d: -f6) || true
    if [ -z "$home" ] || [ "$bin" != "$home/.fly/bin/fly" ]; then
        error_exit "${AGENT_UNIT} does not run ${home:-~$user}/.fly/bin/fly. The layout is not known, so fly is not installed."
    fi

    info_msg "Installing to ${bin} as ${user}..."
    # shellcheck disable=SC2016 # $1 is for the inner shell
    runuser -u "$user" -- sh -c 'set -e
        tmp=$(mktemp "$(dirname "$1")/.fly-update-XXXXXX")
        trap '"'"'rm -f "$tmp"'"'"' EXIT
        cat >"$tmp"
        chmod 0755 "$tmp"
        mv -f "$tmp" "$1"
        trap - EXIT' sh "$bin" <"$TEMP_DIR/$NAME" ||
        error_exit "Failed to install the binary to ${bin} as ${user}."

    # The CLI and the agent use the same binary. The old installer put a
    # separate file here; replace it with the link.
    ln -sfn "$bin" "${TARGET}.link-$$" && mv -Tf "${TARGET}.link-$$" "$TARGET" ||
        error_exit "fly is installed in ${bin}, but ${TARGET} could not be linked to it."

    # try-restart: an agent that an administrator stopped stays stopped.
    info_msg "Restarting the monitoring agent..."
    systemctl try-restart fly-agent ||
        error_exit "fly is installed, but the monitoring agent did not restart."
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
