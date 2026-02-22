# SSM Proxy CLI - Technical Specification (macOS)

**Version:** 1.0  
**Platform:** macOS 11.0+ (Big Sur and later)  
**Language:** Go 1.21+

---

## 1. Executive Summary

### 1.1 Purpose
A macOS command-line tool that creates **transparent system-level routing** for specified CIDR blocks through an AWS EC2 instance via SSM Session Manager. Applications require **zero configuration** - traffic is automatically routed based on destination IP address.

### 1.2 Key Value Proposition
- ✅ **Zero application configuration** - works transparently with all apps
- ✅ **No VPN client** needed - uses AWS SSM infrastructure
- ✅ **No SSH keys** - leverages AWS IAM authentication
- ✅ **Secure & Auditable** - all traffic logged via CloudTrail
- ✅ **Simple UX** - one command to start, applications just work

### 1.3 How It Works
```
psql -h 10.0.1.5 -p 5432 mydb
         ↓
macOS Routing Table (10.0.0.0/8 → utun2)
         ↓
utun2 (virtual network interface)
         ↓
ssm-proxy CLI (reads packets, forwards via SSM)
         ↓
AWS SSM Session Manager (encrypted tunnel)
         ↓
EC2 Instance in VPC (forwards packets)
         ↓
Target Resource (RDS, Redis, etc.)
```

**User just runs:**
```bash
sudo ssm-proxy start --instance-id i-xxxxx --cidr 10.0.0.0/8
psql -h 10.0.1.5 -p 5432 mydb  # Works automatically!
```

---

## 2. Architecture

### 2.1 System Architecture Diagram

```
┌────────────────────────────────────────────────────────────┐
│ macOS System                                                │
│                                                             │
│  ┌──────────────────────────────────────────────────────┐ │
│  │ User Space                                            │ │
│  │                                                       │ │
│  │  ┌─────────────────┐                                 │ │
│  │  │  Application    │ (curl, psql, browser, etc.)     │ │
│  │  │  No config      │                                 │ │
│  │  └────────┬────────┘                                 │ │
│  │           │ connect(10.0.1.5:5432)                   │ │
│  │           │                                           │ │
│  └───────────┼───────────────────────────────────────────┘ │
│              │                                             │
│  ┌───────────▼───────────────────────────────────────────┐ │
│  │ Kernel Space                                          │ │
│  │                                                       │ │
│  │  ┌──────────────────────────────┐                    │ │
│  │  │  Routing Table               │                    │ │
│  │  │  10.0.0.0/8 → utun2         │                    │ │
│  │  │  172.16.0.0/12 → utun2      │                    │ │
│  │  └──────────┬───────────────────┘                    │ │
│  │             │                                         │ │
│  │  ┌──────────▼───────────────────┐                    │ │
│  │  │  utun Device (utun2)         │                    │ │
│  │  │  169.254.169.1/30            │                    │ │
│  │  │  (TUN virtual interface)     │                    │ │
│  │  └──────────┬───────────────────┘                    │ │
│  │             │ IP packets                              │ │
│  └─────────────┼─────────────────────────────────────────┘ │
│                │                                           │
│  ┌─────────────▼─────────────────────────────────────────┐ │
│  │ User Space                                            │ │
│  │                                                       │ │
│  │  ┌──────────────────────────────────────────────┐    │ │
│  │  │  ssm-proxy CLI Process                       │    │ │
│  │  │                                               │    │ │
│  │  │  ┌──────────────────────────────────────┐    │    │ │
│  │  │  │  TUN Reader/Writer                   │    │    │ │
│  │  │  │  - read() from /dev/utun2            │    │    │ │
│  │  │  │  - write() to /dev/utun2             │    │    │ │
│  │  │  └──────────┬───────────────────────────┘    │    │ │
│  │  │             │                                 │    │ │
│  │  │  ┌──────────▼───────────────────────────┐    │    │ │
│  │  │  │  Packet Processor                    │    │    │ │
│  │  │  │  - Parse IP packets                  │    │    │ │
│  │  │  │  - Encapsulate/decapsulate           │    │    │ │
│  │  │  └──────────┬───────────────────────────┘    │    │ │
│  │  │             │                                 │    │ │
│  │  │  ┌──────────▼───────────────────────────┐    │    │ │
│  │  │  │  SSM Session Manager Client          │    │    │ │
│  │  │  │  - AWS SDK for Go v2                 │    │    │ │
│  │  │  │  - WebSocket connection              │    │    │ │
│  │  │  └──────────┬───────────────────────────┘    │    │ │
│  │  │             │                                 │    │ │
│  │  └─────────────┼─────────────────────────────────┘    │ │
└────────────────────┼──────────────────────────────────────┘
                     │ HTTPS/WebSocket (encrypted)
                     │
┌────────────────────▼──────────────────────────────────────┐
│ AWS Cloud                                                  │
│                                                            │
│  ┌──────────────────┐       ┌─────────────────────────┐  │
│  │  SSM Service     │──────►│  EC2 Instance           │  │
│  │  (Regional)      │       │  (10.0.1.50)            │  │
│  └──────────────────┘       │                         │  │
│                              │  ┌───────────────────┐  │  │
│                              │  │  SSM Agent        │  │  │
│                              │  │  ssm-proxy-agent  │  │  │
│                              │  └─────────┬─────────┘  │  │
│                              │            │            │  │
│                              │  ┌─────────▼─────────┐  │  │
│                              │  │  IP Forwarding    │  │  │
│                              │  │  Enabled          │  │  │
│                              │  └─────────┬─────────┘  │  │
│                              └────────────┼────────────┘  │
│                                           │               │
│                              ┌────────────▼────────────┐  │
│                              │  Target Resources       │  │
│                              │  - RDS (10.0.1.5:5432)  │  │
│                              │  - Redis (10.0.2.10)    │  │
│                              │  - APIs (10.0.3.x)      │  │
│                              │  - Any service in CIDR  │  │
│                              └─────────────────────────┘  │
└────────────────────────────────────────────────────────────┘
```

### 2.2 Component Breakdown

#### 2.2.1 utun Device (TUN Interface)
- **Type**: Layer 3 virtual network interface
- **Device**: `/dev/utunX` (where X is auto-assigned by macOS, typically 2+)
- **Purpose**: Capture IP packets destined for target CIDR blocks
- **IP Assignment**: Link-local address (e.g., 169.254.169.1/30)
- **MTU**: Configurable, default 1500 bytes
- **Access**: Requires root privileges to create

#### 2.2.2 macOS Routing Table
- **Purpose**: Direct traffic for CIDR blocks to utun device
- **Command**: `route add -net <cidr> -interface utunX`
- **Example**: `route add -net 10.0.0.0/8 -interface utun2`
- **Verification**: `netstat -rn | grep utun`
- **Cleanup**: `route delete -net <cidr>` on exit

#### 2.2.3 SSM Tunnel
- **Protocol**: AWS SSM Session Manager via HTTPS/WebSocket
- **Encryption**: TLS 1.2+ (managed by AWS)
- **Authentication**: AWS IAM credentials
- **Data Format**: Custom encapsulation protocol (see section 5.4)
- **Bandwidth**: Limited by SSM (typically 10-100 Mbps)
- **Latency**: +5-20ms overhead

