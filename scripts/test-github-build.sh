#!/bin/bash
#
# Test GitHub Actions Build Process Locally
#
# This script simulates the GitHub Actions build process to help debug
# issues before pushing to the repository.
#

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${GREEN}✓${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}⚠${NC} $1"
}

log_error() {
    echo -e "${RED}✗${NC} $1"
}

log_header() {
    echo ""
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${BLUE}$1${NC}"
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
}

# Get script directory and project root
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PROJECT_ROOT="$( cd "${SCRIPT_DIR}/.." && pwd )"

log_header "GitHub Actions Build Simulation"

# Change to project root
cd "${PROJECT_ROOT}"
log_info "Project root: ${PROJECT_ROOT}"

# Step 1: Verify project structure
log_header "Step 1: Verify Project Structure"

if [ ! -f "go.mod" ]; then
    log_error "go.mod not found"
    exit 1
fi
log_info "go.mod found"

if [ ! -d "cmd/ssm-proxy" ]; then
    log_error "cmd/ssm-proxy directory not found"
    exit 1
fi
log_info "cmd/ssm-proxy directory found"

echo ""
echo "Project structure:"
ls -la cmd/
echo ""

# Step 2: Get version
log_header "Step 2: Get Version Information"

if git rev-parse --git-dir > /dev/null 2>&1; then
    if git describe --tags --exact-match HEAD 2>/dev/null; then
        VERSION=$(git describe --tags --exact-match HEAD)
    else
        VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
    fi
    COMMIT=$(git rev-parse --short HEAD)
else
    VERSION="dev"
    COMMIT="unknown"
fi

BUILD_TIME=$(date -u '+%Y-%m-%dT%H:%M:%SZ')

log_info "Version: ${VERSION}"
log_info "Commit: ${COMMIT}"
log_info "Build time: ${BUILD_TIME}"

# Step 3: Download dependencies
log_header "Step 3: Download Dependencies"

go mod download
log_info "Dependencies downloaded"

go mod verify
log_info "Dependencies verified"

# Step 4: Run tests
log_header "Step 4: Run Tests"

log_warn "Running tests (this may take a moment)..."
if go test -v -race ./... 2>&1 | tee /tmp/test-output.log; then
    log_info "Tests passed"
else
    log_error "Tests failed"
    echo ""
    echo "Test output:"
    cat /tmp/test-output.log
    exit 1
fi

# Step 5: Build for multiple platforms
log_header "Step 5: Build Binaries"

# Clean previous builds
rm -rf dist
mkdir -p dist

# Build for darwin-amd64
log_info "Building for darwin-amd64..."
GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags="-s -w -X 'main.version=${VERSION}' -X 'main.commit=${COMMIT}' -X 'main.buildTime=${BUILD_TIME}'" \
    -o "dist/ssm-proxy-darwin-amd64" \
    ./cmd/ssm-proxy

if [ ! -f "dist/ssm-proxy-darwin-amd64" ]; then
    log_error "darwin-amd64 binary was not created"
    exit 1
fi
log_info "darwin-amd64 binary created ($(du -h dist/ssm-proxy-darwin-amd64 | cut -f1))"

# Build for darwin-arm64
log_info "Building for darwin-arm64..."
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags="-s -w -X 'main.version=${VERSION}' -X 'main.commit=${COMMIT}' -X 'main.buildTime=${BUILD_TIME}'" \
    -o "dist/ssm-proxy-darwin-arm64" \
    ./cmd/ssm-proxy

if [ ! -f "dist/ssm-proxy-darwin-arm64" ]; then
    log_error "darwin-arm64 binary was not created"
    exit 1
fi
log_info "darwin-arm64 binary created ($(du -h dist/ssm-proxy-darwin-arm64 | cut -f1))"

# Step 6: Create tarballs and checksums
log_header "Step 6: Create Release Artifacts"

cd dist

for BINARY in ssm-proxy-darwin-amd64 ssm-proxy-darwin-arm64; do
    PLATFORM=$(echo $BINARY | sed 's/ssm-proxy-//')
    TARBALL="ssm-proxy-${VERSION}-${PLATFORM}.tar.gz"

    log_info "Creating tarball: ${TARBALL}"
    tar -czf "${TARBALL}" "${BINARY}"

    log_info "Creating checksum: ${TARBALL}.sha256"
    shasum -a 256 "${TARBALL}" > "${TARBALL}.sha256"
done

cd ..

# Step 7: Verify artifacts
log_header "Step 7: Verify Artifacts"

echo ""
echo "Build artifacts:"
ls -lh dist/
echo ""

# Test the binary
log_info "Testing binary..."
if ./dist/ssm-proxy-darwin-arm64 version; then
    log_info "Binary executes successfully"
else
    log_error "Binary failed to execute"
    exit 1
fi

# Step 8: Summary
log_header "Build Summary"

echo ""
log_info "All builds completed successfully!"
echo ""
echo "Artifacts created:"
for file in dist/*; do
    echo "  - $(basename $file) ($(du -h $file | cut -f1))"
done
echo ""
echo "These artifacts match what would be created by GitHub Actions."
echo ""

# Step 9: Cleanup option
log_header "Cleanup"

echo ""
read -p "Do you want to clean up build artifacts? (y/N): " -n 1 -r
echo ""
if [[ $REPLY =~ ^[Yy]$ ]]; then
    rm -rf dist
    log_info "Build artifacts cleaned up"
else
    log_info "Build artifacts preserved in dist/"
fi

echo ""
log_info "Test build complete!"
echo ""
