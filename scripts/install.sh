#!/usr/bin/env bash
#
# cando1 — one-command installer.
#
#   bash <(curl -fsSL https://raw.githubusercontent.com/meran77777/cando1/main/scripts/install.sh)
#
# What it does (all of it, on any Ubuntu/Debian and any CPU):
#   1. Detects your OS and CPU architecture (amd64, arm64, armv7, 386, riscv64…).
#   2. Installs the few packages it needs (curl, tar, git, ca-certificates).
#   3. Gets cando1: downloads a prebuilt release binary for your arch straight
#      from the GitHub release (no Go needed) and verifies its SHA-256. Only if
#      no matching binary exists does it fall back to building from source
#      (auto-installing a Go toolchain). Set CANDO1_METHOD=release to require the
#      prebuilt binary and never build. Every transport is compiled in —
#      tls / wss / ws / tcp+obfs / kcp — plus smux multiplexing, the connection
#      pool and auto-reconnect. Nothing is left out.
#   4. Installs the binary to /usr/local/bin/cando1.
#   5. Enables BBR + tuned network buffers (the single biggest speed win).
#   6. Optionally installs a systemd service for auto-start on boot.
#   7. Drops you into the interactive setup wizard (when run in a terminal).
#
# Everything is overridable with environment variables — see CONFIG below.
# Re-runs are safe (idempotent): it upgrades in place.
set -euo pipefail

# ----------------------------- CONFIG ---------------------------------------
REPO="${CANDO1_REPO:-meran77777/cando1}"     # GitHub owner/name
BRANCH="${CANDO1_BRANCH:-main}"              # branch to build from source
BIN_DIR="${CANDO1_BIN_DIR:-/usr/local/bin}"
BIN="${BIN_DIR}/cando1"
GO_FALLBACK="${CANDO1_GO_VERSION:-go1.22.10}" # used if we can't query the latest
DO_BBR="${CANDO1_BBR:-1}"                    # 1 = apply BBR/sysctl tuning
DO_SERVICE="${CANDO1_SERVICE:-ask}"          # 1 | 0 | ask
# How to obtain the binary:
#   auto    (default) — prebuilt release binary; fall back to building from source
#   release           — prebuilt release binary ONLY; never build, never needs Go
#   source            — always build from source (installs a Go toolchain if needed)
METHOD="${CANDO1_METHOD:-auto}"
# Pin the release to install from ("latest" or a tag like v1.2.3).
RELEASE="${CANDO1_RELEASE:-latest}"
# Module proxy: goproxy.cn first helps on restricted networks (e.g. Iran).
export GOPROXY="${GOPROXY:-https://goproxy.cn,https://proxy.golang.org,direct}"
# go.sum in the repo already pins every dependency hash, so integrity is enforced
# at build time regardless; GOSUMDB=off just avoids a call to a checksum server
# that some restricted networks block. Override by exporting GOSUMDB yourself.
export GOSUMDB="${GOSUMDB:-off}"
export GOTOOLCHAIN="${GOTOOLCHAIN:-local}"   # never auto-download a toolchain mid-build
# ----------------------------------------------------------------------------

C_RESET='\033[0m'; C_G='\033[32m'; C_Y='\033[33m'; C_R='\033[31m'; C_B='\033[1;36m'
info() { printf "${C_B}==>${C_RESET} %s\n" "$*"; }
ok()   { printf "${C_G}  ok${C_RESET} %s\n" "$*"; }
warn() { printf "${C_Y}  ! ${C_RESET} %s\n" "$*" >&2; }
die()  { printf "${C_R}error:${C_RESET} %s\n" "$*" >&2; exit 1; }

TMPDIR_BUILD=""
cleanup() { [ -n "$TMPDIR_BUILD" ] && rm -rf "$TMPDIR_BUILD" 2>/dev/null || true; }
trap cleanup EXIT

# --- privilege: we need root for /usr/local, sysctl and systemd -------------
SUDO=""
ensure_root() {
  if [ "$(id -u)" -ne 0 ]; then
    if command -v sudo >/dev/null 2>&1; then
      SUDO="sudo"
      info "Some steps need root; you may be prompted for your password."
    else
      die "Please run as root (or install sudo). Try: su -c 'bash install.sh'"
    fi
  fi
}

