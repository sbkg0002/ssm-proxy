.PHONY: build build-release build-all test install uninstall clean lint fmt help

# Binary name
BINARY_NAME := ssm-proxy

# Version information
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

# Build flags
LDFLAGS := -ldflags "\
	-X 'main.version=$(VERSION)' \
	-X 'main.commit=$(COMMIT)' \
	-X 'main.buildTime=$(BUILD_TIME)'"

# Directories
BUILD_DIR := bin
DIST_DIR := dist

# Go parameters
GOCMD := go
GOBUILD := $(GOCMD) build
GOTEST := $(GOCMD) test
GOGET := $(GOCMD) get
GOMOD := $(GOCMD) mod
GOFMT := $(GOCMD) fmt

# Build targets
.DEFAULT_GOAL := build

## help: Display this help message
help:
	@echo "SSM Proxy - Makefile"
	@echo ""
	@echo "Available targets:"
	@echo "  build          - Build binary for current platform"
	@echo "  build-release  - Build optimized release binary for current platform"
	@echo "  build-all      - Build for all supported platforms (darwin-amd64, darwin-arm64)"
	@echo "  test           - Run tests"
	@echo "  test-coverage  - Run tests with coverage report"
	@echo "  install        - Install binary to /usr/local/bin (requires sudo)"
	@echo "  uninstall      - Remove binary from /usr/local/bin (requires sudo)"
	@echo "  clean          - Remove build artifacts"
	@echo "  lint           - Run golangci-lint"
	@echo "  fmt            - Format Go code"
	@echo "  deps           - Download dependencies"
	@echo "  tidy           - Tidy go.mod"
	@echo ""
	@echo "Version: $(VERSION)"
	@echo "Commit:  $(COMMIT)"
	@echo "Built:   $(BUILD_TIME)"

## build: Build binary for current platform
build:
	@echo "Building $(BINARY_NAME) $(VERSION)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/ssm-proxy
	@echo "✓ Built: $(BUILD_DIR)/$(BINARY_NAME)"

## build-release: Build optimized release binary
build-release:
	@echo "Building release binary $(VERSION)..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 $(GOBUILD) $(LDFLAGS) \
		-trimpath \
		-o $(BUILD_DIR)/$(BINARY_NAME) \
		./cmd/ssm-proxy
	@echo "✓ Built: $(BUILD_DIR)/$(BINARY_NAME)"

## build-all: Build for all supported macOS platforms
build-all: build-darwin-amd64 build-darwin-arm64
	@echo "✓ All builds complete"

## build-darwin-amd64: Build for macOS Intel
build-darwin-amd64:
	@echo "Building for darwin/amd64..."
	@mkdir -p $(DIST_DIR)
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 $(GOBUILD) $(LDFLAGS) \
		-trimpath \
		-o $(DIST_DIR)/$(BINARY_NAME)-darwin-amd64 \
		./cmd/ssm-proxy
	@echo "✓ Built: $(DIST_DIR)/$(BINARY_NAME)-darwin-amd64"

## build-darwin-arm64: Build for macOS Apple Silicon
build-darwin-arm64:
	@echo "Building for darwin/arm64..."
	@mkdir -p $(DIST_DIR)
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 $(GOBUILD) $(LDFLAGS) \
		-trimpath \
		-o $(DIST_DIR)/$(BINARY_NAME)-darwin-arm64 \
		./cmd/ssm-proxy
	@echo "✓ Built: $(DIST_DIR)/$(BINARY_NAME)-darwin-arm64"

## test: Run tests
test:
	@echo "Running tests..."
	$(GOTEST) -v -race ./...

## test-coverage: Run tests with coverage
test-coverage:
	@echo "Running tests with coverage..."
	$(GOTEST) -v -race -coverprofile=coverage.out -covermode=atomic ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "✓ Coverage report: coverage.html"

## install: Install binary to /usr/local/bin
install: build-release
	@echo "Installing $(BINARY_NAME) to /usr/local/bin..."
	@if [ "$$(id -u)" != "0" ]; then \
		echo "Error: Installation requires sudo/root privileges"; \
		echo "Run: sudo make install"; \
		exit 1; \
	fi
	install -m 755 $(BUILD_DIR)/$(BINARY_NAME) /usr/local/bin/$(BINARY_NAME)
	@echo "✓ Installed: /usr/local/bin/$(BINARY_NAME)"
	@echo ""
	@echo "Run '$(BINARY_NAME) --help' to get started"

## uninstall: Remove binary from /usr/local/bin
uninstall:
	@echo "Uninstalling $(BINARY_NAME)..."
	@if [ "$$(id -u)" != "0" ]; then \
		echo "Error: Uninstallation requires sudo/root privileges"; \
		echo "Run: sudo make uninstall"; \
		exit 1; \
	fi
	rm -f /usr/local/bin/$(BINARY_NAME)
	@echo "✓ Uninstalled"

## clean: Remove build artifacts
clean:
	@echo "Cleaning build artifacts..."
	rm -rf $(BUILD_DIR) $(DIST_DIR)
	rm -f coverage.out coverage.html
	@echo "✓ Cleaned"

## lint: Run golangci-lint
lint:
	@echo "Running linter..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed. Install with:"; \
		echo "  brew install golangci-lint"; \
		echo "  or visit: https://golangci-lint.run/usage/install/"; \
		exit 1; \
	fi

## fmt: Format Go code
fmt:
	@echo "Formatting code..."
	$(GOFMT) ./...
	@echo "✓ Formatted"

## deps: Download dependencies
deps:
	@echo "Downloading dependencies..."
	$(GOGET) -v -t -d ./...
	@echo "✓ Dependencies downloaded"

## tidy: Tidy go.mod
tidy:
	@echo "Tidying go.mod..."
	$(GOMOD) tidy
	@echo "✓ go.mod tidied"

## verify: Verify dependencies
verify:
	@echo "Verifying dependencies..."
	$(GOMOD) verify
	@echo "✓ Dependencies verified"

## release: Create release archives
release: build-all
	@echo "Creating release archives..."
	@mkdir -p $(DIST_DIR)
	cd $(DIST_DIR) && \
		tar -czf $(BINARY_NAME)-$(VERSION)-darwin-amd64.tar.gz $(BINARY_NAME)-darwin-amd64 && \
		tar -czf $(BINARY_NAME)-$(VERSION)-darwin-arm64.tar.gz $(BINARY_NAME)-darwin-arm64
	@echo "✓ Release archives created:"
	@ls -lh $(DIST_DIR)/*.tar.gz

## version: Show version information
version:
	@echo "Version:    $(VERSION)"
	@echo "Commit:     $(COMMIT)"
	@echo "Build Time: $(BUILD_TIME)"
