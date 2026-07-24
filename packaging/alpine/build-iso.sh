#!/bin/sh
# Minimal Router OS — Automated ISO Appliance Builder Script
# Builds an all-in-one bootable ISO with routerd, router-applyd, web dashboard, and setup wizard pre-installed.

set -e

echo "=== Minimal Router OS ISO Builder ==="

OUTPUT_DIR="./build/iso"
OVERLAY_DIR="./build/overlay"

mkdir -p "$OUTPUT_DIR" "$OVERLAY_DIR/usr/bin" "$OVERLAY_DIR/usr/sbin" "$OVERLAY_DIR/etc/init.d" "$OVERLAY_DIR/var/lib/minimalrouter"

echo "[1/4] Compiling Go binaries for Linux (x86_64)..."
GOOS=linux GOARCH=amd64 go build -o "$OVERLAY_DIR/usr/bin/routerd" ./cmd/routerd
GOOS=linux GOARCH=amd64 go build -o "$OVERLAY_DIR/usr/sbin/router-applyd" ./cmd/router-applyd

echo "[2/4] Installing OpenRC init scripts..."
cp packaging/alpine/routerd.initd "$OVERLAY_DIR/etc/init.d/routerd"
cp packaging/alpine/router-applyd.initd "$OVERLAY_DIR/etc/init.d/router-applyd"
chmod +x "$OVERLAY_DIR/etc/init.d/routerd" "$OVERLAY_DIR/etc/init.d/router-applyd"

echo "[3/4] Packaging static Web Dashboard assets..."
mkdir -p "$OVERLAY_DIR/var/lib/minimalrouter/web"
if [ -d "web/dist" ]; then
    cp -r web/dist/* "$OVERLAY_DIR/var/lib/minimalrouter/web/"
fi

echo "[4/4] Generating ISO manifest and boot scripts..."
cat << 'EOF' > "$OVERLAY_DIR/etc/local.d/minimalrouter.start"
#!/bin/sh
# Auto-start Minimal Router OS control plane on boot
rc-service router-applyd start || true
rc-service routerd start || true
EOF
chmod +x "$OVERLAY_DIR/etc/local.d/minimalrouter.start"

echo "=== Appliance Overlay Ready at $OVERLAY_DIR ==="
echo "To produce bootable .iso image on Alpine build host, run: mkimage.sh --profile minimalrouter --overlay $OVERLAY_DIR"