# --- detect OS + arch -------------------------------------------------------
OS=""; ARCH=""; GOARCH=""; GOARM=""
detect_platform() {
  case "$(uname -s)" in
    Linux)  OS="linux" ;;
    Darwin) OS="darwin" ;;
    *) die "unsupported OS: $(uname -s). cando1's installer targets Linux (and macOS from source)." ;;
  esac
  local m; m="$(uname -m)"
  case "$m" in
    x86_64|amd64)          ARCH="amd64";   GOARCH="amd64" ;;
    aarch64|arm64)         ARCH="arm64";   GOARCH="arm64" ;;
    armv7l|armv7|armhf)    ARCH="armv7";   GOARCH="arm"; GOARM="7" ;;
    armv6l|armv6)          ARCH="armv6";   GOARCH="arm"; GOARM="6" ;;
    i386|i686)             ARCH="386";     GOARCH="386" ;;
    riscv64)               ARCH="riscv64"; GOARCH="riscv64" ;;
    ppc64le)               ARCH="ppc64le"; GOARCH="ppc64le" ;;
    s390x)                 ARCH="s390x";   GOARCH="s390x" ;;
    *) die "unrecognised CPU architecture: $m" ;;
  esac
  ok "platform: ${OS}/${ARCH}  ($(nproc 2>/dev/null || echo '?') CPU core(s))"
}

# --- package prerequisites --------------------------------------------------
install_prereqs() {
  local need=(curl tar)
  local missing=()
  for c in "${need[@]}"; do command -v "$c" >/dev/null 2>&1 || missing+=("$c"); done
  command -v git >/dev/null 2>&1 || missing+=(git)
  if [ "${#missing[@]}" -eq 0 ]; then ok "prerequisites present"; return; fi

  info "installing prerequisites: ${missing[*]}"
  if command -v apt-get >/dev/null 2>&1; then
    $SUDO apt-get update -y -q
    $SUDO env DEBIAN_FRONTEND=noninteractive apt-get install -y -q \
      ca-certificates curl tar git
  elif command -v dnf >/dev/null 2>&1; then
    $SUDO dnf install -y ca-certificates curl tar git
  elif command -v yum >/dev/null 2>&1; then
    $SUDO yum install -y ca-certificates curl tar git
  elif command -v apk >/dev/null 2>&1; then
    $SUDO apk add --no-cache ca-certificates curl tar git bash
  elif command -v pacman >/dev/null 2>&1; then
    $SUDO pacman -Sy --noconfirm ca-certificates curl tar git
  else
    warn "no known package manager found; make sure these exist: ${missing[*]}"
  fi
  ok "prerequisites ready"
}

# --- fetch: try a prebuilt release binary for this arch ---------------------
# The release workflow publishes a static binary for every Linux arch the
# installer can detect (amd64, arm64, armv7, armv6, 386, riscv64, ppc64le,
# s390x), each named cando1-linux-<arch> with a matching .sha256 checksum.
# So on a Linux server this path never needs Go — the tunnel binary is the
# only thing that lands on disk.
fetch_release_binary() {
  [ "$OS" = "linux" ] || { warn "no prebuilt binaries for ${OS} — build from source"; return 1; }
  local asset="cando1-linux-${ARCH}"
  local base
  if [ "$RELEASE" = "latest" ]; then
    base="https://github.com/${REPO}/releases/latest/download"
  else
    base="https://github.com/${REPO}/releases/download/${RELEASE}"
  fi
  local url="${base}/${asset}"
  info "trying prebuilt binary: ${url}"
  local tmp; tmp="$(mktemp)"
  if ! { curl -fsSL --retry 3 -o "$tmp" "$url" 2>/dev/null && [ -s "$tmp" ]; }; then
    rm -f "$tmp"
    warn "no prebuilt binary for ${OS}/${ARCH} in the ${RELEASE} release"
    return 1
  fi
  # sanity: must be an ELF binary, not an HTML error page
  if ! head -c 4 "$tmp" | grep -qa 'ELF'; then
    rm -f "$tmp"
    warn "downloaded file is not a valid binary (got an error page?) — skipping"
    return 1
  fi
  # integrity: verify the published SHA-256 when available (best effort).
  local sum; sum="$(mktemp)"
  if curl -fsSL --retry 3 -o "$sum" "${url}.sha256" 2>/dev/null && [ -s "$sum" ]; then
    if command -v sha256sum >/dev/null 2>&1; then
      local want got
      want="$(awk '{print $1}' "$sum" | head -1)"
      got="$(sha256sum "$tmp" | awk '{print $1}')"
      if [ -n "$want" ] && [ "$want" != "$got" ]; then
        rm -f "$tmp" "$sum"
        die "checksum mismatch for ${asset} (expected ${want}, got ${got}) — refusing to install"
      fi
      ok "checksum verified"
    fi
  fi
  rm -f "$sum"
  $SUDO install -m 0755 "$tmp" "$BIN"; rm -f "$tmp"
  ok "installed prebuilt binary (${asset})"
  return 0
}

