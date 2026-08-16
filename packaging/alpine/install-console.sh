#!/bin/sh
# Minimal Router OS — interactive first-run wrapper around install-core.sh.
set -eu

if [ "$(id -u)" -ne 0 ]; then
    echo "ERROR: install.sh must run as root" >&2
    exit 1
fi
if [ ! -f /etc/alpine-release ] || ! command -v apk >/dev/null 2>&1; then
    echo "ERROR: this distribution installer supports Alpine Linux only" >&2
    exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

case "$(uname -m)" in
    x86_64) BIN_ARCH=amd64 ;;
    aarch64) BIN_ARCH=arm64 ;;
    *) echo "ERROR: unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

MR_VERSION="dev"
if [ -r "$SCRIPT_DIR/VERSION" ]; then
    MR_VERSION="$(tr -d '\r\n' < "$SCRIPT_DIR/VERSION")"
    [ -n "$MR_VERSION" ] || MR_VERSION="dev"
fi

SETUP_BIN="$SCRIPT_DIR/bin/router-setup-${BIN_ARCH}"
CORE_INSTALLER="$SCRIPT_DIR/install-core.sh"
PROVISION_FILE=/run/minimalrouter-console-setup.json
INTERACTIVE_SETUP=0

cleanup() {
    rm -f "$PROVISION_FILE"
}
trap cleanup EXIT
trap 'cleanup; exit 129' HUP
trap 'cleanup; exit 130' INT
trap 'cleanup; exit 143' TERM

show_welcome() {
    command -v clear >/dev/null 2>&1 && clear || true
    cat <<'ASCII'
 __  __ _       _                 _ ____             _
|  \/  (_)_ __ (_)_ __ ___   __ _| |  _ \ ___  _   _| |_ ___ _ __
| |\/| | | '_ \| | '_ ` _ \ / _` | | |_) / _ \| | | | __/ _ \ '__|
| |  | | | | | | | | | | | | (_| | |  _ < (_) | |_| | ||  __/ |
|_|  |_|_|_| |_|_|_| |_| |_|\__,_|_|_| \_\___/ \__,_|\__\___|_|
ASCII
    printf '\nMinimal Router OS v%s\n' "$MR_VERSION"
    printf 'Welcome to Minimal Router OS.\n\n'
    cat <<'EOF'
Before you continue
-------------------
If you are installing on Proxmox VE, you should already have:
  - a QEMU/KVM virtual machine (not an LXC container);
  - at least 1 vCPU, 1 GiB RAM and an 8 GiB virtual disk;
  - CPU type "host" recommended for a fixed home/lab node;
  - two network adapters (VirtIO is recommended);
  - one adapter connected to the WAN bridge leading to the ISP modem/ONT;
  - one adapter connected to an isolated LAN bridge for your clients;
  - working Proxmox console access and a rollback/snapshot path.

Network preparation:
  - the ISP modem/ONT must expose PPPoE to the WAN adapter using bridge or
    pass-through mode;
  - have the PPPoE username and password ready;
  - keep your previous router available until this installation is verified;
  - do not connect the new MinimalRouter LAN to the same broadcast domain as
    another active DHCP server during the first installation.

This ISO already contains Alpine Linux, the linux-lts kernel, the required
router packages, MinimalRouter and the Web Dashboard. You do not need to
install Alpine separately.

The installer will test the network adapters for PPPoE, propose WAN/LAN roles,
and ask you to confirm or change them. PPPoE credentials and the dashboard
administrator password are entered locally and are never echoed on screen.
EOF
    printf '\nPress Enter to continue, or Ctrl+C to abort. '
    IFS= read -r _mr_continue
    printf '\n'
}

[ -x "$SETUP_BIN" ] || { echo "ERROR: missing console setup helper: $SETUP_BIN" >&2; exit 1; }
[ -f "$CORE_INSTALLER" ] || { echo "ERROR: missing core installer: $CORE_INSTALLER" >&2; exit 1; }

# Console setup is deliberately opt-in only when stdin is a real local terminal.
# CI, scripted installs and redirected stdin keep the existing non-interactive path.
if [ -t 0 ] && [ "${MINIMALROUTER_SKIP_CONSOLE_SETUP:-0}" != "1" ]; then
    INTERACTIVE_SETUP=1
    show_welcome

    # The full core installer owns dependency policy. Install only the two tools
    # needed before it runs: ip(8) for link state and pppoe-discovery for safe
    # WAN detection. Offline mode never reaches the network.
    if [ "${1:-}" = "--offline" ] || [ "${MINIMALROUTER_OFFLINE:-0}" = "1" ]; then
        for pkg in iproute2 ppp-pppoe; do
            apk info -e "$pkg" >/dev/null 2>&1 || {
                echo "ERROR: offline console setup requires preinstalled package: $pkg" >&2
                exit 1
            }
        done
    else
        MISSING_BOOTSTRAP=""
        for pkg in iproute2 ppp-pppoe; do
            apk info -e "$pkg" >/dev/null 2>&1 || MISSING_BOOTSTRAP="$MISSING_BOOTSTRAP $pkg"
        done
        if [ -n "$MISSING_BOOTSTRAP" ]; then
            echo "Preparing network discovery tools..."
            apk update
            # shellcheck disable=SC2086
            apk add --no-cache $MISSING_BOOTSTRAP
        fi
    fi

    rm -f "$PROVISION_FILE"
    "$SETUP_BIN" collect --output "$PROVISION_FILE" --data-dir /var/lib/minimalrouter
fi

# Run the existing hardened installer unchanged. This keeps one source of truth
# for package policy, kernel checks, A/B baseline creation and service layout.
sh "$CORE_INSTALLER" "$@"

# If this was a first interactive install, apply exactly the reviewed console
# choices through the normal privileged transaction engine. The CLI commits the
# setup only after a real PPP IPv4 session exists when PPPoE credentials were
# supplied; a failed authentication therefore fails setup instead of leaving a
# half-configured router behind.
if [ "$INTERACTIVE_SETUP" -eq 1 ] && [ -f "$PROVISION_FILE" ]; then
    echo
    echo "Applying and verifying first-run network configuration..."
    rc-service router-applyd start

    SETUP_RC=0
    "$SETUP_BIN" apply --input "$PROVISION_FILE" --data-dir /var/lib/minimalrouter || SETUP_RC=$?

    # router-setup runs as root because it talks to the privileged helper. Give
    # canonical SQLite state back to routerd even on a failed setup/rollback.
    chown -R routerd:routerd /var/lib/minimalrouter
    chmod 0700 /var/lib/minimalrouter
    find /var/lib/minimalrouter -maxdepth 1 -type f -name 'minimalrouter.db*' -exec chmod 0600 {} \;

    if [ "$SETUP_RC" -ne 0 ]; then
        echo "ERROR: first-run network verification failed; configuration was not finalized." >&2
        exit "$SETUP_RC"
    fi

    rm -f "$PROVISION_FILE"
    rc-service chronyd start
    rc-service routerd start
    echo
    printf '\033[32m●\033[0m Minimal Router OS v%s is ready. Dashboard: https://192.168.1.1:8443\n' "$MR_VERSION"
fi
