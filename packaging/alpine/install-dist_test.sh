#!/bin/sh
set -eu

echo "=== Testing install-dist.sh ==="

# Mock environment
export PATH="$PWD/mock-bin:$PATH"
mkdir -p mock-bin
cat > mock-bin/apk << 'EOF'
#!/bin/sh
if [ "$1" = "update" ]; then
    echo "MOCK apk update"
elif [ "$1" = "add" ]; then
    shift
    echo "MOCK apk add $@"
elif [ "$1" = "info" ] && [ "$2" = "-e" ]; then
    pkg="$3"
    # For testing, we pretend 'missing-pkg' is not installed
    if [ "$pkg" = "missing-pkg" ]; then
        exit 1
    fi
    exit 0
else
    echo "MOCK apk unknown: $@"
    exit 1
fi
EOF
chmod +x mock-bin/apk

# Mock id and uname
cat > mock-bin/id << 'EOF'
#!/bin/sh
echo "0"
EOF
chmod +x mock-bin/id

cat > mock-bin/uname << 'EOF'
#!/bin/sh
echo "x86_64"
EOF
chmod +x mock-bin/uname

# Mock install
cat > mock-bin/install << 'EOF'
#!/bin/sh
echo "MOCK install $@"
EOF
chmod +x mock-bin/install

# Create dummy Alpine release file
mkdir -p mock-etc/apk
touch mock-etc/alpine-release
touch mock-etc/apk/repositories

# We'll run the installer via a wrapper that sets /etc to mock-etc
cat > run-installer.sh << 'EOF'
#!/bin/sh
export PATH="$PWD/mock-bin:$PATH"
# Patch script to use mock-etc instead of /etc just for the first check
sed 's|/etc|mock-etc|g' packaging/alpine/install-dist.sh > /tmp/test-install.sh
chmod +x /tmp/test-install.sh
# Remove cd $SCRIPT_DIR so it uses our local mock-etc
sed -i.bak '/cd "$SCRIPT_DIR"/d' /tmp/test-install.sh
# Also stub the file checks because we don't have the build artifacts
sed -i.bak 's/\[ -f "$required" \]/true/g' /tmp/test-install.sh
# This test intentionally exercises only dependency selection. Stop before the
# real kernel-module preflight and all root-runtime mutations.
sed -i.bak '/# Fail before replacing appliance runtime files/,$d' /tmp/test-install.sh

/tmp/test-install.sh "$@"
EOF
chmod +x run-installer.sh

# Test A: Normal mode
echo "--- Test A: Normal mode ---"
output=$(./run-installer.sh 2>&1)
if echo "$output" | grep -q "MOCK apk update" && echo "$output" | grep -q "MOCK apk add"; then
    echo "PASS: Normal mode calls apk update/add"
else
    echo "FAIL: Normal mode did not call apk update/add"
    echo "$output"
    exit 1
fi

# Test B: Offline mode with all dependencies
echo "--- Test B: Offline mode with all dependencies ---"
output=$(./run-installer.sh --offline 2>&1)
if echo "$output" | grep -q "MOCK apk" || ! echo "$output" | grep -q "All required dependencies already installed"; then
    echo "FAIL: Offline mode called apk or failed to recognize installed dependencies"
    echo "$output"
    exit 1
else
    echo "PASS: Offline mode skipped apk and verified dependencies"
fi

# Test C: Offline mode with missing dependency
echo "--- Test C: Offline mode with missing dependency ---"
# We inject a fake missing dependency into the required list
sed -i.bak 's/REQUIRED_PACKAGES="/REQUIRED_PACKAGES="missing-pkg /g' /tmp/test-install.sh
set +e
output=$(/tmp/test-install.sh --offline 2>&1)
rc=$?
set -e
if [ $rc -ne 0 ] && echo "$output" | grep -q "ERROR: The following required packages are missing" && echo "$output" | grep -q "missing-pkg"; then
    echo "PASS: Offline mode failed correctly with missing dependency"
else
    echo "FAIL: Offline mode did not fail correctly on missing dependency"
    echo "RC: $rc"
    echo "$output"
    exit 1
fi

# Test D: Unknown CLI argument
echo "--- Test D: Unknown argument ---"
set +e
output=$(./run-installer.sh --unknown 2>&1)
rc=$?
set -e
if [ $rc -ne 0 ] && echo "$output" | grep -q "Usage:"; then
    echo "PASS: Unknown argument rejected correctly"
else
    echo "FAIL: Unknown argument not rejected"
    echo "RC: $rc"
    echo "$output"
    exit 1
fi

echo "=== All install-dist.sh tests passed ==="
rm -rf mock-bin mock-etc run-installer.sh /tmp/test-install.sh
