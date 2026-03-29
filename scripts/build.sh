#!/usr/bin/env bash
# Build netscan executable only
set -e

# Get the directory of the script
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Project root is one level up
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

BINARY=netscan

cd "$PROJECT_ROOT"

# Determine version
VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "1.0.0")
echo "Building version: $VERSION"

# Build the binary
if [ -f "$BINARY" ]; then
    echo "Removing old $BINARY..."
    rm -f $BINARY
fi

echo "Building netscan..."
go build -ldflags "-X github.com/kljama/netscan/internal/version.Version=$VERSION" -o $BINARY ./cmd/netscan

echo "Build complete: $BINARY"
