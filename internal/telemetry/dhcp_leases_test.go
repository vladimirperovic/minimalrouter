package telemetry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDHCPLeases(t *testing.T) {
	data := []byte(strings.Join([]string{
		"1760000000 aa:bb:cc:dd:ee:ff 192.168.1.42 phone 01:aa:bb",
		"0 00:11:22:33:44:55 192.168.1.50 * *",
		"invalid 00:11:22:33:44:66 192.168.1.51 ignored *",
		"1760000000 invalid 192.168.1.52 ignored *",
		"1760000000 00:11:22:33:44:77 2001:db8::1 ignored *",
	}, "\n"))

	leases, err := parseDHCPLeases(data)
	if err != nil {
		t.Fatalf("parse leases: %v", err)
	}
	if len(leases) != 2 {
		t.Fatalf("expected 2 valid leases, got %d", len(leases))
	}
	if leases[0].Hostname != "phone" || leases[0].IPAddress != "192.168.1.42" {
		t.Fatalf("unexpected first lease: %#v", leases[0])
	}
	if leases[1].Hostname != "" || leases[1].MAC != "00:11:22:33:44:55" {
		t.Fatalf("unexpected anonymous lease: %#v", leases[1])
	}
}

func TestParseDHCPLeasesRejectsOversizedInput(t *testing.T) {
	if _, err := parseDHCPLeases(make([]byte, maxDHCPLeaseBytes+1)); err == nil {
		t.Fatal("expected oversized lease data to be rejected")
	}
}

func TestActiveDHCPLeasesAtRemovesExpiredEntries(t *testing.T) {
	leases := []DHCPLease{
		{ExpiresAt: 99, MAC: "00:11:22:33:44:55"},
		{ExpiresAt: 101, MAC: "00:11:22:33:44:66"},
		{ExpiresAt: 0, MAC: "00:11:22:33:44:77"},
	}
	active := activeDHCPLeasesAt(leases, 100)
	if len(active) != 2 || active[0].MAC != "00:11:22:33:44:66" || active[1].MAC != "00:11:22:33:44:77" {
		t.Fatalf("unexpected active leases: %#v", active)
	}
}

func TestReadDHCPLeasesTracksFileReplacementAndBounds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "leases")
	if len(readDHCPLeases(path)) != 0 {
		t.Fatal("missing file produced leases")
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(name, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(path, "1 02:00:00:00:00:20 192.168.1.20 expired *\n0 02:00:00:00:00:21 192.168.1.21 permanent *\n")
	if got := readDHCPLeases(path); len(got) != 1 || got[0].Hostname != "permanent" {
		t.Fatal("expired/permanent lease semantics changed")
	}
	replacement := filepath.Join(dir, "new")
	write(replacement, "0 02:00:00:00:00:22 192.168.1.22 replaced *\n")
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	if got := readDHCPLeases(path); len(got) != 1 || got[0].Hostname != "replaced" {
		t.Fatal("replacement left stale labels")
	}
	write(path, strings.Repeat("x", maxDHCPLeaseBytes+1))
	if len(readDHCPLeases(path)) != 0 {
		t.Fatal("oversized lease file accepted")
	}
}
