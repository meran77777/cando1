#!/usr/bin/env bash
# Enable BBR congestion control and enlarge network buffers.
#
# On lossy international links (e.g. Iran <-> Europe) the default CUBIC
# congestion control collapses throughput on packet loss. BBR keeps the pipe
# full regardless of loss and typically multiplies tunnel throughput several
# times over. Run this on BOTH ends (server and client), then reconnect.
#
#   sudo bash scripts/tune-bbr.sh
set -euo pipefail

if [[ $EUID -ne 0 ]]; then
  echo "Please run as root (sudo bash scripts/tune-bbr.sh)"; exit 1
fi

CONF=/etc/sysctl.d/99-cando1.conf
cat > "$CONF" <<'EOF'
# cando1 network tuning
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr

# Larger socket buffers for high bandwidth-delay-product links
net.core.rmem_max = 67108864
net.core.wmem_max = 67108864
net.core.rmem_default = 16777216
net.core.wmem_default = 16777216
net.ipv4.tcp_rmem = 4096 87380 67108864
net.ipv4.tcp_wmem = 4096 65536 67108864

# Helpful on flaky links
net.ipv4.tcp_mtu_probing = 1
net.ipv4.tcp_fastopen = 3
net.core.netdev_max_backlog = 32768
net.ipv4.tcp_slow_start_after_idle = 0

# UDP buffers (matters for the KCP transport)
net.ipv4.udp_rmem_min = 8192
net.ipv4.udp_wmem_min = 8192
EOF

# Load bbr if it is a module.
modprobe tcp_bbr 2>/dev/null || true

sysctl --system >/dev/null

echo "Applied. Current settings:"
echo -n "  congestion control: "; sysctl -n net.ipv4.tcp_congestion_control
echo -n "  qdisc:              "; sysctl -n net.core.default_qdisc
echo -n "  bbr available:      "; sysctl -n net.ipv4.tcp_available_congestion_control
echo
echo "If it does not say 'bbr', your kernel may be old (need >= 4.9). Then upgrade the kernel."
