# SSM Proxy

[![Build and Release](https://github.com/sbkg0002/ssm-proxy/actions/workflows/release.yml/badge.svg)](https://github.com/sbkg0002/ssm-proxy/actions/workflows/release.yml)
[![Go Version](https://img.shields.io/badge/go-1.21+-blue.svg)](https://golang.org/dl/)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

A macOS command-line tool that creates **transparent system-level routing** for specified CIDR blocks through an AWS EC2 instance via SSM Session Manager. Applications require **zero configuration** - traffic is automatically routed based on destination IP address.

**Architecture:** Uses TUN device + SSH tunnel over SSM + internal SOCKS5 proxy (invisible to apps). See [Architecture Guide](TRANSPARENT_PROXY_ARCHITECTURE.md) for details.

## 🚀 Features

- ✅ **Zero Application Configuration** - Works transparently with all applications
- ✅ **No VPN Client** - Uses AWS SSM infrastructure
- ✅ **No SSH Keys** - Leverages AWS IAM authentication
- ✅ **Secure & Auditable** - All traffic logged via AWS CloudTrail
- ✅ **Simple UX** - One command to start, applications just work
- ✅ **Multiple CIDR Blocks** - Route multiple networks simultaneously
- ✅ **Auto-Reconnect** - Automatic reconnection on connection failures
- ✅ **Session Management** - Track and manage multiple concurrent sessions

## 🎯 Use Cases

- Access RDS databases in private subnets
- Connect to ElastiCache Redis clusters
- Reach internal APIs and services
- Access multiple AWS resources without VPN
- Development/debugging in private VPCs
- Temporary access to AWS resources

## 📋 Requirements

### Local Machine (macOS)

- macOS 11.0 (Big Sur) or later
- Root/sudo privileges (for network configuration)
- AWS credentials configured (`~/.aws/credentials` or environment variables)

### AWS Infrastructure

- EC2 instance running in your VPC (bastion/jump host)
- EC2 instance with SSM Agent installed and running
- EC2 instance with IAM role: `AmazonSSMManagedInstanceCore`
- EC2 instance with IP forwarding enabled: `sudo sysctl -w net.ipv4.ip_forward=1`
- VPC with SSM endpoints OR NAT Gateway/Internet Gateway

### AWS Permissions

Your IAM user/role needs:

- `ssm:StartSession`
- `ssm:TerminateSession`
- `ec2:DescribeInstances`

## 📚 Documentation

- **[Quick Start Guide](TRANSPARENT_PROXY_QUICKSTART.md)** - Get started in 5 minutes
- **[Architecture Guide](TRANSPARENT_PROXY_ARCHITECTURE.md)** - How transparent proxy works
- **[Specification](SPECIFICATION.md)** - Complete feature specification

## 📦 Installation

### macOS (Apple Silicon / M1/M2/M3)

```bash
# Install mise https://mise.jdx.dev/
mise use --global github:sbkg0002/ssm-proxy
```

### Build from Source

```bash
git clone https://github.com/sbkg0002/ssm-proxy.git
cd ssm-proxy
make build
sudo make install
```

## 🎬 Quick Start

### 1. Start the Proxy

```bash
# Route 10.0.0.0/8 through EC2 instance
sudo -E ssm-proxy start \
  --instance-id i-1234567890abcdef0 \
  --cidr 10.0.0.0/8
```

### 2. Use Your Applications Normally

```bash
# Database - NO PROXY CONFIGURATION NEEDED!
e.g. psql -h 10.0.1.5 -p 5432 mydb
```

### 3. Stop the Proxy

```bash
sudo -E ssm-proxy stop
```

## 📖 Usage

### Start Proxy

```bash
# Basic usage
sudo -E ssm-proxy start --instance-id i-xxx --cidr 10.0.0.0/8

# Multiple CIDR blocks
sudo -E ssm-proxy start \
  --instance-id i-xxx \
  --cidr 10.0.0.0/8 \
  --cidr 172.16.0.0/12

# Find instance by tag
sudo -E ssm-proxy start \
  --instance-tag Name=bastion-host \
  --cidr 10.0.0.0/16

# Custom AWS profile and region
sudo -E ssm-proxy start \
  --profile production \
  --region us-west-2 \
  --instance-id i-xxx \
  --cidr 10.0.0.0/8

# Run as daemon (background)
sudo -E ssm-proxy start \
  --instance-id i-xxx \
  --cidr 10.0.0.0/8 \
  --daemon
```

### Check Status

```bash
# Show active sessions
ssm-proxy status

# JSON output
ssm-proxy status --json

# Watch mode (live updates)
ssm-proxy status --watch

# Detailed output with routes and stats
ssm-proxy status --show-routes --show-stats
```

### List Available EC2 Instances

```bash
# List all running instances
ssm-proxy list-instances

# Filter by tag
ssm-proxy list-instances --tag Environment=production

# Only show SSM-ready instances
ssm-proxy list-instances --ssm-only
```

### Stop Proxy

```bash
# Stop default session
sudo -E ssm-proxy stop

# Stop specific session
sudo -E ssm-proxy stop --session-name my-session

# Stop all sessions
sudo -E ssm-proxy stop --all
```

### Test Connectivity

```bash
# Test TCP connectivity
ssm-proxy test 10.0.1.5:5432

# Test with custom timeout
ssm-proxy test --timeout 30s 10.0.2.100:8080
```

## ⚙️ Configuration

### Configuration File

Create `~/.ssm-proxy/config.yaml`:

```yaml
# AWS Configuration
aws:
  profile: default
  region: us-east-1

# Default Settings
defaults:
  local_ip: 169.254.169.1/30
  mtu: 1500
  keep_alive: 30s
  timeout: 30s
  auto_reconnect: true
  reconnect_delay: 5s
  max_retries: 0 # 0 = unlimited

# Logging
logging:
  level: info # debug, info, warn, error
  file: ~/.ssm-proxy/logs/ssm-proxy.log

# Named Profiles for Quick Access
profiles:
  prod:
    instance_id: i-1234567890abcdef0
    cidr:
      - 10.0.0.0/8
    session_name: prod-vpc

  dev:
    instance_tag: Environment=dev,Role=bastion
    cidr:
      - 172.16.0.0/12
    session_name: dev-vpc
```

### Using Named Profiles

```bash
# Start using named profile
sudo -E ssm-proxy start --profile-name prod

# Override profile settings
sudo -E ssm-proxy start --profile-name prod --cidr 10.0.0.0/16
```

## 🔧 EC2 Instance Setup

Your EC2 instance needs the following configuration:

### 1. Install SSM Agent (if not pre-installed)

```bash
# Amazon Linux 2 / Amazon Linux 2023 (pre-installed)
sudo systemctl status amazon-ssm-agent

# Ubuntu
sudo snap install amazon-ssm-agent --classic
sudo snap start amazon-ssm-agent
```

### 2. Enable IP Forwarding

```bash
sudo sysctl -w net.ipv4.ip_forward=1
echo "net.ipv4.ip_forward=1" | sudo tee -a /etc/sysctl.conf
```

### 3. Attach IAM Role

Attach the AWS managed policy `AmazonSSMManagedInstanceCore` to the instance's IAM role, or create a custom policy:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "ssm:UpdateInstanceInformation",
        "ssmmessages:CreateControlChannel",
        "ssmmessages:CreateDataChannel",
        "ssmmessages:OpenControlChannel",
        "ssmmessages:OpenDataChannel"
      ],
      "Resource": "*"
    }
  ]
}
```

### 4. Security Group

Ensure outbound HTTPS (443) is allowed to SSM endpoints.

## 🎨 Architecture

```
Application (psql, curl, etc.)
        ↓ (no configuration needed)
