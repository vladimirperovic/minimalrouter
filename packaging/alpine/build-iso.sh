#!/bin/sh
# Minimal Router OS — Automated ISO Appliance Builder Script
# Builds an all-in-one bootable ISO with routerd, router-applyd, web dashboard, and setup wizard pre-installed.

set -e

echo "=== Minimal Router OS ISO Builder ==="

OUTPUT_DIR="./build/iso"
OVERLAY_DIR="./build/overlay"

mkdir -p "$OUTPUT_DIR"
rm -rf "$OVERLAY_DIR"
mkdir -p "$OVERLAY_DIR/usr/bin" "$OVERLAY_DIR/usr/sbin" \
    "$OVERLAY_DIR/etc/init.d" "$OVERLAY_DIR/etc/sysctl.d" \
    "$OVERLAY_DIR/etc/modules-load.d" \
    "$OVERLAY_DIR/etc/local.d" "$OVERLAY_DIR/var/lib/minimalrouter" \
    "$OVERLAY_DIR/var/lib/minimalrouter-applyd" \
    "$OVERLAY_DIR/usr/share/minimalrouter/web"

echo "[1/4] Compiling Go binaries for Linux (x86_64)..."
make build-linux
cp bin/routerd-linux-amd64 "$OVERLAY_DIR/usr/bin/routerd"
cp bin/router-applyd-linux-amd64 "$OVERLAY_DIR/usr/sbin/router-applyd"

echo "[2/4] Installing OpenRC init scripts..."
cp packaging/alpine/routerd.initd "$OVERLAY_DIR/etc/init.d/routerd"
cp packaging/alpine/router-applyd.initd "$OVERLAY_DIR/etc/init.d/router-applyd"
cp packaging/alpine/pppoe-wan.initd "$OVERLAY_DIR/etc/init.d/pppoe-wan"
cp packaging/alpine/99-minimalrouter.conf "$OVERLAY_DIR/etc/sysctl.d/99-minimalrouter.conf"
cp packaging/alpine/minimalrouter.modules "$OVERLAY_DIR/etc/modules-load.d/minimalrouter.conf"
chmod +x "$OVERLAY_DIR/etc/init.d/routerd" "$OVERLAY_DIR/etc/init.d/router-applyd" "$OVERLAY_DIR/etc/init.d/pppoe-wan"

echo "[3/4] Packaging static Web Dashboard assets..."
if [ ! -f "web/dist/index.html" ]; then
    echo "ERROR: web/dist/index.html is missing. Run 'cd web && pnpm build' first." >&2
    exit 1
fi
cp -r web/dist/. "$OVERLAY_DIR/usr/share/minimalrouter/web/"

echo "[4/4] Generating ISO manifest and boot scripts..."
cat << 'EOF' > "$OVERLAY_DIR/etc/local.d/minimalrouter.start"
#!/bin/sh
# Auto-start Minimal Router OS control plane on boot
rc-service router-applyd start
rc-service routerd start
EOF
chmod +x "$OVERLAY_DIR/etc/local.d/minimalrouter.start"

echo "=== Appliance Overlay Ready at $OVERLAY_DIR ==="
echo "No bootable ISO was produced."
echo "A reviewed Alpine mkimage profile, package repository, signing, and boot test remain release gates."
