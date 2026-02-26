# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- DNS resolution through SSM tunnel
  - `--dns-resolver` flag to specify DNS server accessible through tunnel
  - `--dns-domains` flag to filter which domains to resolve through tunnel
  - Support for AWS VPC DNS resolver (169.254.169.253:53)
  - Support for custom internal DNS servers
  - Split-horizon DNS support (resolve only specific domains through tunnel)
  - DNS response caching (60 second TTL) for improved performance
  - UDP DNS packet capture with transparent TCP conversion for tunnel transport
  - TCP DNS through SOCKS5 tunnel (better compatibility than UDP)
  - Automatic domain pattern matching with suffix support
  - **Automatic macOS DNS resolver configuration** (`/etc/resolver/` management)
  - Automatic backup and restore of existing resolver files
  - Automatic DNS cache flushing on start and stop
  - Clean automatic cleanup on exit (Ctrl+C)
  - Comprehensive DNS resolver documentation (DNS_RESOLVER.md)
  - Automatic DNS setup guide (AUTOMATIC_DNS_SETUP.md)

### Changed

- Updated `NewTunToSOCKS` to accept optional DNS configuration
- Enhanced packet handling to support UDP DNS capture with TCP tunnel transport
- Implemented UDP-to-TCP DNS conversion for better SOCKS5 compatibility
- Updated README with DNS resolution examples and use cases
- Improved success banner to display DNS configuration
- Integrated automatic macOS resolver setup into start command
- DNS resolver now automatically configures macOS system DNS (no manual steps!)

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
- DNS uses TCP through tunnel (UDP to SOCKS5 conversion complex)
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