#### 2.2.4 EC2 Packet Forwarder
- **Component**: Companion agent on EC2 instance
- **Function**: Receive packets from SSM, forward to destination, return responses
- **Requirements**: 
  - SSM Agent running
  - IP forwarding enabled (`sysctl net.ipv4.ip_forward=1`)
  - Network access to target CIDR blocks

### 2.3 Packet Flow (Detailed)

#### Outbound (Client → Target):
1. Application calls `connect(10.0.1.5, 5432)`
2. macOS kernel checks routing table: `10.0.1.5` matches `10.0.0.0/8 → utun2`
3. Kernel writes IP packet to utun2 device
4. ssm-proxy reads raw IP packet from `/dev/utun2`
5. ssm-proxy encapsulates packet (adds length header)
6. ssm-proxy sends via SSM WebSocket to EC2
7. EC2 ssm-proxy-agent receives, decapsulates
8. EC2 forwards raw IP packet to destination (10.0.1.5:5432)

#### Inbound (Target → Client):
1. Response comes back to EC2 instance
2. EC2 ssm-proxy-agent captures response packet
3. EC2 encapsulates packet
4. Sends via SSM WebSocket to client
5. Client ssm-proxy receives, decapsulates
6. Writes raw IP packet to `/dev/utun2`
7. macOS kernel routes to application
8. Application receives response

### 2.4 Assumptions

**Environment:**
- ✅ macOS 11.0 or later (Big Sur, Monterey, Ventura, Sonoma)
- ✅ User has admin privileges (can run `sudo`)
- ✅ Internet connectivity
- ✅ AWS credentials configured (`~/.aws/credentials` or environment variables)

**AWS Infrastructure:**
- ✅ EC2 instance exists and is running
- ✅ EC2 instance has SSM Agent installed and running
- ✅ EC2 instance has IAM role with `AmazonSSMManagedInstanceCore` policy
- ✅ EC2 instance has IP forwarding enabled
- ✅ EC2 instance can reach target CIDR blocks (routes/security groups configured)
- ✅ VPC has SSM endpoints OR NAT Gateway/Internet Gateway for SSM connectivity

**User:**
- ✅ Has IAM permissions for `ssm:StartSession`, `ssm:TerminateSession`
- ✅ Has IAM permissions for `ec2:DescribeInstances`

---

## 3. CLI Interface

### 3.1 Installation

```bash
# Homebrew (future)
brew install ssm-proxy

# Direct download
curl -L https://github.com/user/ssm-proxy/releases/latest/download/ssm-proxy-darwin-amd64 -o /usr/local/bin/ssm-proxy
chmod +x /usr/local/bin/ssm-proxy

# Build from source
git clone https://github.com/user/ssm-proxy.git
cd ssm-proxy
make build
sudo make install
```

### 3.2 Command Structure

```bash
sudo ssm-proxy [global-flags] <command> [flags]
```

**⚠️ Important:** Most commands require `sudo` due to TUN device creation and routing table modification.

### 3.3 Global Flags

```
--profile string        AWS profile name (default: $AWS_PROFILE or "default")
--region string         AWS region (default: $AWS_REGION or from profile)
--config string         Config file path (default: ~/.ssm-proxy/config.yaml)
--verbose, -v           Verbose output (includes INFO logs)
--debug                 Debug output (includes packet-level details)
--quiet, -q             Quiet mode (errors only)
--version               Show version and exit
--help, -h              Show help
```

### 3.4 Commands

#### 3.4.1 `start` - Start Transparent Proxy

Start the transparent proxy tunnel. This creates a utun device, adds routes, and establishes an SSM session.

```bash
sudo ssm-proxy start [flags]
```

**Required Flags (choose one):**

```
--instance-id string         EC2 instance ID
                            Example: --instance-id i-1234567890abcdef0

--instance-tag string        Find instance by tag (format: Key=Value)
                            Example: --instance-tag Name=bastion-host
                            Example: --instance-tag Environment=prod
```

**Required Flags:**

```
--cidr strings              CIDR blocks to route through tunnel (repeatable)
                            Example: --cidr 10.0.0.0/8
                            Example: --cidr 10.0.0.0/8 --cidr 172.16.0.0/12
```

**Optional Flags:**

```
--local-ip string           IP address for utun device (default: 169.254.169.1/30)
                            Must be in link-local range and not in use
                            Example: --local-ip 169.254.170.1/30

--mtu int                   MTU for utun device (default: 1500)
                            Adjust if experiencing fragmentation issues
                            Example: --mtu 1400

--session-name string       Custom name for this session (default: auto-generated)
                            Used for managing multiple concurrent sessions
                            Example: --session-name prod-vpc

--keep-alive duration       Keep-alive interval (default: 30s)
                            Send periodic keep-alive packets
                            Example: --keep-alive 60s

--timeout duration          Initial connection timeout (default: 30s)
                            Example: --timeout 60s

--auto-reconnect           Enable automatic reconnection on failure (default: true)
                            Disable with --auto-reconnect=false

--reconnect-delay duration  Delay between reconnection attempts (default: 5s)
                            Example: --reconnect-delay 10s

--max-retries int          Maximum reconnection attempts (default: 0 = unlimited)
                            Example: --max-retries 5

--daemon, -d               Run in background as daemon
                            Process detaches and runs in background

--pid-file string          PID file location (default: /var/run/ssm-proxy.pid)
                            Only used in daemon mode
                            Example: --pid-file /tmp/ssm-proxy.pid

--log-file string          Log file location (default: stderr, or /var/log/ssm-proxy.log in daemon mode)
                            Example: --log-file /tmp/ssm-proxy.log

--log-packets              Log individual packets (very verbose, debug only)
                            WARNING: High disk usage
```

**Examples:**

```bash
# Basic usage - route 10.0.0.0/8 through bastion
sudo ssm-proxy start \
  --instance-id i-1234567890abcdef0 \
  --cidr 10.0.0.0/8

# Multiple CIDR blocks
sudo ssm-proxy start \
  --instance-id i-1234567890abcdef0 \
  --cidr 10.0.0.0/8 \
  --cidr 172.16.0.0/12

# Find instance by tag
sudo ssm-proxy start \
  --instance-tag Name=bastion-host \
  --cidr 10.0.0.0/16

# Custom MTU (for environments with lower MTU)
sudo ssm-proxy start \
  --instance-id i-1234567890abcdef0 \
  --cidr 10.0.0.0/8 \
  --mtu 1400

# Named session (for multiple concurrent tunnels)
sudo ssm-proxy start \
  --instance-id i-1234567890abcdef0 \
  --cidr 10.0.0.0/8 \
  --session-name prod-vpc

# Run as daemon (background)
sudo ssm-proxy start \
  --instance-id i-1234567890abcdef0 \
  --cidr 10.0.0.0/8 \
  --daemon

# Different AWS profile and region
sudo ssm-proxy start \
  --profile production \
  --region us-west-2 \
  --instance-id i-1234567890abcdef0 \
  --cidr 10.0.0.0/8
```

**Success Output:**

