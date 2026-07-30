//go:build linux

package telemetry

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSysfsValue(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestInterfaceInventoryAtReportsStableOperatorDetails(t *testing.T) {
	root := t.TempDir()
	writeSysfsValue(t, filepath.Join(root, "eth1", "type"), "1")
	writeSysfsValue(t, filepath.Join(root, "eth1", "address"), "02:00:00:00:00:11")
	writeSysfsValue(t, filepath.Join(root, "eth1", "operstate"), "up")
	writeSysfsValue(t, filepath.Join(root, "eth1", "carrier"), "1")
	writeSysfsValue(t, filepath.Join(root, "eth1", "speed"), "2500")

	writeSysfsValue(t, filepath.Join(root, "lo", "type"), "772")
	writeSysfsValue(t, filepath.Join(root, "lo", "operstate"), "unknown")

	writeSysfsValue(t, filepath.Join(root, "wlan0", "type"), "1")
	writeSysfsValue(t, filepath.Join(root, "wlan0", "address"), "02:00:00:00:00:22")
	if err := os.MkdirAll(filepath.Join(root, "wlan0", "wireless"), 0755); err != nil {
		t.Fatal(err)
	}

	got := interfaceInventoryAt(root)
	if len(got) != 3 {
		t.Fatalf("got %d interfaces, want 3: %#v", len(got), got)
	}
	if got[0].Name != "eth1" || got[0].Kind != "ethernet" || !got[0].Carrier || got[0].SpeedMbps != 2500 {
		t.Fatalf("unexpected ethernet inventory: %#v", got[0])
	}
	if got[1].Name != "lo" || !got[1].Loopback || got[1].Kind != "loopback" {
		t.Fatalf("unexpected loopback inventory: %#v", got[1])
	}
	if got[2].Name != "wlan0" || got[2].Kind != "wifi" {
		t.Fatalf("unexpected Wi-Fi inventory: %#v", got[2])
	}
}
