#!/bin/sh
# Exercise the kernel-module pruning guard against synthetic module trees.
set -eu
SOURCE=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
WORK=$(mktemp -d "${TMPDIR:-/tmp}/minimalrouter-prune-test.XXXXXX")
trap 'rm -rf "$WORK"' EXIT HUP INT TERM
export WORK
mkdir -p "$WORK/bin"
# depmod records that the index was rebuilt; modprobe resolves a module only
# while its file is still in the tree (modprobe -d ROOT -S REL --show-depends M).
cat > "$WORK/bin/depmod" <<'MOCK'
#!/bin/sh
echo "$@" > "$WORK/depmod.called"
MOCK
cat > "$WORK/bin/modprobe" <<'MOCK'
#!/bin/sh
find "$2/lib/modules/$4" -name "$6.ko*" | grep -q .
MOCK
chmod 0755 "$WORK/bin/depmod" "$WORK/bin/modprobe"
PATH="$WORK/bin:$PATH"
export PATH

RELEASE=6.18.0-test
make_tree() {
    tree="$WORK/$1/lib/modules/$RELEASE"
    mkdir -p "$tree/kernel/sound/core" "$tree/kernel/drivers/media" "$tree/kernel/drivers/net/ppp"
    for module in sound/core/snd drivers/media/videodev drivers/net/ppp/ppp_generic drivers/net/ppp/pppoe; do
        : > "$tree/kernel/$module.ko.gz"
    done
    cat > "$tree/modules.dep" <<'DEP'
kernel/sound/core/snd.ko.gz:
kernel/drivers/media/videodev.ko.gz:
kernel/drivers/net/ppp/ppp_generic.ko.gz:
kernel/drivers/net/ppp/pppoe.ko.gz: kernel/drivers/net/ppp/ppp_generic.ko.gz
DEP
}
printf 'pppoe\nppp_generic # runtime\n' > "$WORK/required"

# Unused subsystems go, required modules stay and the index is rebuilt.
make_tree ok
sh "$SOURCE/prune-kernel-modules.sh" "$WORK/ok" "$WORK/required" > "$WORK/ok.log"
[ ! -e "$WORK/ok/lib/modules/$RELEASE/kernel/sound" ]
[ ! -e "$WORK/ok/lib/modules/$RELEASE/kernel/drivers/media" ]
[ -e "$WORK/ok/lib/modules/$RELEASE/kernel/drivers/net/ppp/pppoe.ko.gz" ]
grep -q "$RELEASE" "$WORK/depmod.called"
grep -q 'removed 2 files' "$WORK/ok.log"

# A module that needs a removed one could never load, so it is removed too.
make_tree dependent
: > "$WORK/dependent/lib/modules/$RELEASE/kernel/drivers/net/ppp/pppox.ko.gz"
echo "kernel/drivers/net/ppp/pppox.ko.gz: kernel/sound/core/snd.ko.gz" >> "$WORK/dependent/lib/modules/$RELEASE/modules.dep"
sh "$SOURCE/prune-kernel-modules.sh" "$WORK/dependent" "$WORK/required" > "$WORK/dependent.log"
[ ! -e "$WORK/dependent/lib/modules/$RELEASE/kernel/drivers/net/ppp/pppox.ko.gz" ]
[ -e "$WORK/dependent/lib/modules/$RELEASE/kernel/drivers/net/ppp/pppoe.ko.gz" ]
grep -q 'Removing kernel/drivers/net/ppp/pppox.ko.gz: it depends on a removed module' "$WORK/dependent.log"
grep -q 'removed 3 files' "$WORK/dependent.log"

# ...but never when the appliance requires it.
make_tree required-dependent
: > "$WORK/required-dependent/lib/modules/$RELEASE/kernel/drivers/net/ppp/pppox.ko.gz"
echo "kernel/drivers/net/ppp/pppox.ko.gz: kernel/sound/core/snd.ko.gz" >> "$WORK/required-dependent/lib/modules/$RELEASE/modules.dep"
printf 'pppox\n' > "$WORK/required-pppox"
if sh "$SOURCE/prune-kernel-modules.sh" "$WORK/required-dependent" "$WORK/required" "$WORK/required-pppox" 2> "$WORK/required-dependent.log"; then
    echo "FAIL: a required module that depends on a removed one was dropped silently" >&2
    exit 1
fi
grep -q 'required kernel module pppox is unavailable after pruning' "$WORK/required-dependent.log"

# A required module that pruning would remove fails the build.
make_tree gone
printf 'snd\n' > "$WORK/required-sound"
if sh "$SOURCE/prune-kernel-modules.sh" "$WORK/gone" "$WORK/required" "$WORK/required-sound" 2> "$WORK/gone.log"; then
    echo "FAIL: a missing required module was accepted" >&2
    exit 1
fi
grep -q 'required kernel module snd is unavailable after pruning' "$WORK/gone.log"

echo "prune-kernel-modules: all checks passed"