```
✓ Checking privileges... OK (running as root)
✓ Validating AWS credentials... OK (using profile: default)
✓ Finding EC2 instance i-1234567890abcdef0...
  ├─ Instance: bastion-host (t3.micro)
  ├─ State: running
  ├─ AZ: us-east-1a
  ├─ Private IP: 10.0.1.50
  └─ SSM Status: connected ✓
✓ Starting SSM session...
  └─ Session ID: sess-0123456789abcdef
✓ Creating utun device...
  ├─ Device: utun2
  ├─ IP: 169.254.169.1/30
  └─ MTU: 1500
✓ Adding routes...
  └─ 10.0.0.0/8 → utun2
✓ Starting packet forwarder...

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
🚀 Transparent proxy is now active!

All traffic to the following CIDR blocks will be
automatically routed through the SSM tunnel:
  • 10.0.0.0/8

Your applications require NO configuration.
Just connect normally:

  # Database
  psql -h 10.0.1.5 -p 5432 mydb

  # API
  curl http://10.0.2.100:8080/health

  # Redis
  redis-cli -h 10.0.3.25 -p 6379

Press Ctrl+C to stop and clean up...
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```

**Error Examples:**

```
Error: Not running as root
→ This command requires root privileges to create network devices
→ Please run: sudo ssm-proxy start ...

Error: Instance i-1234567890abcdef0 not found
→ Verify the instance ID is correct
→ Verify you have permission to describe EC2 instances
→ Check you're in the correct AWS region (current: us-east-1)

Error: SSM Agent not connected on instance i-1234567890abcdef0
→ Ensure SSM Agent is installed and running
→ Verify instance has IAM role with AmazonSSMManagedInstanceCore
→ Check VPC has connectivity to SSM endpoints

Error: Port already in use: 169.254.169.1
→ Another ssm-proxy session may be running
→ Use `sudo ssm-proxy status` to check active sessions
→ Or use a different IP: --local-ip 169.254.170.1/30
```

#### 3.4.2 `stop` - Stop Proxy

Stop a running proxy session and clean up routes and TUN device.

```bash
sudo ssm-proxy stop [flags]
```

**Flags:**

```
--session-name string    Stop specific session by name
--all                    Stop all running sessions
--force                  Force stop without graceful shutdown
```

**Examples:**

```bash
# Stop default session
sudo ssm-proxy stop

# Stop specific named session
sudo ssm-proxy stop --session-name prod-vpc

# Stop all sessions
sudo ssm-proxy stop --all

# Force stop (immediate, no cleanup)
sudo ssm-proxy stop --force
```

**Output:**

```
✓ Found session: default (sess-0123456789abcdef)
✓ Terminating SSM session...
✓ Removing routes...
  └─ 10.0.0.0/8 → utun2
✓ Closing utun device...
✓ Session stopped successfully

All routes have been cleaned up.
```

#### 3.4.3 `status` - Show Status

Display status of active proxy sessions.

```bash
ssm-proxy status [flags]
```

**Flags:**

```
--json                   Output in JSON format
--watch, -w             Watch mode (refresh every 2s, press Ctrl+C to exit)
--show-routes           Include routing table details
--show-stats            Include traffic statistics
```

**Examples:**

```bash
# Show status
ssm-proxy status

# JSON output
ssm-proxy status --json

# Watch mode (live updates)
ssm-proxy status --watch

# Detailed output
ssm-proxy status --show-routes --show-stats
```

**Output:**

```
ACTIVE SSM PROXY SESSIONS

SESSION       INSTANCE ID          STATUS    UTUN    CIDR BLOCKS        UPTIME     TX/RX
────────────────────────────────────────────────────────────────────────────────────────
default       i-1234567890abcdef0  active    utun2   10.0.0.0/8        2h 34m     1.2GB / 850MB
prod-vpc      i-0987654321fedcba0  active    utun3   172.16.0.0/12     45m        120MB / 95MB

ROUTING TABLE (filtered for ssm-proxy):
10.0.0.0/8         utun2      UGSc
172.16.0.0/12      utun3      UGSc
```

**JSON Output:**

```json
{
  "sessions": [
    {
      "name": "default",
      "instance_id": "i-1234567890abcdef0",
      "status": "active",
      "utun_device": "utun2",
      "utun_ip": "169.254.169.1/30",
      "cidr_blocks": ["10.0.0.0/8"],
      "started_at": "2024-01-15T10:30:00Z",
      "uptime_seconds": 9240,
      "bytes_tx": 1288490188,
      "bytes_rx": 891289600,
      "packets_tx": 856123,
      "packets_rx": 594087
    }
  ]
}
```

#### 3.4.4 `list-instances` - List EC2 Instances

List EC2 instances available for use as proxy endpoints.

```bash
ssm-proxy list-instances [flags]
```

**Flags:**

```
--tag string            Filter by tag (format: Key=Value)
--ssm-only             Only show instances with SSM Agent connected
--json                 Output in JSON format
```

**Examples:**

```bash
# List all instances
ssm-proxy list-instances

# Filter by tag
ssm-proxy list-instances --tag Environment=production

# Only SSM-ready instances
ssm-proxy list-instances --ssm-only

# JSON output
ssm-proxy list-instances --json
```

**Output:**

```
EC2 INSTANCES (us-east-1)

INSTANCE ID          NAME            STATE    SSM STATUS    PRIVATE IP    IP FWD    AZ
─────────────────────────────────────────────────────────────────────────────────────────
i-1234567890abcdef0  bastion-host    running  connected ✓   10.0.1.50     yes ✓     us-east-1a
i-0987654321fedcba0  bastion-dev     running  connected ✓   10.0.2.50     yes ✓     us-east-1b
i-abcdef1234567890  web-server      running  disconnected  10.0.3.100    no ✗      us-east-1a

✓ = Ready for use as proxy endpoint
✗ = Not suitable (SSM not connected or IP forwarding disabled)
```

#### 3.4.5 `test` - Test Connectivity

Test connectivity to a target through the active tunnel.

```bash
ssm-proxy test [flags] <target>
```

**Flags:**

```
--session-name string    Test through specific session (default: default)
--timeout duration       Test timeout (default: 10s)
--count int             Number of attempts (default: 4)
```

**Arguments:**

```
<target>                 Target address to test
                        Format: <host>:<port> or just <host> (for ICMP ping)
                        Examples: 10.0.1.5:5432, 10.0.2.100:8080, 10.0.3.50
```

**Examples:**

```bash
# Test TCP connectivity
ssm-proxy test 10.0.1.5:5432

# Ping test
ssm-proxy test 10.0.1.5

# Test HTTP endpoint
ssm-proxy test 10.0.2.100:8080

# Custom timeout
ssm-proxy test --timeout 30s 10.0.3.25:6379
```

**Output:**

```
Testing connectivity to 10.0.1.5:5432 through session 'default'...

Attempt 1/4... ✓ Connected in 45ms
Attempt 2/4... ✓ Connected in 42ms
Attempt 3/4... ✓ Connected in 44ms
Attempt 4/4... ✓ Connected in 43ms

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
✓ Connectivity test PASSED

Statistics:
  Success Rate: 100% (4/4)
  Average Latency: 43.5ms
  Min: 42ms | Max: 45ms
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```

#### 3.4.6 `routes` - Manage Routes

Show and manage routing table entries created by ssm-proxy.

```bash
sudo ssm-proxy routes <subcommand> [flags]
```

**Subcommands:**

