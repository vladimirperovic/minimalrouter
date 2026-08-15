package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

const (
	routerdUpdateHelperPath = "/usr/libexec/minimalrouter/routerd-update"
	routerdUpdateDoasPath   = "/etc/doas.d/51-minimalrouter-update.conf"
)

const routerdUpdateHelper = `#!/bin/sh
set -eu

UPDATE_ROOT=/var/lib/minimalrouter-update
INBOX=/var/lib/minimalrouter/update-inbox

case "$(uname -m)" in
  x86_64) BIN_ARCH=amd64 ;;
  aarch64) BIN_ARCH=arm64 ;;
  *) echo "ERROR: unsupported update architecture" >&2; exit 1 ;;
esac

case "${1:-}" in
  stage)
    [ "$#" -eq 1 ] || { echo "ERROR: stage accepts no arguments" >&2; exit 2; }
    exec /usr/sbin/router-update stage \
      --dir "$INBOX/release/minimalrouter-linux-$BIN_ARCH" \
      --manifest "$INBOX/manifest.json"
    ;;
  activate)
    [ "$#" -eq 1 ] || { echo "ERROR: activate accepts no arguments" >&2; exit 2; }
    pending="$(/usr/sbin/router-update status | sed -n 's/.*"pending"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
    [ -n "$pending" ] || { echo "ERROR: no verified release is pending activation" >&2; exit 1; }
    exec /usr/sbin/router-update activate --version "$pending" --confirm ACTIVATE-UPDATE
    ;;
  *)
    echo "Usage: routerd-update {stage|activate}" >&2
    exit 2
    ;;
esac
`

const routerdUpdateDoas = `permit nopass routerd as root cmd /usr/libexec/minimalrouter/routerd-update args stage
permit nopass routerd as root cmd /usr/libexec/minimalrouter/routerd-update args activate
`

func atomicRootFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".minimalrouter-update-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chown(tempPath, 0, 0); err != nil {
		return err
	}
	if err := os.Chmod(tempPath, mode); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	dirHandle, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer dirHandle.Close()
	return dirHandle.Sync()
}

func installRouterdUpdatePrivilege() error {
	if err := atomicRootFile(routerdUpdateHelperPath, []byte(routerdUpdateHelper), 0o755); err != nil {
		return fmt.Errorf("install update helper: %w", err)
	}
	if err := atomicRootFile(routerdUpdateDoasPath, []byte(routerdUpdateDoas), 0o400); err != nil {
		return fmt.Errorf("install update doas rule: %w", err)
	}
	return nil
}

func init() {
	// Test binaries must never mutate the host even when a test happens to run
	// as root. Production router-applyd runs as root on the appliance.
	if os.Geteuid() != 0 || strings.HasSuffix(os.Args[0], ".test") {
		return
	}
	if err := installRouterdUpdatePrivilege(); err != nil {
		// Updating is optional. A failure here must not prevent the privileged
		// helper from bringing up the router; the API will report update
		// privilege as unavailable and normal routing/recovery remains intact.
		log.Printf("signed web update privilege unavailable: %v", err)
	}
}
