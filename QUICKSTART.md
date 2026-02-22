# SSM Proxy - Quick Start Guide

Get up and running with SSM Proxy in 5 minutes.

## What is SSM Proxy?

A CLI tool that lets you access AWS private resources (databases, APIs, etc.) **without VPN or SSH keys**. Just route traffic through an EC2 instance via AWS SSM.

**The magic:** Applications work with **zero configuration**. The tool modifies your macOS routing table automatically.

## Prerequisites

- ✅ macOS 11.0+ (Big Sur or later)
- ✅ AWS account with EC2 and SSM access
- ✅ AWS credentials configured (`aws configure`)
- ✅ An EC2 instance running in your VPC

## Step 1: Install

### Option A: Download Binary (Fastest)

```bash
# Apple Silicon (M1/M2/M3)
curl -L https://github.com/sbkg0002/ssm-proxy/releases/latest/download/ssm-proxy-darwin-arm64.tar.gz -o ssm-proxy.tar.gz
tar -xzf ssm-proxy.tar.gz
sudo mv ssm-proxy-darwin-arm64 /usr/local/bin/ssm-proxy

# Intel Mac
curl -L https://github.com/sbkg0002/ssm-proxy/releases/latest/download/ssm-proxy-darwin-amd64.tar.gz -o ssm-proxy.tar.gz
tar -xzf ssm-proxy.tar.gz
sudo mv ssm-proxy-darwin-amd64 /usr/local/bin/ssm-proxy
```

### Option B: Build from Source

```bash
git clone https://github.com/sbkg0002/ssm-proxy.git
cd ssm-proxy
make build
sudo make install
```

### Verify Installation

```bash
ssm-proxy version
```

## Step 2: Setup EC2 Instance

Your EC2 instance needs three things:

### 1. SSM Agent Running

```bash
# Check SSM agent (on EC2 instance)
sudo systemctl status amazon-ssm-agent

# If not running, start it
sudo systemctl start amazon-ssm-agent
```

### 2. IAM Role

Attach the `AmazonSSMManagedInstanceCore` policy to your instance's IAM role.

**Console:** EC2 → Instance → Actions → Security → Modify IAM Role

### 3. IP Forwarding Enabled

```bash
# On EC2 instance
sudo sysctl -w net.ipv4.ip_forward=1
echo "net.ipv4.ip_forward=1" | sudo tee -a /etc/sysctl.conf
```

## Step 3: Start the Proxy

```bash
# Replace with your instance ID and VPC CIDR
sudo ssm-proxy start \
  --instance-id i-1234567890abcdef0 \
  --cidr 10.0.0.0/8
```

**Don't know your instance ID?**

```bash
# List all instances
ssm-proxy list-instances

# Find by name tag
ssm-proxy list-instances | grep bastion
```

**Success looks like:**

```
✓ Checking privileges... OK (running as root)
✓ Validating AWS credentials... OK (using profile: default)
✓ Finding EC2 instance i-1234567890abcdef0...
  ├─ Instance: bastion-host (t3.micro)
  ├─ State: running
  └─ SSM Status: connected ✓
✓ Starting SSM session...
✓ Creating utun device...
✓ Adding routes...

🚀 Transparent proxy is now active!

Press Ctrl+C to stop...
```

## Step 4: Use Your Applications

**That's it!** Your applications now work with no configuration:

```bash
# Database
psql -h 10.0.1.5 -p 5432 mydb

# API
curl http://10.0.2.100:8080/health

# Redis
redis-cli -h 10.0.3.25 -p 6379

# Any application!
mysql -h 10.0.4.50 -u admin -p
```

## Step 5: Stop When Done

```bash
# Press Ctrl+C in the terminal where ssm-proxy is running
# OR in another terminal:
sudo ssm-proxy stop
```

Routes are automatically cleaned up.

## Common Commands

```bash
# Check what's running
ssm-proxy status

# Stop all sessions
sudo ssm-proxy stop --all

# Find instances
ssm-proxy list-instances --ssm-only

# Test connectivity
ssm-proxy test 10.0.1.5:5432

# Get help
ssm-proxy --help
ssm-proxy start --help
```

## Troubleshooting

### "Not running as root"

```bash
# Always use sudo for start/stop commands
sudo ssm-proxy start ...
```

### "SSM Agent not connected"