```
list                    List all ssm-proxy managed routes
verify                  Verify routes are correctly configured
cleanup                 Remove stale/orphaned routes (from crashed sessions)
```

**Examples:**

```bash
# List managed routes
sudo ssm-proxy routes list

# Verify routes
sudo ssm-proxy routes verify

# Clean up stale routes
sudo ssm-proxy routes cleanup
```

**Output (list):**

```
SSM-PROXY MANAGED ROUTES

DESTINATION        INTERFACE    FLAGS    SESSION
───────────────────────────────────────────────────
10.0.0.0/8        utun2        UGSc     default
172.16.0.0/12     utun3        UGSc     prod-vpc

Total: 2 routes
```

**Output (cleanup):**

```
Scanning for stale routes...
✓ Found 1 orphaned route: 192.168.0.0/16 → utun5 (no active session)
✓ Removed orphaned route
✓ Cleanup complete
```

#### 3.4.7 `logs` - View Logs

View or tail logs from ssm-proxy sessions.

```bash
ssm-proxy logs [flags]
```

**Flags:**

```
--session-name string    Show logs for specific session (default: all)
--follow, -f            Follow log output (stream)
--tail int              Show last N lines (default: 100)
--since duration        Show logs since duration ago (e.g., 5m, 1h)
--level string          Filter by level: debug, info, warn, error
```

**Examples:**

```bash
# View recent logs
ssm-proxy logs

# Follow logs (real-time)
ssm-proxy logs --follow

# Last 50 lines
ssm-proxy logs --tail 50

# Errors only from last hour
ssm-proxy logs --since 1h --level error

# Specific session
ssm-proxy logs --session-name prod-vpc
```

#### 3.4.8 `version` - Show Version

Display version and build information.

```bash
ssm-proxy version [--short]
```

**Output:**

```
ssm-proxy version 1.0.0
  Build:      abc123def
  Go version: go1.21.5
  Platform:   darwin/arm64
  Built:      2024-01-15T10:00:00Z
```

---

## 4. Configuration File

### 4.1 Configuration File Locations

Configuration files are searched in the following order (first found is used):

1. Path specified by `--config` flag
2. `./ssm-proxy.yaml` (current directory)
3. `~/.ssm-proxy/config.yaml` (user home)
4. `/etc/ssm-proxy/config.yaml` (system-wide)

### 4.2 Configuration File Format

```yaml
# ~/.ssm-proxy/config.yaml

# AWS Configuration
aws:
  profile: default              # AWS profile name
  region: us-east-1            # Default AWS region

# Default Settings
defaults:
  local_ip: 169.254.169.1/30   # Default utun IP
  mtu: 1500                     # Default MTU
  keep_alive: 30s               # Keep-alive interval
  timeout: 30s                  # Connection timeout
  auto_reconnect: true          # Auto-reconnect on failure
  reconnect_delay: 5s           # Delay between reconnect attempts
  max_retries: 0                # 0 = unlimited

# Logging Configuration
logging:
  level: info                   # debug, info, warn, error
  file: ~/.ssm-proxy/logs/ssm-proxy.log
  max_size: 100                 # MB
  max_backups: 5                # Number of old log files to keep
  max_age: 30                   # Days
  compress: true                # Compress old logs
  log_packets: false            # Log individual packets (debug)

# Named Profiles for Quick Access
profiles:
  # Production VPC
  prod:
    instance_id: i-1234567890abcdef0
    cidr:
      - 10.0.0.0/8
    session_name: prod-vpc
    local_ip: 169.254.169.1/30

  # Development VPC
  dev:
    instance_tag: Environment=dev,Role=bastion
    cidr:
      - 172.16.0.0/12
    session_name: dev-vpc
    local_ip: 169.254.170.1/30
    mtu: 1400

  # Multi-CIDR example
  multi:
    instance_id: i-abcdef1234567890
    cidr:
      - 10.0.0.0/8
      - 172.16.0.0/12
      - 192.168.0.0/16
    session_name: multi-vpc

# Advanced Settings
advanced:
  # Buffer sizes (bytes)
  read_buffer_size: 65536
  write_buffer_size: 65536
  
  # Packet queue sizes
  tx_queue_size: 1000
  rx_queue_size: 1000
  
  # Monitoring
  metrics_enabled: false        # Enable Prometheus metrics
  metrics_port: 9090            # Metrics HTTP server port
  health_check_enabled: false   # Enable health check endpoint
  health_check_port: 9091       # Health check HTTP server port
```

### 4.3 Using Named Profiles

```bash
# Start using named profile
sudo ssm-proxy start --profile-name prod

# Override profile settings
sudo ssm-proxy start --profile-name prod --cidr 10.0.0.0/16

# List available profiles
ssm-proxy config list-profiles
```

---

## 5. Technical Implementation

### 5.1 Technology Stack

**Core:**
- **Language**: Go 1.21+
- **Platform**: macOS 11.0+ (darwin/amd64, darwin/arm64)

**Key Libraries:**
- **CLI Framework**: `github.com/spf13/cobra` (command structure)
- **Configuration**: `github.com/spf13/viper` (config management)
- **AWS SDK**: `github.com/aws/aws-sdk-go-v2` (SSM, EC2 APIs)
- **TUN Device**: `golang.org/x/sys/unix` (native syscalls) or `github.com/songgao/water`
- **Logging**: `github.com/sirupsen/logrus` or `go.uber.org/zap`
- **Packet Parsing**: `golang.org/x/net/ipv4` and `golang.org/x/net/ipv6` (optional)

**Build Tools:**
- **Build**: `make`, `go build`
- **Testing**: `go test`, `github.com/stretchr/testify`
- **Linting**: `golangci-lint`

### 5.2 Project Structure

