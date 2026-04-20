#!/bin/bash

# Multi-platform release script for beba
# Usage: ./scripts/release.sh [version_tag]

set -e

# Ensure clean worktree
if [ -n "$(git status --porcelain)" ]; then
    echo "Error: Working directory is not clean. Please commit or stash changes before releasing."
    echo "Uncommitted changes:"
    git status --short
    exit 1
fi

# Read current version from VERSION file
if [ ! -f VERSION ]; then
    echo "0.0.0" > VERSION
fi
CURRENT_VERSION=$(cat VERSION)
IFS='.' read -r MAJOR MINOR PATCH <<< "$CURRENT_VERSION"

# Determine increment level (major, minor, patch)
LEVEL=${1:-patch}

case $LEVEL in
    major)
        MAJOR=$((MAJOR + 1))
        MINOR=0
        PATCH=0
        ;;
    minor)
        MINOR=$((MINOR + 1))
        PATCH=0
        ;;
    patch)
        PATCH=$((PATCH + 1))
        ;;
    *)
        echo "Unknown level: $LEVEL. Use 'major', 'minor', or 'patch' (default)."
        exit 1
        ;;
esac

NEW_VERSION="${MAJOR}.${MINOR}.${PATCH}"
VERSION="v${NEW_VERSION}"

echo "Release automation for $BINARY_NAME"
echo "-----------------------------------"
echo "Current version: $CURRENT_VERSION"
echo "Target version:  $NEW_VERSION ($LEVEL)"
echo

read -p "Proceed with release $VERSION? (y/n) " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo "Release aborted."
    exit 0
fi

# Update VERSION file immediately so build logic uses it
echo "$NEW_VERSION" > VERSION

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

# Extract release notes from CHANGELOG.md
echo "Extracting release notes from CHANGELOG.md..."
VERSION_CLEAN=$(echo "$VERSION" | sed 's/^v//')
# Extract everything between ## [VERSION] and the next ## [ heading
# We use a temporary file to store the notes
NOTES_FILE=$(mktemp)
sed -n "/^## \[$VERSION_CLEAN\]/,/^## \[/p" CHANGELOG.md | sed '1d;$d' > "$NOTES_FILE"

# If notes are empty, try with the 'v' prefix in the changelog
if [ ! -s "$NOTES_FILE" ]; then
    sed -n "/^## \[$VERSION\]/,/^## \[/p" CHANGELOG.md | sed '1d;$d' > "$NOTES_FILE"
fi

if [ -s "$NOTES_FILE" ]; then
    echo "Release notes extracted successfully."
else
    echo "Warning: Could not find release notes for $VERSION in CHANGELOG.md"
    echo "Defaulting to 'Release $VERSION'"
    echo "Release $VERSION" > "$NOTES_FILE"
fi

# Optional: Git tagging
read -p "Do you want to tag this release in Git? (y/n) " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    if git rev-parse "$VERSION" >/dev/null 2>&1; then
        echo "Tag $VERSION already exists."
    else
        git tag -a "$VERSION" -m "Release $VERSION"
        echo "Tag $VERSION created locally."
    fi
    
    read -p "Do you want to push the tag to origin? (y/n) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        git push origin "$VERSION"
        echo "Tag $VERSION pushed to origin."
        
        # GitHub Release creation
        read -p "Do you want to create a GitHub release? (y/n) " -n 1 -r
        echo
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            if ! command -v gh &> /dev/null; then
                echo "Error: 'gh' CLI not found. Please install it to create GitHub releases."
            else
                echo "Creating GitHub release $VERSION..."
                gh release create "$VERSION" "$OUT_DIR/*$VERSION*" --title "$VERSION" --notes-file "$NOTES_FILE"
                echo "GitHub release created successfully!"
            fi
        fi
    fi
fi

rm "$NOTES_FILE"
echo "Done!"
