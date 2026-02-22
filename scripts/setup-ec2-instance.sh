#!/bin/bash
#
# SSM Proxy - EC2 Instance Setup Script
#
# This script configures an EC2 instance to act as a proxy endpoint
# for ssm-proxy. It enables IP forwarding, ensures SSM Agent is running,
# and sets up the necessary network configuration.
#
# Usage:
#   sudo ./setup-ec2-instance.sh
#
# Requirements:
#   - Must run as root (sudo)
#   - Amazon Linux 2/2023, Ubuntu, RHEL, or Debian
#   - Instance must have IAM role with AmazonSSMManagedInstanceCore policy
#

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Logging functions
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

# Check if running as root
if [[ $EUID -ne 0 ]]; then
    log_error "This script must be run as root"
    echo "Please run: sudo $0"
    exit 1
fi

log_header "SSM Proxy - EC2 Instance Setup"

# Detect OS
if [[ -f /etc/os-release ]]; then
    . /etc/os-release
    OS=$ID
    VERSION=$VERSION_ID
    log_info "Detected OS: $NAME $VERSION"
else
    log_error "Cannot detect operating system"
    exit 1
fi

# Step 1: Enable IP Forwarding
log_header "Step 1: Enable IP Forwarding"

CURRENT_IP_FORWARD=$(sysctl -n net.ipv4.ip_forward)

if [[ "$CURRENT_IP_FORWARD" == "1" ]]; then
    log_info "IP forwarding is already enabled"
else
    log_info "Enabling IP forwarding..."
    sysctl -w net.ipv4.ip_forward=1

    # Make it persistent across reboots
    if grep -q "^net.ipv4.ip_forward" /etc/sysctl.conf; then
        sed -i 's/^net.ipv4.ip_forward.*/net.ipv4.ip_forward=1/' /etc/sysctl.conf
    else
        echo "net.ipv4.ip_forward=1" >> /etc/sysctl.conf
    fi

    log_info "IP forwarding enabled and persisted"
fi

# Step 2: Check/Install SSM Agent
log_header "Step 2: SSM Agent Configuration"

# Function to check if SSM Agent is running
check_ssm_agent() {
    if systemctl is-active --quiet amazon-ssm-agent; then
        return 0
    elif systemctl is-active --quiet snap.amazon-ssm-agent.amazon-ssm-agent.service; then
        return 0
    else
        return 1
    fi
}

if check_ssm_agent; then
    log_info "SSM Agent is running"

    # Show SSM Agent status
    if command -v amazon-ssm-agent &> /dev/null; then
        SSM_VERSION=$(amazon-ssm-agent -version 2>/dev/null || echo "unknown")
        log_info "SSM Agent version: $SSM_VERSION"
    fi
else
    log_warn "SSM Agent is not running. Attempting to start..."

    case "$OS" in
        amzn|rhel|centos|fedora)
            if ! systemctl start amazon-ssm-agent; then
                log_error "Failed to start SSM Agent"
                log_warn "You may need to install it first:"
                echo "  sudo yum install -y amazon-ssm-agent"
                exit 1
            fi
            systemctl enable amazon-ssm-agent
            ;;

        ubuntu|debian)
            if command -v snap &> /dev/null && snap list amazon-ssm-agent &> /dev/null; then
                # Using snap
                if ! snap start amazon-ssm-agent; then
                    log_error "Failed to start SSM Agent (snap)"
                    exit 1
                fi
            elif systemctl list-unit-files amazon-ssm-agent.service &> /dev/null; then
                # Using systemd
                if ! systemctl start amazon-ssm-agent; then
                    log_error "Failed to start SSM Agent"
                    log_warn "You may need to install it first:"
                    echo "  sudo snap install amazon-ssm-agent --classic"
                    exit 1
                fi
                systemctl enable amazon-ssm-agent
            else
                log_error "SSM Agent not found"
                log_warn "Install it with:"
                echo "  sudo snap install amazon-ssm-agent --classic"
                exit 1
            fi
            ;;

        *)
            log_error "Unsupported OS: $OS"
            exit 1
            ;;
    esac

    log_info "SSM Agent started successfully"
fi

# Step 3: Configure Firewall (if applicable)
log_header "Step 3: Firewall Configuration"

# Check if firewalld is running
if systemctl is-active --quiet firewalld; then
    log_info "Detected firewalld"

    # Enable masquerading for IP forwarding
    if ! firewall-cmd --query-masquerade &> /dev/null; then
        log_info "Enabling masquerading in firewalld..."
        firewall-cmd --add-masquerade --permanent
        firewall-cmd --reload
        log_info "Masquerading enabled"
    else
        log_info "Masquerading is already enabled"
    fi