# --- ensure a Go toolchain (>= 1.21) ---------------------------------------
GO_BIN="go"
have_recent_go() {
  command -v go >/dev/null 2>&1 || return 1
  local v; v="$(go env GOVERSION 2>/dev/null || go version | awk '{print $3}')"
  # v looks like go1.24.5 → compare minor
  local minor; minor="$(printf '%s' "$v" | sed -n 's/^go1\.\([0-9]*\).*/\1/p')"
  [ -n "$minor" ] && [ "$minor" -ge 21 ] 2>/dev/null
}

install_go() {
  if have_recent_go; then ok "using system Go: $(go version | awk '{print $3}')"; GO_BIN="go"; return; fi
  info "installing a Go toolchain (need >= 1.21)"

  local gv=""
  gv="$(curl -fsSL "https://go.dev/VERSION?m=text" 2>/dev/null | head -1 || true)"
  [ -n "$gv" ] || gv="$GO_FALLBACK"

  local tarball="${gv}.${OS}-${GOARCH}.tar.gz"
  local hosts=("https://go.dev/dl" "https://golang.google.cn/dl" "https://dl.google.com/go")
  local dest="${TMPDIR_BUILD}/${tarball}"
  local got=""
  for h in "${hosts[@]}"; do
    info "  downloading ${h}/${tarball}"
    if curl -fsSL --retry 3 -o "$dest" "${h}/${tarball}" 2>/dev/null && [ -s "$dest" ]; then got="$h"; break; fi
  done
  # if the queried version wasn't downloadable, retry with the pinned fallback
  if [ -z "$got" ] && [ "$gv" != "$GO_FALLBACK" ]; then
    gv="$GO_FALLBACK"; tarball="${gv}.${OS}-${GOARCH}.tar.gz"; dest="${TMPDIR_BUILD}/${tarball}"
    for h in "${hosts[@]}"; do
      info "  downloading ${h}/${tarball}"
      if curl -fsSL --retry 3 -o "$dest" "${h}/${tarball}" 2>/dev/null && [ -s "$dest" ]; then got="$h"; break; fi
    done
  fi
  [ -n "$got" ] || die "could not download a Go toolchain. Install Go >= 1.21 manually (https://go.dev/dl/) and re-run."

  $SUDO rm -rf /usr/local/go
  $SUDO tar -C /usr/local -xzf "$dest"
  GO_BIN="/usr/local/go/bin/go"
  export PATH="/usr/local/go/bin:${PATH}"
  ok "installed $("$GO_BIN" version | awk '{print $3}') to /usr/local/go"
}

# --- get the source tree ----------------------------------------------------
get_source() {
  SRC_DIR="${TMPDIR_BUILD}/src"
  mkdir -p "$SRC_DIR"
  # If we're already inside a checkout (running ./scripts/install.sh), build that.
  local here; here="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")/.." 2>/dev/null && pwd || true)"
  if [ -n "$here" ] && [ -f "${here}/go.mod" ] && [ -f "${here}/main.go" ]; then
    SRC_DIR="$here"; ok "building from local checkout: $SRC_DIR"; return
  fi
  info "downloading source (${REPO}@${BRANCH})"
  local tgz="${TMPDIR_BUILD}/src.tar.gz"
  if curl -fsSL --retry 3 -o "$tgz" "https://github.com/${REPO}/archive/refs/heads/${BRANCH}.tar.gz" 2>/dev/null && [ -s "$tgz" ]; then
    tar -C "$SRC_DIR" -xzf "$tgz" --strip-components=1
  elif command -v git >/dev/null 2>&1; then
    git clone --depth 1 -b "$BRANCH" "https://github.com/${REPO}.git" "$SRC_DIR"
  else
    die "could not fetch source from ${REPO}@${BRANCH}"
  fi
  ok "source ready"
}

