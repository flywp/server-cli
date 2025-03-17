#!/bin/bash

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

warning_msg() {
    echo -e "\033[33m$1\033[0m"
}

# Check if script is running with sudo privileges
check_sudo() {
    if [ "$(id -u)" -ne 0 ]; then
        error_exit "This script requires sudo privileges. Please run with sudo."
    fi
}

# Check for required commands and install if missing
check_dependencies() {
    
    # Check for Perl regex support
    if ! echo "test" | grep -P "test" &> /dev/null; then
        warning_msg "Perl regex support not detected. Installing..."
        
        # Install perl-compatible grep for Ubuntu
        apt-get install -y -qq grep
        
        # Check again after installation
        if ! echo "test" | grep -P "test" &> /dev/null; then
            warning_msg "Perl regex support still not available. Using alternative parsing method."
            USE_PERL_REGEX=false
        else
            USE_PERL_REGEX=true
        fi
    else
        USE_PERL_REGEX=true
    fi
}

# Determine OS and architecture
determine_platform() {
    info_msg "Detecting system platform..."
    
    # Check if running on Ubuntu
    if [ -f /etc/os-release ]; then
        . /etc/os-release
        if [[ "$ID" != "ubuntu" ]]; then
            error_exit "This script only supports Ubuntu. Detected OS: $ID"
        fi
        info_msg "Detected Ubuntu version: $VERSION_ID"
    else
        error_exit "Cannot detect OS. This script only supports Ubuntu."
    fi
    
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m)
    
    info_msg "Detected architecture: $ARCH"
    
    if [ "$ARCH" == "x86_64" ]; then
        ARCH="amd64"
    elif [[ "$ARCH" == "aarch64" || "$ARCH" == "arm64" ]]; then
        ARCH="arm64"
    else
        error_exit "Unsupported architecture: $ARCH. Only amd64 and arm64 are supported."
    fi
    
    info_msg "Using OS: $OS, Architecture: $ARCH"
}

# Get latest release from GitHub API
get_release_info() {
    info_msg "Fetching latest release information from GitHub..."
    
    # Use a temporary file for the API response
    GITHUB_API_RESPONSE=$(mktemp)
    
    # Add a user-agent to avoid rate limiting
    if ! curl -s -L -H "User-Agent: FlyWP-Installer" \
        https://api.github.com/repos/flywp/server-cli/releases/latest \
        -o "$GITHUB_API_RESPONSE"; then
        error_exit "Failed to access GitHub API. Please check your internet connection."
    fi
    
    # Check for rate limiting
    if grep -q "API rate limit exceeded" "$GITHUB_API_RESPONSE"; then
        error_exit "GitHub API rate limit exceeded. Please try again later or use a GitHub token."
    fi
    
    # Extract tag name with more robust methods
    if [ "$USE_PERL_REGEX" = true ]; then
        TAG_NAME=$(grep -oP '"tag_name":\s*"\K[^"]+' "$GITHUB_API_RESPONSE")
    else
        TAG_NAME=$(grep '"tag_name"' "$GITHUB_API_RESPONSE" | sed -E 's/.*"tag_name":\s*"([^"]+)".*/\1/')
    fi
    
    if [ -z "$TAG_NAME" ]; then
        # Fallback to a more basic approach
        TAG_NAME=$(grep "tag_name" "$GITHUB_API_RESPONSE" | cut -d'"' -f4)
    fi
    
    if [ -z "$TAG_NAME" ]; then
        error_exit "Failed to determine the latest release version."
    fi
    
    info_msg "Latest release version: $TAG_NAME"
    
    # Since we know the exact format of the release assets, construct the URL directly
    DOWNLOAD_URL="https://github.com/flywp/server-cli/releases/download/${TAG_NAME}/fly-linux-${ARCH}.tar.gz"
    
    # Clean up
    rm -f "$GITHUB_API_RESPONSE"
    
    info_msg "Download URL: $DOWNLOAD_URL"
}

# Download and verify the release
download_release() {
    info_msg "Creating temporary directory..."
    TEMP_DIR=$(mktemp -d)
    if [ ! -d "$TEMP_DIR" ]; then
        error_exit "Failed to create temporary directory."
    fi
    
    DOWNLOAD_FILE="$TEMP_DIR/fly-$OS-$ARCH.tar.gz"
    
    info_msg "Downloading latest release..."
    if ! curl -s -L -o "$DOWNLOAD_FILE" "$DOWNLOAD_URL"; then
        rm -rf "$TEMP_DIR"
        error_exit "Failed to download the release file."
    fi
    
    # Verify the downloaded file
    if [ ! -s "$DOWNLOAD_FILE" ]; then
        rm -rf "$TEMP_DIR"
        error_exit "Downloaded file is empty or corrupted."
    fi
    
    info_msg "Download completed successfully."
}

# Extract and install
install_binary() {
    info_msg "Extracting $DOWNLOAD_FILE..."
    if ! tar -xzf "$DOWNLOAD_FILE" -C "$TEMP_DIR"; then
        rm -rf "$TEMP_DIR"
        error_exit "Failed to extract the archive."
    fi
    
    # Look for the binary file
    BINARY_FILE=$(find "$TEMP_DIR" -type f -executable | head -n 1)
    
    if [ -z "$BINARY_FILE" ]; then
        # Fallback to expected name pattern
        BINARY_FILE="$TEMP_DIR/fly-$OS-$ARCH"
        
        if [ ! -f "$BINARY_FILE" ]; then
            # Try finding any file that might be the binary
            BINARY_FILE=$(find "$TEMP_DIR" -type f -name "fly*" | head -n 1)
        fi
        
        if [ -z "$BINARY_FILE" ]; then
            rm -rf "$TEMP_DIR"
            error_exit "Could not find the executable in the extracted archive."
        fi
    fi
    
    info_msg "Installing to /usr/local/bin/fly..."
    
    if ! mv "$BINARY_FILE" /usr/local/bin/fly; then
        rm -rf "$TEMP_DIR"
        error_exit "Failed to move the binary to /usr/local/bin/fly. Check your permissions."
    fi
    
    if ! chmod +x /usr/local/bin/fly; then
        rm -rf "$TEMP_DIR"
        error_exit "Failed to make the binary executable."
    fi
    
    # Clean up
    rm -rf "$TEMP_DIR"
    
    # Verify installation
    if ! command -v fly &> /dev/null; then
        error_exit "Installation failed: 'fly' command not found in PATH."
    fi
    
    success_msg "Installation completed successfully!"
    info_msg "Verify with 'fly version'"
}

main() {
    echo "===== FlyWP Server CLI Installer ====="
    
    # Check for sudo access
    check_sudo
    
    # Determine OS and architecture (and check for Ubuntu)
    determine_platform
    
    # Check for Perl regex support (only essential dependency check)
    check_dependencies
    
    # Get release information
    get_release_info
    
    # Download the release
    download_release
    
    # Install the binary
    install_binary
}

# Run the main function
main