```
ssm-proxy/
├── cmd/
│   └── ssm-proxy/
│       ├── main.go              # Entry point, privilege check
│       ├── root.go              # Root cobra command
│       ├── start.go             # start command
│       ├── stop.go              # stop command
│       ├── status.go            # status command
│       ├── list_instances.go    # list-instances command
│       ├── test.go              # test command
│       ├── routes.go            # routes command
│       ├── logs.go              # logs command
│       └── version.go           # version command
│
├── internal/
│   ├── tunnel/
│   │   ├── tun_darwin.go        # macOS utun device creation
│   │   ├── tun.go               # TUN interface definition
│   │   ├── packet.go            # IP packet read/write
│   │   └── mtu.go               # MTU handling
│   │
│   ├── routing/
│   │   ├── route_darwin.go      # macOS route add/delete
│   │   ├── route.go             # Routing interface
│   │   ├── table.go             # Route table inspection
│   │   └── cleanup.go           # Route cleanup on exit
│   │
│   ├── ssm/
│   │   ├── client.go            # SSM client wrapper
│   │   ├── session.go           # Session lifecycle management
│   │   ├── websocket.go         # WebSocket handling
│   │   ├── protocol.go          # Packet encapsulation protocol
│   │   └── keepalive.go         # Keep-alive mechanism
│   │
│   ├── forwarder/
│   │   ├── forwarder.go         # Packet forwarding logic
│   │   ├── tx.go                # Transmit (TUN → SSM)
│   │   ├── rx.go                # Receive (SSM → TUN)
│   │   └── stats.go             # Traffic statistics
│   │
│   ├── aws/
│   │   ├── client.go            # AWS SDK client setup
│   │   ├── ec2.go               # EC2 instance operations
│   │   ├── credentials.go       # Credential chain
│   │   └── regions.go           # Region handling
│   │
│   ├── session/
│   │   ├── manager.go           # Session state management
│   │   ├── store.go             # Persistent session store
│   │   ├── state.go             # Session state machine
│   │   └── registry.go          # Session registry (multiple sessions)
│   │
│   ├── config/
│   │   ├── config.go            # Configuration structs
│   │   ├── loader.go            # Load config from file
│   │   ├── validator.go         # Config validation
│   │   └── profiles.go          # Named profile handling
│   │
│   ├── daemon/
│   │   ├── daemon.go            # Daemonization
│   │   ├── pidfile.go           # PID file management
│   │   └── signals.go           # Signal handling
│   │
│   └── privilege/
│       ├── check.go             # Root privilege check
│       └── drop.go              # Drop privileges (future)
│
├── pkg/
│   ├── logger/
│   │   ├── logger.go            # Structured logger setup
│   │   └── packet.go            # Packet-level logging
│   │
│   ├── version/
│   │   └── version.go           # Version info (set at build time)
│   │
│   └── cidr/
│       └── cidr.go              # CIDR validation and utilities
│
├── test/
│   ├── integration/             # Integration tests
│   ├── fixtures/                # Test fixtures
│   └── mocks/                   # Mock implementations
│
├── scripts/
│   ├── install.sh               # Installation script
│   ├── uninstall.sh             # Uninstallation script
│   └── build.sh                 # Build script
│
├── docs/
│   ├── architecture.md          # Architecture documentation
│   ├── development.md           # Development guide
│   └── troubleshooting.md       # Troubleshooting guide
│
├── go.mod                       # Go module definition
├── go.sum                       # Go module checksums
├── Makefile                     # Build automation
├── README.md                    # User-facing documentation
├── SPECIFICATION.md             # This file
├── LICENSE                      # License file
├── .gitignore                   # Git ignore rules
└── .golangci.yml               # Linter configuration
```

### 5.3 macOS utun Device Implementation

#### Creating utun Device

macOS uses `utun` devices which are created by opening `/dev/utunN` where N is auto-assigned.

```go
// internal/tunnel/tun_darwin.go

package tunnel

import (
    "fmt"
    "golang.org/x/sys/unix"
    "os"
    "unsafe"
)

type TunDevice struct {
    name   string
    fd     *os.File
    mtu    int
}

// CreateTUN creates a new utun device on macOS
func CreateTUN() (*TunDevice, error) {
    // Open the utun control socket
    fd, err := unix.Socket(unix.AF_SYSTEM, unix.SOCK_DGRAM, unix.SYSPROTO_CONTROL)
    if err != nil {
        return nil, fmt.Errorf("failed to create utun socket: %w", err)
    }

    // Find the utun control ID
    ctlInfo := &unix.CtlInfo{}
    copy(ctlInfo.Name[:], "com.apple.net.utun_control")
    
    err = unix.IoctlCtlInfo(fd, ctlInfo)
    if err != nil {
        unix.Close(fd)
        return nil, fmt.Errorf("failed to get utun control info: %w", err)
    }

    // Connect to utun control (auto-assigns utun number)
    sc := &unix.SockaddrCtl{
        ID:   ctlInfo.Id,
        Unit: 0, // 0 = auto-assign next available
    }
    
    err = unix.Connect(fd, sc)
    if err != nil {
        unix.Close(fd)
        return nil, fmt.Errorf("failed to connect to utun control: %w", err)
    }

    // Get the assigned utun device name
    name, err := getDeviceName(fd)
    if err != nil {
        unix.Close(fd)
        return nil, err
    }

    return &TunDevice{
        name: name,
        fd:   os.NewFile(uintptr(fd), name),
        mtu:  1500,
    }, nil
}

// Read reads an IP packet from the utun device
func (t *TunDevice) Read(buf []byte) (int, error) {
    // macOS utun prepends 4-byte protocol header
    n, err := t.fd.Read(buf)
    if err != nil {
        return 0, err
    }
    
    // Skip the 4-byte protocol header
    if n < 4 {
        return 0, fmt.Errorf("packet too small")
    }
    
    // Move packet data to start of buffer (skip header)
    copy(buf, buf[4:n])
    return n - 4, nil
}

// Write writes an IP packet to the utun device
func (t *TunDevice) Write(buf []byte) (int, error) {
    // Prepend 4-byte protocol header (0x02 for IPv4, 0x1e for IPv6)
    proto := determineIPVersion(buf)
    
    packet := make([]byte, 4+len(buf))
    binary.BigEndian.PutUint32(packet[0:4], proto)
    copy(packet[4:], buf)
    
    n, err := t.fd.Write(packet)
    if err != nil {
        return 0, err
    }
    return n - 4, nil
}

// Close closes the utun device
func (t *TunDevice) Close() error {
    return t.fd.Close()
}

// Name returns the device name (e.g., "utun2")
func (t *TunDevice) Name() string {
    return t.name
}

func determineIPVersion(packet []byte) uint32 {
    if len(packet) < 1 {
        return 0x02 // default to IPv4
    }
    version := packet[0] >> 4
    if version == 6 {
        return 0x1e // IPv6 (AF_INET6 = 30 = 0x1e)
    }
    return 0x02 // IPv4 (AF_INET = 2)
}
```

#### Assigning IP Address to utun

```go
// internal/tunnel/configure.go

func ConfigureInterface(name string, ipAddr string, mtu int) error {
    // Use ifconfig to assign IP
    cmd := exec.Command("ifconfig", name, ipAddr, ipAddr)
    if err := cmd.Run(); err != nil {
        return fmt.Errorf("failed to assign IP: %w", err)
    }
    
    // Set MTU
    cmd = exec.Command("ifconfig", name, "mtu", fmt.Sprintf("%d", mtu))
    if err := cmd.Run(); err != nil {
        return fmt.Errorf("failed to set MTU: %w", err)
    }
    
    // Bring interface up
    cmd = exec.Command("ifconfig", name, "up")
    if err := cmd.Run(); err != nil {
        return fmt.Errorf("failed to bring interface up: %w", err)
    }
    
    return nil
}
```

### 5.4 macOS Routing Implementation