```bash
# On EC2 instance, check SSM agent
sudo systemctl status amazon-ssm-agent

# Verify IAM role has AmazonSSMManagedInstanceCore
# Check VPC has SSM endpoints or NAT Gateway
```

### "Instance not found"

```bash
# List all instances to find the correct ID
ssm-proxy list-instances

# Check you're in the correct AWS region
ssm-proxy start --region us-west-2 --instance-id i-xxx --cidr 10.0.0.0/8
```

### "Failed to create utun device"

```bash
# Ensure running with sudo
# Check macOS security settings
# Try restarting and running again
```

### Debug Mode

```bash
# Enable verbose logging
sudo ssm-proxy start --debug \
  --instance-id i-xxx \
  --cidr 10.0.0.0/8
```

## Multiple CIDR Blocks

Route multiple networks simultaneously:

```bash
sudo ssm-proxy start \
  --instance-id i-xxx \
  --cidr 10.0.0.0/8 \
  --cidr 172.16.0.0/12 \
  --cidr 192.168.0.0/16
```

## Using AWS Profiles

```bash
# Use specific AWS profile
sudo ssm-proxy start \
  --profile production \
  --region us-west-2 \
  --instance-id i-xxx \
  --cidr 10.0.0.0/8
```

## Find Instance by Tag

```bash
# Use instance tag instead of ID
sudo ssm-proxy start \
  --instance-tag Name=bastion-host \
  --cidr 10.0.0.0/8
```

## Configuration File

Create `~/.ssm-proxy/config.yaml` for commonly used settings:

```yaml
aws:
  profile: default
  region: us-east-1

profiles:
  prod:
    instance_id: i-1234567890abcdef0
    cidr:
      - 10.0.0.0/8

  dev:
    instance_id: i-0987654321fedcba0
    cidr:
      - 172.16.0.0/12
```

Then use:

```bash
sudo ssm-proxy start --profile-name prod
```

## What's Happening Under the Hood?

1. **Creates utun device** - Virtual network interface (like `utun2`)
2. **Adds routes** - `route add -net 10.0.0.0/8 -interface utun2`
3. **Starts SSM session** - Secure tunnel to EC2 instance
4. **Forwards packets** - Routes traffic through tunnel
5. **EC2 forwards** - Instance sends traffic to actual destination

Your routing table now has:

```bash
$ netstat -rn | grep utun
10.0.0.0/8         169.254.169.1     UGSc      utun2
```

All traffic to `10.0.0.0/8` goes through `utun2` → SSM tunnel → EC2 → destination.

## Real-World Examples

### Access RDS Database

```bash
# Start proxy
sudo ssm-proxy start --instance-id i-xxx --cidr 10.0.0.0/16

# Connect to RDS (using private DNS)
psql -h mydb.abc123.us-east-1.rds.amazonaws.com -p 5432 -U admin myapp
```

### Multiple Services in Development

```bash
# Start proxy for dev VPC
sudo ssm-proxy start --instance-id i-xxx --cidr 10.10.0.0/16

# Terminal 1: Database
psql -h dev-db.internal -p 5432 mydb

# Terminal 2: API
curl http://dev-api.internal:8080

# Terminal 3: Redis
redis-cli -h dev-cache.internal

# All work simultaneously!
```

### Daemon Mode (Background)

```bash
# Start in background
sudo ssm-proxy start \
  --instance-id i-xxx \
  --cidr 10.0.0.0/8 \
  --daemon

# Work all day...

# Stop when done
sudo ssm-proxy stop
```

## Next Steps

- 📖 Read the full [README](README.md)
- 📋 Check the [SPECIFICATION](SPECIFICATION.md) for technical details
- 🐛 Report issues on [GitHub](https://github.com/sbkg0002/ssm-proxy/issues)
- 🤝 Contribute! See [CONTRIBUTING](CONTRIBUTING.md)

## Need Help?

- **GitHub Issues**: Bug reports and feature requests
- **GitHub Discussions**: Questions and help

## Tips

- ✅ Use `ssm-proxy status` to see what's running
- ✅ Routes are automatically cleaned up on stop
- ✅ Multiple sessions can run simultaneously (different CIDR blocks)
- ✅ All AWS actions are logged to CloudTrail
- ✅ No SSH keys or VPN client needed!

---

**Happy tunneling! 🚀**