# --- build ------------------------------------------------------------------
build_from_source() {
  install_go
  get_source
  info "building cando1 (all transports & features)…"
  local ver commit date ldflags
  ver="$(cd "$SRC_DIR" && git describe --tags --always --dirty 2>/dev/null || echo "$BRANCH")"
  commit="$(cd "$SRC_DIR" && git rev-parse --short HEAD 2>/dev/null || echo none)"
  date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  ldflags="-s -w -X main.version=${ver} -X main.commit=${commit} -X main.date=${date}"
  local out="${TMPDIR_BUILD}/cando1"
  ( cd "$SRC_DIR" && CGO_ENABLED=0 GOOS="$OS" GOARCH="$GOARCH" GOARM="${GOARM}" \
      "$GO_BIN" build -trimpath -ldflags "$ldflags" -o "$out" . )
  $SUDO install -m 0755 "$out" "$BIN"
  ok "built and installed cando1"
}

# --- BBR + network tuning (same as scripts/tune-bbr.sh) ---------------------
apply_bbr() {
  [ "$DO_BBR" = "1" ] || { warn "skipping BBR tuning (CANDO1_BBR=0)"; return; }
  [ "$OS" = "linux" ] || { warn "BBR tuning is Linux-only; skipping"; return; }
  info "enabling BBR congestion control + tuned buffers"
  $SUDO tee /etc/sysctl.d/99-cando1.conf >/dev/null <<'EOF'
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

# UDP buffers (matter for the KCP transport)
net.ipv4.udp_rmem_min = 8192
net.ipv4.udp_wmem_min = 8192
EOF
  $SUDO modprobe tcp_bbr 2>/dev/null || true
  $SUDO sysctl --system >/dev/null 2>&1 || true
  local cc; cc="$(sysctl -n net.ipv4.tcp_congestion_control 2>/dev/null || echo '?')"
  if [ "$cc" = "bbr" ]; then ok "BBR active"; else warn "congestion control is '$cc' (kernel may be < 4.9 — consider upgrading)"; fi
}

# --- optional systemd service ----------------------------------------------
install_service() {
  [ "$OS" = "linux" ] || return 0
  command -v systemctl >/dev/null 2>&1 || return 0
  local want="$DO_SERVICE"
  if [ "$want" = "ask" ]; then
    if [ -t 0 ]; then
      printf "  Install a systemd service for auto-start on boot? [y/N] "
      read -r ans || ans=""
      case "$ans" in y|Y|yes) want=1 ;; *) want=0 ;; esac
    else
      want=0
    fi
  fi
  [ "$want" = "1" ] || { info "systemd service not installed (run 'cando1' manually or re-run with CANDO1_SERVICE=1)"; return 0; }

  $SUDO mkdir -p /etc/cando1
  $SUDO tee /etc/systemd/system/cando1.service >/dev/null <<EOF
[Unit]
Description=cando1 anti-DPI tunnel
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${BIN} -c /etc/cando1/cando1.toml
Restart=always
RestartSec=3
LimitNOFILE=1048576
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
EOF
  $SUDO systemctl daemon-reload
  ok "systemd unit installed: cando1.service"
  info "  put your config at /etc/cando1/cando1.toml, then:"
  info "    sudo systemctl enable --now cando1"
}

# --- finish -----------------------------------------------------------------
summary_and_launch() {
  echo
  info "cando1 installed:"
  "$BIN" version 2>/dev/null || true
  echo
  echo "  Next steps:"
  echo "    cando1                 # interactive setup wizard (generates configs)"
  echo "    cando1 -c file.toml    # run a config"
  echo "    cando1 gen-token       # print a fresh token"
  echo
  echo "  Anti-filtering tip: on a hostile network, front the foreign server"
  echo "  behind Cloudflare (ws/wss) or use a real domain + Let's Encrypt cert."
  echo "  A self-signed cert on a bare IP is the usual reason an IP gets burned."
  echo
  echo "  Channel: https://t.me/cando1tunnel"
  echo
  # Launch the wizard only when attached to a real terminal.
  if [ -t 0 ] && [ -t 1 ]; then
    cleanup   # free the build tempdir before we hand off the process
    exec "$BIN"
  fi
}

main() {
  info "cando1 installer"
  ensure_root
  detect_platform
  TMPDIR_BUILD="$(mktemp -d)"
  install_prereqs
  case "$METHOD" in
    release)
      fetch_release_binary || die "no prebuilt binary for ${OS}/${ARCH} in the ${RELEASE} release (CANDO1_METHOD=release). Remove CANDO1_METHOD to allow a source build, or pick a supported arch."
      ;;
    source)
      info "CANDO1_METHOD=source — building from source"
      build_from_source
      ;;
    auto|*)
      if ! fetch_release_binary; then
        build_from_source
      fi
      ;;
  esac
  apply_bbr
  install_service
  summary_and_launch
}

main "$@"