```go
// internal/routing/route_darwin.go

package routing

import (
    "fmt"
    "os/exec"
    "strings"
)

type Route struct {
    CIDR      string
    Interface string
}

// AddRoute adds a route to the macOS routing table
func AddRoute(cidr, interfaceName string) error {
    // Parse CIDR to get network and mask
    network, mask, err := parseCIDR(cidr)
    if err != nil {
        return err
    }
    
    // Execute: route add -net <network> -netmask <mask> -interface <interface>
    cmd := exec.Command("route", "add", "-net", network, "-netmask", mask, "-interface", interfaceName)
    output, err := cmd.CombinedOutput()
    if err != nil {
        return fmt.Errorf("failed to add route: %s: %w", string(output), err)
    }
    
    return nil
}

// DeleteRoute removes a route from the macOS routing table
func DeleteRoute(cidr string) error {
    network, mask, err := parseCIDR(cidr)
    if err != nil {
        return err
    }
    
    cmd := exec.Command("route", "delete", "-net", network, "-netmask", mask)
    output, err := cmd.CombinedOutput()
    if err != nil {
        return fmt.Errorf("failed to delete route: %s: %w", string(output), err)
    }
    
    return nil
}

// ListRoutes lists all routes managed by ssm-proxy
func ListRoutes() ([]Route, error) {
    cmd := exec.Command("netstat", "-rn")
    output, err := cmd.Output()
    if err != nil {
        return nil, err
    }
    
    routes := []Route{}
    lines := strings.Split(string(output), "\n")
    
    for _, line := range lines {
        if strings.Contains(line, "utun") {
            // Parse route line
            // Format: "10.0.0.0/8        169.254.169.1      UGSc      utun2"
            fields := strings.Fields(line)
            if len(fields) >= 2 {
                routes = append(routes, Route{
                    CIDR:      fields[0],
                    Interface: fields[len(fields)-1],
                })
            }
        }
    }
    
    return routes, nil
}

func parseCIDR(cidr string) (network, mask string, err error) {
    parts := strings.Split(cidr, "/")
    if len(parts) != 2 {
        return "", "", fmt.Errorf("invalid CIDR format: %s", cidr)
    }
    
    network = parts[0]
    
    // Convert CIDR prefix length to netmask
    prefixLen := parts[1]
    switch prefixLen {
    case "8":
        mask = "255.0.0.0"
    case "16":
        mask = "255.255.0.0"
    case "24":
        mask = "255.255.255.0"
    default:
        // Calculate mask from prefix length
        mask = cidrToNetmask(prefixLen)
    }
    
    return network, mask, nil
}
```

### 5.5 Packet Encapsulation Protocol

Since SSM Session Manager doesn't natively support raw IP packet forwarding, we define a simple framing protocol:

```
┌─────────────────────────────────────────────────────┐
│ Frame Header (8 bytes)                               │
│ ┌────────────────┬──────────────────────────────┐   │
│ │ Magic (4 bytes)│ Length (4 bytes, big-endian) │   │
│ │ 0x53534D50     │ uint32                        │   │
│ │ "SSMP"         │                               │   │
│ └────────────────┴──────────────────────────────┘   │
├─────────────────────────────────────────────────────┤
│ IP Packet (variable length)                         │
│ - Complete IP packet including headers              │
│ - IPv4 or IPv6                                       │
│ - Size = Length field above                         │
└─────────────────────────────────────────────────────┘
```

**Implementation:**

```go
// internal/ssm/protocol.go

package ssm

import (
    "encoding/binary"
    "fmt"
    "io"
)

const (
    MagicNumber = 0x53534D50 // "SSMP" in hex
    HeaderSize  = 8
)

// EncapsulatePacket wraps an IP packet with our protocol header
func EncapsulatePacket(packet []byte) []byte {
    frame := make([]byte, HeaderSize+len(packet))
    
    // Write magic number
    binary.BigEndian.PutUint32(frame[0:4], MagicNumber)
    
    // Write packet length
    binary.BigEndian.PutUint32(frame[4:8], uint32(len(packet)))
    
    // Write packet data
    copy(frame[8:], packet)
    
    return frame
}

// DecapsulatePacket extracts an IP packet from our protocol frame
func DecapsulatePacket(reader io.Reader) ([]byte, error) {
    // Read header
    header := make([]byte, HeaderSize)
    if _, err := io.ReadFull(reader, header); err != nil {
        return nil, fmt.Errorf("failed to read header: %w", err)
    }
    
    // Verify magic number
    magic := binary.BigEndian.Uint32(header[0:4])
    if magic != MagicNumber {
        return nil, fmt.Errorf("invalid magic number: 0x%x", magic)
    }
    
    // Read packet length
    length := binary.BigEndian.Uint32(header[4:8])
    if length > 65535 {
        return nil, fmt.Errorf("packet too large: %d bytes", length)
    }
    
    // Read packet data
    packet := make([]byte, length)
    if _, err := io.ReadFull(reader, packet); err != nil {
        return nil, fmt.Errorf("failed to read packet: %w", err)
    }
    
    return packet, nil
}
```

### 5.6 Main Packet Forwarding Loop

```go
// internal/forwarder/forwarder.go

package forwarder

type Forwarder struct {
    tun     *tunnel.TunDevice
    ssm     *ssm.Session
    stats   *Stats
    stopCh  chan struct{}
}

func (f *Forwarder) Start() error {
    // Start TUN → SSM forwarding
    go f.tunToSSM()
    
    // Start SSM → TUN forwarding
    go f.ssmToTUN()
    
    return nil
}

func (f *Forwarder) tunToSSM() {
    buf := make([]byte, 65535)
    
    for {
        select {
        case <-f.stopCh:
            return
        default:
        }
        
        // Read IP packet from TUN device
        n, err := f.tun.Read(buf)
        if err != nil {
            log.Errorf("TUN read error: %v", err)
            continue
        }
        
        packet := buf[:n]
        
        // Log packet (if debug enabled)
        logPacket("TX", packet)
        
        // Encapsulate packet
        frame := ssm.EncapsulatePacket(packet)
        
        // Send through SSM tunnel
        if err := f.ssm.Write(frame); err != nil {
            log.Errorf("SSM write error: %v", err)
            continue
        }
        
        // Update stats
        f.stats.AddTX(n)
    }
}

func (f *Forwarder) ssmToTUN() {
    for {
        select {
        case <-f.stopCh:
            return
        default:
        }
        
        // Read and decapsulate packet from SSM
        packet, err := ssm.DecapsulatePacket(f.ssm.Reader())
        if err != nil {
            log.Errorf("SSM read error: %v", err)
            continue
        }
        
        // Log packet (if debug enabled)
        logPacket("RX", packet)
        
        // Write to TUN device
        if _, err := f.tun.Write(packet); err != nil {
            log.Errorf("TUN write error: %v", err)
            continue
        }
        
        // Update stats
        f.stats.AddRX(len(packet))
    }
}

func (f *Forwarder) Stop() {
    close(f.stopCh)
}
```

### 5.7 AWS IAM Permissions

**User/Role Permissions:**

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "SSMSessionManagement",
      "Effect": "Allow",
      "Action": [
        "ssm:StartSession",
        "ssm:TerminateSession",
        "ssm:ResumeSession",
        "ssm:DescribeSessions",
        "ssm:GetConnectionStatus"
      ],
      "Resource": [
        "arn:aws:ec2:*:*:instance/*",
        "arn:aws:ssm:*::document/AWS-StartInteractiveCommand",
        "arn:aws:ssm:*:*:session/${aws:username}-*"
      ],
      "Condition": {
        "StringEquals": {
          "ssm:SessionDocumentAccessCheck": "true"
        }
      }
    },
    {
      "Sid": "EC2ReadOnly",
      "Effect": "Allow",
      "Action": [
        "ec2:DescribeInstances",
        "ec2:DescribeInstanceStatus",
        "ec2:DescribeTags"
      ],
      "Resource": "*"
    }
  ]
}
```

**EC2 Instance Role:**

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
    },
    {
      "Effect": "Allow",
      "Action": [
        "s3:GetEncryptionConfiguration"
      ],
      "Resource": "*"
    }
  ]
}
```

Or simply attach the AWS managed policy: `arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore`

