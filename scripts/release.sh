#!/bin/bash

# Multi-platform release script for beba
# Usage: ./scripts/release.sh [version_tag]

set -e

VERSION=$1

if [ -z "$VERSION" ]; then
    echo "Usage: $0 [version_tag] (e.g., v1.0.0)"
    exit 1
fi

BINARY_NAME="beba"
OUT_DIR="release"

# Platforms and Architectures
PLATFORMS=("linux/amd64" "linux/arm64" "darwin/amd64" "darwin/arm64" "windows/amd64")

echo "Preparing release $VERSION..."

# Create release directory
mkdir -p "$OUT_DIR"

for PLATFORM in "${PLATFORMS[@]}"; do
    IFS="/" read -r OS ARCH <<< "$PLATFORM"
    
    OUTPUT_NAME="${BINARY_NAME}_${VERSION}_${OS}_${ARCH}"
    EXTENSION=""
    if [ "$OS" == "windows" ]; then
        EXTENSION=".exe"
    fi

    echo "Building for $OS/$ARCH..."
    
    # Build the binary
    # We use -ldflags to inject the version into main.Version
    GOOS=$OS GOARCH=$ARCH go build -ldflags "-X main.Version=$VERSION -s -w" -o "$OUT_DIR/$BINARY_NAME$EXTENSION" .

    # Package the binary
    pushd "$OUT_DIR" > /dev/null
    
    if [ "$OS" == "windows" ]; then
        ZIP_FILE="${OUTPUT_NAME}.zip"
        zip -q "$ZIP_FILE" "$BINARY_NAME$EXTENSION"
        rm "$BINARY_NAME$EXTENSION"
        echo "Created $ZIP_FILE"
    else
        TAR_FILE="${OUTPUT_NAME}.tar.gz"
        tar -czf "$TAR_FILE" "$BINARY_NAME$EXTENSION"
        rm "$BINARY_NAME$EXTENSION"
        echo "Created $TAR_FILE"
    fi
    
    popd > /dev/null
done

echo "Built all binaries in $OUT_DIR/"

# Optional: Git tagging
read -p "Do you want to tag this release in Git? (y/n) " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    git tag -a "$VERSION" -m "Release $VERSION"
    echo "Tag $VERSION created locally."
    read -p "Do you want to push the tag to origin? (y/n) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        git push origin "$VERSION"
        echo "Tag $VERSION pushed to origin."
    fi
fi

echo "Done!"