# Check if ufw is running
elif command -v ufw &> /dev/null && ufw status | grep -q "active"; then
    log_info "Detected ufw (Ubuntu firewall)"
    log_warn "UFW detected - you may need to configure it manually"
    echo "  To allow forwarding, edit /etc/default/ufw and set:"
    echo "  DEFAULT_FORWARD_POLICY=\"ACCEPT\""

# Check if iptables has rules
elif iptables -L -n | grep -q "Chain"; then
    log_info "iptables detected"

    # Check if FORWARD chain has DROP policy
    if iptables -L FORWARD -n | head -1 | grep -q "DROP"; then
        log_warn "FORWARD chain has DROP policy"
        log_warn "You may need to add rules to allow forwarding:"
        echo "  sudo iptables -A FORWARD -j ACCEPT"
    else
        log_info "FORWARD chain policy looks OK"
    fi
else
    log_info "No active firewall detected"
fi

# Step 4: Network Configuration
log_header "Step 4: Network Configuration"

# Disable source/destination check (note to user)
INSTANCE_ID=$(ec2-metadata --instance-id 2>/dev/null | cut -d " " -f 2 || echo "unknown")

if [[ "$INSTANCE_ID" != "unknown" ]]; then
    log_info "Instance ID: $INSTANCE_ID"
    log_warn "Note: Ensure source/destination check is DISABLED on this instance"
    echo "  AWS Console: EC2 → Instance → Actions → Networking → Change Source/Dest. Check"
    echo "  Or use AWS CLI:"
    echo "  aws ec2 modify-instance-attribute --instance-id $INSTANCE_ID --no-source-dest-check"
else
    log_warn "Could not determine Instance ID"
    log_warn "Make sure to disable source/destination check on this EC2 instance"
fi

# Step 5: Security Group Check
log_header "Step 5: Security Group Requirements"

log_info "Ensure your security group allows:"
echo "  • Outbound HTTPS (443) to SSM endpoints"
echo "  • Outbound traffic to target resources in your VPC"
echo ""
echo "  Inbound rules: NONE required (SSM uses outbound connections)"

# Step 6: IAM Role Check
log_header "Step 6: IAM Role Verification"

# Try to get IAM role
IAM_ROLE=$(ec2-metadata --iam-info 2>/dev/null | grep -o 'InstanceProfileArn.*' | cut -d '/' -f2 || echo "")

if [[ -n "$IAM_ROLE" ]]; then
    log_info "IAM Role attached: $IAM_ROLE"
    log_warn "Verify this role has the 'AmazonSSMManagedInstanceCore' policy"
else
    log_error "No IAM role detected!"
    echo ""
    echo "This instance MUST have an IAM role with the following policy:"
    echo "  • AmazonSSMManagedInstanceCore (AWS managed policy)"
    echo ""
    echo "To attach a role:"
    echo "  1. Create an IAM role with AmazonSSMManagedInstanceCore policy"
    echo "  2. EC2 Console → Instance → Actions → Security → Modify IAM Role"
    exit 1
fi

# Step 7: VPC Endpoints Check
log_header "Step 7: VPC Endpoints"

log_info "For private subnet instances, ensure these VPC endpoints exist:"
echo "  • com.amazonaws.<region>.ssm"
echo "  • com.amazonaws.<region>.ssmmessages"
echo "  • com.amazonaws.<region>.ec2messages"
echo ""
echo "Or ensure the instance can reach the internet via NAT Gateway/Internet Gateway"

# Step 8: Test SSM Connectivity
log_header "Step 8: Testing SSM Connectivity"

log_info "Checking SSM connectivity..."

# Give SSM Agent time to register
sleep 3

# Try to get SSM ping status
if aws ssm describe-instance-information --filters "Key=InstanceIds,Values=$INSTANCE_ID" --query "InstanceInformationList[0].PingStatus" --output text 2>/dev/null | grep -q "Online"; then
    log_info "SSM Agent is online and connected!"
else
    log_warn "Could not verify SSM connectivity"
    echo "Wait a few minutes for SSM Agent to register, then verify with:"
    echo "  aws ssm describe-instance-information --filters \"Key=InstanceIds,Values=$INSTANCE_ID\""
fi

# Final Summary
log_header "Setup Complete!"

echo ""
echo "This EC2 instance is now configured for SSM Proxy."
echo ""
echo "Summary of configuration:"
echo "  ✓ IP forwarding: enabled"
echo "  ✓ SSM Agent: running"
echo "  ✓ Instance ID: $INSTANCE_ID"
echo ""
echo "Next steps:"
echo "  1. Verify SSM connectivity from your local machine:"
echo "     ssm-proxy list-instances --ssm-only"
echo ""
echo "  2. Start the proxy from your local machine:"
echo "     sudo ssm-proxy start --instance-id $INSTANCE_ID --cidr 10.0.0.0/8"
echo ""
echo "  3. Test connectivity:"
echo "     ssm-proxy test <target-ip>:<port>"
echo ""

log_info "Setup script completed successfully"
echo ""
