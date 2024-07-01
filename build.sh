#!/bin/bash

set -e

# Set variables
CLI_NAME="fly"
REPO_NAME="flywp/server-cli"
VERSION=$(git describe --tags --always --dirty)
COMMIT_HASH=$(git rev-parse HEAD)
BUILD_DATE=$(date -u +"%Y-%m-%d")
LDFLAGS="-X github.com/${REPO_NAME}/internal/version.Version=${VERSION} -X github.com/${REPO_NAME}/internal/version.CommitHash=${COMMIT_HASH} -X github.com/${REPO_NAME}/internal/version.BuildDate=${BUILD_DATE}"

# Build function
build() {
    local GOOS=$1
    local GOARCH=$2
    local OUTPUT="${CLI_NAME}-${GOOS}-${GOARCH}"

    echo "Building for ${GOOS}/${GOARCH}..."
    GOOS=${GOOS} GOARCH=${GOARCH} go build -ldflags "${LDFLAGS}" -o "build/${OUTPUT}" .
    echo "Done building ${OUTPUT}"

    create_archive "${OUTPUT}"
}

# Create tar.gz archive function
create_archive() {
    local OUTPUT=$1

    echo "Creating archive for ${OUTPUT}..."
    tar -czvf "build/${OUTPUT}.tar.gz" -C build "${OUTPUT}"
    echo "Done creating archive ${OUTPUT}.tar.gz"
}

# Clean build directory
rm -rf build

# Create build directory if not exists
mkdir -p build

# Build for different platforms
build linux amd64
build linux arm64

echo "All builds completed!"
