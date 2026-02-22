# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2024-01-15

### Added
- Initial release of SSM Proxy CLI
- Transparent system-level routing for macOS (darwin/amd64 and darwin/arm64)
- Virtual network interface (utun) creation and management
- Automatic routing table modification for specified CIDR blocks
- AWS SSM Session Manager integration
- Zero-configuration access to private AWS resources
- Support for multiple CIDR blocks simultaneously
- Session state management and persistence
- Commands:
  - `start` - Start transparent proxy tunnel
  - `stop` - Stop running proxy session(s)
  - `status` - Show status of active sessions
  - `version` - Display version information
- Configuration file support (`~/.ssm-proxy/config.yaml`)
- Named profile support for quick access to common configurations
- Auto-reconnect on connection failures
- AWS IAM authentication (no SSH keys required)
- Comprehensive CLI with help documentation
- Traffic statistics tracking
- Session health monitoring
- Graceful shutdown with automatic cleanup
- Debug and verbose logging modes

### Features
- Works with any application (databases, APIs, Redis, etc.)
- No application configuration needed
- Support for EC2 instance selection by ID or tag
- Configurable MTU and keep-alive settings
- Daemon mode for background operation
- Multiple concurrent sessions support
- Route verification and cleanup utilities

### Requirements
- macOS 11.0 (Big Sur) or later
- Go 1.21+ (for building from source)
- AWS credentials configured
- EC2 instance with SSM Agent and proper IAM role

### Known Limitations
- macOS only (Linux and Windows support planned)
- SSM WebSocket implementation is a placeholder (needs completion for production use)
- No DNS proxy/resolution through tunnel yet
- No automatic routing table updates (requires manual route addition)
- Session recovery after crashes needs improvement

### Technical
- Built with Go 1.21+
- Uses AWS SDK for Go v2
- Cobra for CLI framework
- Viper for configuration management
- Native macOS utun device support

## [0.0.1] - 2024-01-01

### Added
- Project initialization
- Basic project structure
- Specification document
- README and documentation

---

[Unreleased]: https://github.com/sbkg0002/ssm-proxy/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/sbkg0002/ssm-proxy/releases/tag/v0.1.0
[0.0.1]: https://github.com/sbkg0002/ssm-proxy/releases/tag/v0.0.1