### 5.8 EC2 Instance Setup

The EC2 instance needs:

1. **SSM Agent** (pre-installed on Amazon Linux 2, Ubuntu 16.04+)
2. **IP Forwarding enabled:**
   ```bash
   sudo sysctl -w net.ipv4.ip_forward=1
   echo "net.ipv4.ip_forward=1" | sudo tee -a /etc/sysctl.conf
   ```

3. **Companion agent** (ssm-proxy-agent) to receive packets:
   ```bash
   # Install agent
   curl -L https://github.com/user/ssm-proxy/releases/latest/download/ssm-proxy-agent-linux-amd64 -o /usr/local/bin/ssm-proxy-agent
   chmod +x /usr/local/bin/ssm-proxy-agent
   
   # Run as systemd service
   sudo systemctl start ssm-proxy-agent
   ```

4. **Security Group**: Outbound HTTPS (443) to SSM endpoints

---

## 6. Use Cases & Examples

### 6.1 Database Access

**Scenario:** Connect to RDS PostgreSQL in private subnet

```bash
# Start proxy
sudo ssm-proxy start \
  --instance-id i-1234567890abcdef0 \
  --cidr 10.0.0.0/16

# Connect to database - NO special configuration needed!
psql -h mydb.abc.us-east-1.rds.amazonaws.com -p 5432 -U admin -d myapp

# Or with private IP
psql -h 10.0.1.5 -p 5432 -U admin -d myapp
```

### 6.2 Redis/ElastiCache

```bash
# Start proxy
sudo ssm-proxy start \
  --instance-id i-1234567890abcdef0 \
  --cidr 10.0.0.0/8

# Use redis-cli normally
redis-cli -h master.abc.cache.amazonaws.com -p 6379

# Or with private IP
redis-cli -h 10.0.2.25 -p 6379
```

### 6.3 Internal Web Applications

```bash
# Start proxy
sudo ssm-proxy start \
  --instance-id i-1234567890abcdef0 \
  --cidr 10.0.0.0/8

# Access with curl
curl http://10.0.3.100:8080/api/health

# Or with browser
open http://10.0.3.100:8080
```

### 6.4 Multiple Services Simultaneously

```bash
# Start proxy for entire VPC CIDR
sudo ssm-proxy start \
  --instance-id i-1234567890abcdef0 \
  --cidr 10.0.0.0/8

# Now use multiple services transparently:

# Terminal 1: Database
psql -h 10.0.1.5 -p 5432 mydb

# Terminal 2: Redis
redis-cli -h 10.0.2.25

# Terminal 3: HTTP API
curl http://10.0.3.100:8080

# Terminal 4: SSH to another instance
ssh ec2-user@10.0.4.50

# All work simultaneously through the same tunnel!
```

### 6.5 Multiple CIDR Blocks

```bash
# Route multiple VPC CIDR blocks
sudo ssm-proxy start \
  --instance-id i-1234567890abcdef0 \
  --cidr 10.0.0.0/8 \
  --cidr 172.16.0.0/12 \
  --cidr 192.168.0.0/16

# Access resources in any of these ranges
psql -h 10.0.1.5 -p 5432 db1
psql -h 172.16.1.10 -p 5432 db2
psql -h 192.168.1.20 -p 5432 db3
```

### 6.6 Development Workflow

```bash
# Morning: Start proxy
sudo ssm-proxy start \
  --instance-tag Environment=dev \
  --cidr 10.10.0.0/16 \
  --daemon

# Work all day with transparent access
psql -h dev-db.internal ...
curl http://dev-api.internal:8080 ...
redis-cli -h dev-cache.internal ...

# Evening: Stop proxy
sudo ssm-proxy stop
```

### 6.7 Using Named Profiles

```yaml
# ~/.ssm-proxy/config.yaml
profiles:
  prod:
    instance_id: i-prod123
    cidr: [10.0.0.0/8]
  
  staging:
    instance_id: i-stage123
    cidr: [10.10.0.0/16]
```

```bash
# Switch between environments easily
sudo ssm-proxy start --profile-name prod
# ... do prod work ...
sudo ssm-proxy stop

sudo ssm-proxy start --profile-name staging
# ... do staging work ...
sudo ssm-proxy stop
```

---

## 7. Security Considerations

### 7.1 Privileges

- **Root Required**: Creating TUN devices and modifying routing tables requires root
- **Privilege Scope**: Only network configuration, no other system modifications
- **Audit**: All privileged operations are logged

### 7.2 Network Security

- **Encryption**: All traffic through SSM tunnel is TLS-encrypted
- **No Inbound Ports**: EC2 bastion requires no inbound security group rules
- **IAM-based Auth**: No SSH keys, uses AWS IAM for authentication
- **Scope Limiting**: Only specified CIDR blocks are routed through tunnel

### 7.3 Audit & Compliance

- **CloudTrail**: All SSM sessions logged to CloudTrail
- **SSM Session Manager**: Built-in session recording available
- **Local Logging**: Optional local packet logging for debugging

### 7.4 Best Practices

1. **Limit CIDR Blocks**: Only route necessary networks
2. **Use Named Sessions**: Track what's running with meaningful names
3. **Monitor Sessions**: Use `ssm-proxy status` regularly
4. **Clean Shutdown**: Always use `ssm-proxy stop` (not kill -9)
5. **IAM Policies**: Use least-privilege IAM policies
6. **EC2 Security Groups**: Limit EC2 instance egress to necessary targets

---

## 8. Error Handling & Troubleshooting

### 8.1 Common Errors

#### "Not running as root"
```
Error: This command requires root privileges
```
**Solution:** Run with `sudo ssm-proxy start ...`

#### "SSM Agent not connected"
```
Error: SSM Agent is not connected on instance i-1234567890abcdef0
```
**Solutions:**
- Verify SSM Agent is running: `sudo systemctl status amazon-ssm-agent`
- Check instance has IAM role with `AmazonSSMManagedInstanceCore`
- Verify VPC has connectivity to SSM endpoints (VPC endpoints or IGW)

#### "Failed to create utun device"
```
Error: Failed to create utun device: operation not permitted
```
**Solutions:**
- Ensure running with `sudo`
- Check macOS security settings (System Preferences → Security)
- Restart and try again

#### "Route already exists"
```
Error: Failed to add route: route already exists
```
**Solutions:**
- Another session may be using same CIDR: `sudo ssm-proxy status`
- Clean up stale routes: `sudo ssm-proxy routes cleanup`
- Use different CIDR or stop conflicting session

#### "Connection timeout"
```
Error: Failed to establish SSM session: timeout
```
**Solutions:**
- Verify internet connectivity
- Check AWS credentials are valid
- Verify instance is running and SSM Agent is connected
- Check security groups allow outbound HTTPS (443)

### 8.2 Debugging

```bash
# Enable verbose logging
sudo ssm-proxy start --verbose ...

# Enable debug logging (very detailed)
sudo ssm-proxy start --debug ...

# Log packets (extreme verbosity)
sudo ssm-proxy start --debug --log-packets ...

# View logs
ssm-proxy logs --follow

# Check routing table
netstat -rn | grep utun

# Test connectivity
ssm-proxy test 10.0.1.5:5432

# Verify EC2 instance
ssm-proxy list-instances --ssm-only
```

