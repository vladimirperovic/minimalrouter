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
LIVE_LAN_FILE=/run/minimalrouter-live-lan
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
    printf 'First-run setup starts automatically.\n\n'
    cat <<'EOF'
You should already have:
  - a Proxmox QEMU/KVM VM with two network adapters and an 8 GiB+ disk;
  - WAN connected toward the modem/ONT and LAN connected to your private bridge;
  - your PPPoE username/password if your ISP uses PPPoE (you may skip it);
  - your old router available until this new installation is tested.

How to answer:
  - when a suggested choice is correct, press Enter to accept it;
  - if WAN/LAN detection is uncertain, the installer explains what to choose;
  - passwords are required and are never shown while you type;
  - on a normal one-disk Proxmox VM, the attached VM disk installs automatically;
  - unusual hardware or multi-disk systems keep an explicit safety confirmation.

This ISO already includes Alpine Linux and everything MinimalRouter needs.
EOF
    printf '\nStarting setup...\n\n'
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
    MINIMALROUTER_WELCOME_SHOWN=1 "$SETUP_BIN" collect --output "$PROVISION_FILE" --data-dir /var/lib/minimalrouter
    if [ "${MINIMALROUTER_ISO_INSTALL:-0}" = "1" ] && [ -s "$PROVISION_FILE" ]; then
        live_lan="$(sed -n 's/.*"lan_interface":"\([^"]*\)".*/\1/p' "$PROVISION_FILE" | head -1)"
        [ -n "$live_lan" ] || { echo "ERROR: selected LAN interface could not be recovered for ISO SSH" >&2; exit 1; }
        printf '%s\n' "$live_lan" > "$LIVE_LAN_FILE"
        chmod 0600 "$LIVE_LAN_FILE"
    fi
fi

# Run the existing hardened installer unchanged. This keeps one source of truth
# for package policy, kernel checks, A/B baseline creation and service layout.
sh "$CORE_INSTALLER" "$@"

# If this was a first interactive install, persist exactly the reviewed console
# choices for the first disk boot. On the live ISO the production router stack
# is never started: the installed system reconciles the configuration natively
# on its own kernel and modules (see router-setup apply --offline).
if [ "$INTERACTIVE_SETUP" -eq 1 ] && [ -f "$PROVISION_FILE" ]; then
    echo
    echo "Writing first-run configuration for the installed system..."
    SETUP_RC=0
    "$SETUP_BIN" apply --offline --input "$PROVISION_FILE" --data-dir /var/lib/minimalrouter || SETUP_RC=$?

    # router-setup runs as root because it talks to the privileged helper. Give
    # canonical SQLite state back to routerd even on a failed setup/rollback.
    chown -R routerd:routerd /var/lib/minimalrouter
    chmod 0700 /var/lib/minimalrouter
    find /var/lib/minimalrouter -maxdepth 1 -type f -name 'minimalrouter.db*' -exec chmod 0600 {} \;

    if [ "$SETUP_RC" -ne 0 ]; then
        echo "ERROR: first-run configuration could not be persisted." >&2
        exit "$SETUP_RC"
    fi

    rm -f "$PROVISION_FILE"
    echo
    cat <<'ART'
           _       _                 _                 _
 _ __ ___ (_)_ __ (_)_ __ ___   __ _| |_ __ ___  _   _| |_ ___ _ __
| '_ ` _ \| | '_ \| | '_ ` _ \ / _` | | '__/ _ \| | | | __/ _ \ '__|
| | | | | | | | | | | | | | | | (_| | | | | (_) | |_| | ||  __/ |
|_| |_| |_|_|_| |_|_|_| |_| |_|\__,_|_|_|  \___/ \__,_|\__\___|_|
ART
    printf '\033[32m●\033[0m Minimal Router OS v%s configuration saved. The first disk boot will finalize the router.\n' "$MR_VERSION"
    printf '\033[32m●\033[0m Dashboard after boot: https://192.168.1.1:8443\n'
fi
