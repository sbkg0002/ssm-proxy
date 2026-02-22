# GitHub Actions Troubleshooting Guide

This guide helps troubleshoot common issues with the GitHub Actions workflow for ssm-proxy.

## Common Issues

### Issue 1: "directory not found" Error

**Error:**
```
stat /Users/runner/work/ssm-proxy/ssm-proxy/cmd/ssm-proxy: directory not found
```

**Causes:**
1. Repository checkout failed or incomplete
2. Working directory not set correctly
3. Repository structure mismatch

**Solutions:**

#### Solution A: Verify Repository Structure Locally
```bash
# Clone a fresh copy to simulate GitHub Actions
cd /tmp
git clone https://github.com/sbkg0002/ssm-proxy.git
cd ssm-proxy

# Verify structure
ls -la cmd/ssm-proxy/
ls -la go.mod
```

If the structure is correct locally, the issue is in GitHub Actions configuration.

#### Solution B: Test Build Locally
Use the provided test script to simulate GitHub Actions:
```bash
./scripts/test-github-build.sh
```

This will:
- Verify project structure
- Download dependencies
- Run tests
- Build for all platforms
- Create tarballs and checksums

#### Solution C: Check GitHub Actions Workflow

The workflow should have proper checkout:
```yaml
- name: Checkout code
  uses: actions/checkout@v4
  with:
    fetch-depth: 0  # Important: fetch full history
```

#### Solution D: Add Debug Steps

Add debugging to the workflow (already included in our workflow):
```yaml
- name: Verify project structure
  run: |
    echo "Current directory:"
    pwd
    echo ""
    echo "Directory structure:"
    ls -la
    echo ""
    echo "Checking for cmd/ssm-proxy:"
    ls -la cmd/
```

### Issue 2: Go Module Not Found

**Error:**
```
go: cannot find main module
```

**Solution:**
Ensure `go.mod` exists in repository root:
```bash
# Verify go.mod
cat go.mod | head -5
```

### Issue 3: Build Fails with Missing Dependencies

**Error:**
```
package github.com/gorilla/websocket: cannot find package
```

**Solution:**
Run dependency download before build:
```yaml
- name: Download dependencies
  run: go mod download

- name: Verify dependencies
  run: go mod verify
```

### Issue 4: Version Detection Fails

**Error:**
```
VERSION is empty or 'dev'
```

**Solution:**
Check git tags and history:
```bash
# List tags
git tag -l

# Check if tag exists
git describe --tags --exact-match HEAD

# Fallback to commit hash
git describe --tags --always --dirty
```

## Debugging Locally

### 1. Simulate GitHub Actions Environment

```bash
# Create temp directory
mkdir -p /tmp/github-test
cd /tmp/github-test

# Clone repository
git clone https://github.com/sbkg0002/ssm-proxy.git
cd ssm-proxy

# Run build steps manually
go mod download
go mod verify
go test -v ./...

# Build binary
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build \
  -trimpath \
  -ldflags="-s -w" \
  -o dist/ssm-proxy-darwin-arm64 \
  ./cmd/ssm-proxy

# Test binary
./dist/ssm-proxy-darwin-arm64 version
```

### 2. Use Test Script

```bash
# Run the test build script
./scripts/test-github-build.sh

# This will simulate the entire GitHub Actions process
```

### 3. Check Repository State

```bash
# Verify all files are committed
git status

# Check what will be pushed
git log --oneline -5

# Verify remote
git remote -v
```

## Pre-Push Checklist

Before pushing code that will trigger GitHub Actions:

- [ ] All files committed: `git status`
- [ ] Local build works: `make build-all`
- [ ] Tests pass: `make test`
- [ ] Test script passes: `./scripts/test-github-build.sh`
- [ ] go.mod is up to date: `go mod tidy`
- [ ] Version tags are correct (for releases): `git tag -l`

## Workflow File Structure

Our workflow has 4 jobs:

1. **test** - Runs tests on macOS
2. **build** - Builds binaries for multiple architectures
3. **release** - Creates GitHub release (only on tags)
4. **notify** - Sends notification (only on tags)

### Job Dependencies

```
test → build → release → notify
```

If `test` fails, `build` won't run.
If `build` fails, `release` won't run.

## Checking GitHub Actions Logs

1. Go to: https://github.com/sbkg0002/ssm-proxy/actions
2. Click on the failed workflow run
3. Click on the failed job (e.g., "Build")
4. Expand the failed step
5. Look for error messages in red

## Common Error Messages and Solutions

### "Error: Process completed with exit code 2"

**Meaning:** Build failed

**Solution:** Check the build logs for compilation errors

### "Error: Resource not accessible by integration"

**Meaning:** Missing GitHub token permissions

**Solution:** Check repository settings → Actions → General → Workflow permissions

### "Error: HttpError: Resource not accessible by integration"

**Meaning:** Release creation failed due to permissions

**Solution:** 
1. Go to repository Settings
2. Actions → General
3. Set "Workflow permissions" to "Read and write permissions"
4. Check "Allow GitHub Actions to create and approve pull requests"

### "Error: Release already exists"

**Meaning:** Trying to create a release for an existing tag

**Solution:**
1. Delete the existing release (if needed)
2. Delete the tag: `git tag -d v1.0.0 && git push origin :v1.0.0`
3. Create new tag: `git tag v1.0.0 && git push origin v1.0.0`

## Manual Build Process

If GitHub Actions continues to fail, you can build and release manually:

```bash
# Build all platforms
make build-all

# Create tarballs
cd dist
for BINARY in ssm-proxy-darwin-*; do
  PLATFORM=$(echo $BINARY | sed 's/ssm-proxy-//')
  tar -czf "ssm-proxy-v1.0.0-${PLATFORM}.tar.gz" "${BINARY}"
  shasum -a 256 "ssm-proxy-v1.0.0-${PLATFORM}.tar.gz" > "ssm-proxy-v1.0.0-${PLATFORM}.tar.gz.sha256"
done

# Upload to GitHub Releases manually
# Go to: https://github.com/sbkg0002/ssm-proxy/releases/new
# - Tag: v1.0.0
# - Title: Release v1.0.0
# - Description: Release notes
# - Attach: All .tar.gz and .sha256 files
```

## Getting Help

If issues persist:

1. **Check GitHub Actions Status**: https://www.githubstatus.com/
2. **Review Workflow File**: `.github/workflows/release.yml`
3. **Check Repository Settings**: Ensure Actions are enabled
4. **Verify Permissions**: Check workflow permissions in settings

## Workflow Modification Tips

### To skip tests temporarily:
```yaml
- name: Run tests
  if: false  # Temporarily disable
  run: go test -v -race ./...
```

### To run workflow manually:
Add to workflow triggers:
```yaml
on:
  workflow_dispatch:  # Enables manual trigger
```

Then go to Actions tab → Select workflow → Run workflow

### To test specific platform only:
Modify the matrix:
```yaml
strategy:
  matrix:
    include:
      - goos: darwin
        goarch: arm64  # Remove amd64 for faster testing
```

## Debug Mode

To enable maximum debugging, add to any step:
```yaml
- name: Debug step
  run: |
    set -x  # Print all commands
    echo "Debug information"
    env | sort  # Print all environment variables
    pwd
    ls -la
```

## Contact

For persistent issues, create an issue on GitHub:
https://github.com/sbkg0002/ssm-proxy/issues

Include:
- Full error message
- GitHub Actions run URL
- Steps to reproduce
- Local build results