### 8.3 Recovery from Crashes

If ssm-proxy crashes without cleaning up:

```bash
# Check for stale sessions
sudo ssm-proxy status

# Clean up orphaned routes
sudo ssm-proxy routes cleanup

# Manually remove route if needed
sudo route delete -net 10.0.0.0/8

# Verify cleanup
netstat -rn | grep utun
```

---

## 9. Testing Strategy

### 9.1 Unit Tests

```bash
# Run all unit tests
make test

# Run with coverage
make test-coverage

# Run specific package
go test -v ./internal/tunnel/
```

**Test Coverage:**
- TUN device creation/cleanup
- Route add/delete
- Packet encapsulation/decapsulation
- CIDR parsing and validation
- Configuration loading
- Session state management

### 9.2 Integration Tests

```bash
# Run integration tests (requires AWS credentials and EC2 instance)
make test-integration
```

**Test Scenarios:**
- End-to-end tunnel creation
- Packet forwarding through tunnel
- Reconnection after failure
- Multiple concurrent sessions
- Route cleanup on exit

### 9.3 Manual Testing Checklist

- [ ] Start tunnel with single CIDR
- [ ] Start tunnel with multiple CIDRs
- [ ] Connect to PostgreSQL database
- [ ] Connect to Redis
- [ ] Access HTTP API with curl
- [ ] Access internal website with browser
- [ ] Multiple concurrent connections
- [ ] Stop tunnel and verify cleanup
- [ ] Crash recovery (kill -9, then cleanup)
- [ ] Reconnection after network interruption
- [ ] Different AWS profiles/regions
- [ ] Named profile usage

### 9.4 Performance Testing

```bash
# Test throughput
iperf3 -c 10.0.1.100 -p 5201

# Test latency
ping 10.0.1.5

# Monitor traffic
sudo ssm-proxy status --show-stats --watch
```

---

## 10. Build & Release

### 10.1 Build

```bash
# Development build
make build

# Production build (optimized, stripped)
make build-release

# Cross-compile for both architectures
make build-all

# Install locally
sudo make install
```

### 10.2 Versioning

Version is set at build time:

```bash
# Build with version
make build VERSION=1.2.3

# Or use git tag
git tag v1.2.3
make build
```

### 10.3 Release Process

1. Tag release: `git tag v1.2.3`
2. Build binaries: `make build-release`
3. Create GitHub release
4. Upload binaries:
   - `ssm-proxy-darwin-amd64`
   - `ssm-proxy-darwin-arm64`
5. Update Homebrew formula (if applicable)

### 10.4 Makefile

```makefile
# Makefile

.PHONY: build test install clean

VERSION ?= $(shell git describe --tags --always --dirty)
BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS := -ldflags "-X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)"

build:
	go build $(LDFLAGS) -o bin/ssm-proxy ./cmd/ssm-proxy

build-release:
	CGO_ENABLED=0 go build $(LDFLAGS) -o bin/ssm-proxy -trimpath ./cmd/ssm-proxy

build-all:
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o bin/ssm-proxy-darwin-amd64 ./cmd/ssm-proxy
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o bin/ssm-proxy-darwin-arm64 ./cmd/ssm-proxy

test:
	go test -v -race ./...

test-coverage:
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

install: build
	install -m 755 bin/ssm-proxy /usr/local/bin/ssm-proxy

uninstall:
	rm -f /usr/local/bin/ssm-proxy

clean:
	rm -rf bin/
	rm -f coverage.out

lint:
	golangci-lint run

.DEFAULT_GOAL := build
```

---

## 11. Future Enhancements

### Phase 2 (3-6 months)
- [ ] DNS proxy for split-horizon DNS resolution
- [ ] Multiple concurrent tunnels to different EC2 instances
- [ ] Load balancing across multiple bastion hosts
- [ ] Web UI for monitoring and management
- [ ] Health checks and automatic failover
- [ ] Prometheus metrics export

### Phase 3 (6-12 months)
- [ ] Linux support
- [ ] Windows support (TAP driver)
- [ ] IPv6 support
- [ ] UDP performance optimizations
- [ ] Automatic EC2 instance selection (closest, healthiest)
- [ ] Integration with AWS Organizations (cross-account)
- [ ] Session Manager session recording integration

### Long-term
- [ ] GUI application (native macOS app)
- [ ] Menu bar widget for easy control
- [ ] Notification center integration
- [ ] Auto-start on login option
- [ ] Profile presets for common scenarios

---

## 12. Success Metrics

- **Installation Count**: Track downloads and brew installs
- **User Satisfaction**: GitHub stars, issues, feedback
- **Performance**: Throughput (target: 50+ Mbps), latency (target: <20ms overhead)
- **Reliability**: Uptime, successful reconnections
- **Community**: Contributors, pull requests, forks

---

## Appendix A: macOS Network Stack

### macOS utun Devices
- `/dev/utun0` - typically used by VPN clients
- `/dev/utun1`, `/dev/utun2`, ... - available for applications
- Controlled via `SYSPROTO_CONTROL` socket API
- Require `root` or entitlements for creation

### Routing Commands
```bash
# View routing table
netstat -rn
route get 10.0.1.5

# Add route
sudo route add -net 10.0.0.0/8 -interface utun2

# Delete route
sudo route delete -net 10.0.0.0/8

# Test route
traceroute 10.0.1.5
```

### Firewall Considerations
macOS firewall operates at application level by default and won't block utun traffic.

---

## Appendix B: AWS SSM Architecture

### SSM Session Manager
- Uses HTTPS (443) for all communication
- WebSocket-based for bi-directional streaming
- Regional service (us-east-1, us-west-2, etc.)
- Endpoints: `ssm.<region>.amazonaws.com`, `ssmmessages.<region>.amazonaws.com`

### Session Documents
- `AWS-StartInteractiveCommand` - Custom commands
- `AWS-StartSSHSession` - SSH-over-SSM
- `AWS-StartPortForwardingSession` - Port forwarding

### Bandwidth & Limits
- Throughput: Typically 10-100 Mbps (varies by region/time)
- Packet size: Up to 1MB per WebSocket message
- Latency: +5-20ms overhead vs direct connection
- Session duration: Up to 60 days (configurable)

---

## Appendix C: Comparison with Alternatives

### vs. Traditional VPN
| Feature | ssm-proxy | VPN |
|---------|-----------|-----|
| Installation | Single binary | Client software + server setup |
| Authentication | AWS IAM | Certificates/keys |
| Infrastructure | Uses existing EC2 | Dedicated VPN server |
| Audit | CloudTrail | VPN logs |
| Cost | EC2 instance only | VPN server + licenses |

### vs. SSH Bastion + Port Forwarding
| Feature | ssm-proxy | SSH Bastion |
|---------|-----------|-------------|
| Key Management | None (IAM) | SSH keys required |
| Public IP | Not required | Required |
| Multiple Services | Automatic | Manual per service |
| Security Groups | No inbound rules | SSH port (22) open |

### vs. AWS Client VPN
| Feature | ssm-proxy | AWS Client VPN |
|---------|-----------|----------------|
| Setup Complexity | Low | High |
| Cost | $5-20/mo | $75+/mo |
| User Management | IAM | Certificate authority |
| Scalability | Per user | Shared endpoint |

---

**End of Specification**