macOS Routing Table (10.0.0.0/8 → utun2)
        ↓
utun2 (virtual network interface)
        ↓
TUN-to-SOCKS Translator (user-space TCP/IP stack)
        ↓ (internal SOCKS5 - apps don't see this!)
SSH Tunnel (-D dynamic forwarding)
        ↓ (encrypted over SSM WebSocket)
AWS SSM Session Manager
        ↓
EC2 Instance (bastion with IP forwarding)
        ↓
Target Resources (RDS, Redis, etc.)
```

**Key Innovation:** Applications connect normally to private IPs. The TUN device captures packets, translates them to SOCKS5 connections internally (invisible to apps), and forwards through an SSH tunnel over SSM.

📖 **Read the [detailed architecture guide](TRANSPARENT_PROXY_ARCHITECTURE.md)** to understand how this achieves true transparency.

## 🐛 Troubleshooting

### "Not running as root"

```bash
# Solution: Run with sudo
sudo -E ssm-proxy start --instance-id i-xxx --cidr 10.0.0.0/8
```

### "SSM Agent not connected"

```bash
# Check SSM Agent status on EC2 instance
sudo systemctl status amazon-ssm-agent

# Verify IAM role has AmazonSSMManagedInstanceCore policy
# Check VPC has connectivity to SSM endpoints
```

### "Failed to create utun device"

```bash
# Ensure running with sudo
# Check macOS security settings (System Preferences → Security)
# Restart and try again
```

### "Route already exists"

```bash
# Another session may be using same CIDR
ssm-proxy status

# Clean up stale routes
sudo -E ssm-proxy routes cleanup

# Or stop conflicting session
sudo -E ssm-proxy stop --all
```

### Enable Debug Logging

```bash
sudo -E ssm-proxy start --debug --instance-id i-xxx --cidr 10.0.0.0/8
```

### Check Routes

```bash
# View routing table
netstat -rn | grep utun

# Verify specific route
route get 10.0.1.5
```

## 📝 Examples

### Database Access

```bash
sudo -E ssm-proxy start --instance-id i-xxx --cidr 10.0.0.0/16

# PostgreSQL
psql -h mydb.abc.us-east-1.rds.amazonaws.com -p 5432 -U admin myapp

# MySQL
mysql -h mydb.abc.us-east-1.rds.amazonaws.com -P 3306 -u admin -p
```

### Redis/ElastiCache

```bash
sudo -E ssm-proxy start --instance-id i-xxx --cidr 10.0.0.0/8

redis-cli -h master.abc.cache.amazonaws.com -p 6379
```

### Multiple Services

```bash
# Route entire VPC CIDR
sudo -E ssm-proxy start --instance-id i-xxx --cidr 10.0.0.0/8

# Now all services work transparently
psql -h 10.0.1.5 -p 5432 db1
redis-cli -h 10.0.2.25
curl http://10.0.3.100:8080/api
ssh ec2-user@10.0.4.50
```

### Development Workflow

```bash
# Morning: Start proxy as daemon
sudo -E ssm-proxy start \
  --instance-tag Environment=dev \
  --cidr 10.10.0.0/16 \
  --daemon

# Work all day with transparent access
psql -h dev-db.internal -p 5432 mydb
curl http://dev-api.internal:8080

# Evening: Stop proxy
sudo -E ssm-proxy stop
```

## 🤝 Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 🙏 Acknowledgments

- AWS Systems Manager team for the excellent SSM Session Manager service
- The Go community for amazing libraries and tools

## 🔗 Links

- [Quick Start Guide](TRANSPARENT_PROXY_QUICKSTART.md)
- [Architecture Guide](TRANSPARENT_PROXY_ARCHITECTURE.md)
- [Full Documentation](SPECIFICATION.md)
- [Issue Tracker](https://github.com/sbkg0002/ssm-proxy/issues)
- [Releases](https://github.com/sbkg0002/ssm-proxy/releases)

## ⭐ Show Your Support

Give a ⭐️ if this project helped you!

---

**How It Works:** This tool creates a TUN device for packet capture, establishes an SSH tunnel with dynamic SOCKS5 forwarding over SSM, and translates TUN packets to SOCKS5 connections in user-space. The result is truly transparent networking where applications require zero configuration. See the [Architecture Guide](TRANSPARENT_PROXY_ARCHITECTURE.md) for a deep dive.
