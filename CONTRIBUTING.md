# Contributing to SSM Proxy

Thank you for your interest in contributing to SSM Proxy! This document provides guidelines and instructions for contributing to the project.

## Table of Contents

- [Code of Conduct](#code-of-conduct)
- [Getting Started](#getting-started)
- [Development Setup](#development-setup)
- [Development Workflow](#development-workflow)
- [Code Style](#code-style)
- [Testing](#testing)
- [Submitting Changes](#submitting-changes)
- [Reporting Issues](#reporting-issues)
- [Areas for Contribution](#areas-for-contribution)

## Code of Conduct

This project follows a Code of Conduct to ensure a welcoming and inclusive environment for all contributors. By participating, you agree to:

- Be respectful and considerate
- Welcome newcomers and help them get started
- Focus on constructive feedback
- Accept criticism gracefully

## Getting Started

### Prerequisites

- macOS 11.0 (Big Sur) or later
- Go 1.21 or later
- Git
- AWS account with EC2 and SSM access (for testing)
- Basic understanding of Go, AWS SSM, and networking

### Fork and Clone

1. Fork the repository on GitHub
2. Clone your fork locally:
   ```bash
   git clone https://github.com/YOUR_USERNAME/ssm-proxy.git
   cd ssm-proxy
   ```
3. Add the upstream repository:
   ```bash
   git remote add upstream https://github.com/sbkg0002/ssm-proxy.git
   ```

## Development Setup

### Install Dependencies

```bash
# Download Go dependencies
go mod download

# Verify dependencies
go mod verify
```

### Build the Project

```bash
# Build for your current platform
make build

# Build for all supported platforms
make build-all

# Build with debug symbols
go build -o bin/ssm-proxy ./cmd/ssm-proxy
```

### Run the Binary

```bash
# Run the built binary
./bin/ssm-proxy version

# Run with sudo for commands that require it
sudo ./bin/ssm-proxy start --help
```

## Development Workflow

### 1. Create a Feature Branch

```bash
# Update your fork
git fetch upstream
git checkout main
git merge upstream/main

# Create a feature branch
git checkout -b feature/my-new-feature
```

### 2. Make Your Changes

- Write clean, readable code
- Follow the existing code structure
- Add comments for complex logic
- Update documentation as needed

### 3. Test Your Changes

```bash
# Run tests
make test

# Run tests with coverage
make test-coverage

# Run linter
make lint

# Format code
make fmt
```

### 4. Commit Your Changes

Write clear, descriptive commit messages:

```bash
git add .
git commit -m "Add feature: description of what was added"
```

Good commit message format:
```
Add feature: brief description

- Detailed point 1
- Detailed point 2
- Detailed point 3

Closes #123
```

### 5. Push and Create Pull Request

```bash
# Push to your fork
git push origin feature/my-new-feature
```

Then create a Pull Request on GitHub.

## Code Style

### Go Style Guidelines

- Follow [Effective Go](https://golang.org/doc/effective_go.html)
- Use `gofmt` to format code (automatically done with `make fmt`)
- Run `golangci-lint` to catch common issues (use `make lint`)

### Code Organization

```
ssm-proxy/
├── cmd/ssm-proxy/       # CLI commands and main entry point
├── internal/            # Internal packages (not importable)
│   ├── aws/            # AWS SDK wrappers
│   ├── forwarder/      # Packet forwarding logic
│   ├── routing/        # Route management
│   ├── session/        # Session state management
│   ├── ssm/            # SSM client
│   └── tunnel/         # TUN device management
└── pkg/                # Public packages (if any)
```

### Naming Conventions

- Use descriptive variable names
- Avoid single-letter variables except in short loops
- Constants should be in `UPPER_CASE` or `camelCase` depending on visibility
- Exported functions start with uppercase
- Unexported functions start with lowercase

### Error Handling

```go
// Good: wrap errors with context
if err != nil {
    return fmt.Errorf("failed to create TUN device: %w", err)
}

// Bad: return raw errors
if err != nil {
    return err
}
```

### Logging

Use structured logging with logrus:

```go
log.WithFields(logrus.Fields{
    "instance_id": instanceID,
    "cidr": cidr,
}).Info("Starting proxy session")
```

## Testing

### Unit Tests

```bash
# Run all tests
go test ./...

# Run tests with verbose output
go test -v ./...

# Run tests with race detection
go test -race ./...

# Run tests with coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### Writing Tests

- Place tests in `*_test.go` files
- Use table-driven tests where appropriate
- Mock external dependencies (AWS SDK, etc.)

Example:
```go
func TestValidateCIDR(t *testing.T) {
    tests := []struct {
        name    string
        cidr    string
        wantErr bool
    }{
        {"valid IPv4", "10.0.0.0/8", false},
        {"invalid format", "10.0.0.0", true},
        {"invalid IP", "999.0.0.0/8", true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := validateCIDR(tt.cidr)
            if (err != nil) != tt.wantErr {
                t.Errorf("validateCIDR() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}
```

### Integration Tests

Integration tests require AWS credentials and a running EC2 instance:

```bash
# Set up test environment
export AWS_PROFILE=test
export TEST_INSTANCE_ID=i-1234567890abcdef0

# Run integration tests
go test -tags=integration ./...
```

## Submitting Changes

### Pull Request Process

1. **Update documentation** - Ensure README, SPECIFICATION, and inline docs are updated
2. **Add tests** - Include unit tests for new functionality
3. **Run checks** - Ensure all tests pass and linter is happy
4. **Update CHANGELOG** - Add entry under `[Unreleased]` section
5. **Create PR** - Write a clear description of changes

### Pull Request Description

Include in your PR description:

- **What** - What does this PR do?
- **Why** - Why is this change needed?
- **How** - How does it work?
- **Testing** - How was it tested?
- **Screenshots** - If UI changes (CLI output)

Example:
```markdown
## What
Adds support for custom MTU configuration on TUN devices

## Why
Some networks require lower MTU values to avoid fragmentation

## How
- Added `--mtu` flag to start command
- Validates MTU is between 576 and 65535
- Applies MTU setting during TUN device configuration

## Testing
- Unit tests for MTU validation
- Tested manually with MTU values: 1400, 1500, 9000
- Verified packet forwarding works correctly

## Related Issues
Closes #42
```

### Review Process

1. Maintainers will review your PR
2. Address feedback and make requested changes
3. Once approved, a maintainer will merge your PR

## Reporting Issues

### Bug Reports

When reporting bugs, include:

- **Environment**: macOS version, Go version, ssm-proxy version
- **Steps to reproduce**: Exact commands and configuration
- **Expected behavior**: What should happen
- **Actual behavior**: What actually happens
- **Logs**: Relevant log output (use `--debug` flag)
- **AWS setup**: Instance type, SSM agent version, etc.

### Feature Requests

When requesting features:

- **Use case**: Describe the problem you're trying to solve
- **Proposed solution**: How you envision it working
- **Alternatives**: Other solutions you've considered
- **Examples**: Similar features in other tools

## Areas for Contribution

### High Priority

- **SSM WebSocket Implementation** - Complete the real SSM WebSocket connection (currently placeholder)
- **EC2 Agent** - Companion agent for EC2 instances to handle packet forwarding
- **DNS Proxy** - Route DNS queries through the tunnel
- **Connection Recovery** - Improve reconnection logic after failures
- **Linux Support** - Port to Linux (similar but different from macOS)

### Medium Priority

- **Windows Support** - Port to Windows (TAP driver)
- **IPv6 Support** - Handle IPv6 packets
- **Performance Optimization** - Reduce latency and increase throughput
- **GUI Application** - Native macOS app with menu bar widget
- **Metrics/Monitoring** - Prometheus metrics export
- **Multiple Sessions** - Better support for concurrent tunnels

### Low Priority

- **Session Recording** - Integration with SSM session recording
- **Auto-discovery** - Automatically find suitable bastion hosts
- **Healthchecks** - More sophisticated health monitoring
- **Configuration Validation** - Pre-flight checks before starting

### Documentation

- Improve README with more examples
- Add troubleshooting guide
- Create video tutorials
- Write blog posts about use cases
- Translate documentation

## Development Tips

### Debugging

```bash
# Run with debug logging
sudo ./bin/ssm-proxy start --debug --instance-id i-xxx --cidr 10.0.0.0/8

# Check logs
tail -f ~/.ssm-proxy/logs/ssm-proxy.log

# Inspect routing table
netstat -rn | grep utun

# Check TUN device
ifconfig | grep utun
```

### Common Issues

**"Not running as root"**
- Most commands require `sudo` for network configuration

**Build errors**
- Run `go mod tidy` to sync dependencies
- Ensure you're on Go 1.21 or later

**Import cycle errors**
- Keep internal packages properly separated
- Avoid circular dependencies

## Getting Help

- **GitHub Issues**: For bugs and feature requests
- **GitHub Discussions**: For questions and general discussion
- **Email**: Contact maintainers for security issues

## License

By contributing to SSM Proxy, you agree that your contributions will be licensed under the MIT License.

---

Thank you for contributing to SSM Proxy! 🚀
