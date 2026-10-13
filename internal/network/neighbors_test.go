package network

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadARPTableKeepsResolvedEntriesOnTheLAN(t *testing.T) {
	table := "IP address       HW type     Flags       HW address            Mask     Device\n" +
		"192.168.1.50     0x1         0x2         00:15:5d:0a:0a:01     *        eth1\n" +
		"192.168.1.60     0x1         0x0         00:00:00:00:00:00     *        eth1\n" +
		"10.250.0.1       0x1         0x2         00:15:5d:30:01:02     *        eth0\n" +
		"192.168.1.70     0x1         0x6         00-15-5D-0A-0A-07     *        eth1\n" +
		"garbage line\n"
	path := filepath.Join(t.TempDir(), "arp")
	if err := os.WriteFile(path, []byte(table), 0o600); err != nil {
		t.Fatal(err)
	}
	got := ReadARPTable(path, "eth1")
	if len(got) != 2 || got[0] != (Neighbor{IP: "192.168.1.50", MAC: "00:15:5d:0a:0a:01", Interface: "eth1"}) || got[1].MAC != "00:15:5d:0a:0a:07" {
		t.Fatalf("unexpected neighbours: %+v", got)
	}
	if all := ReadARPTable(path, ""); len(all) != 3 {
		t.Fatalf("empty interface must read every resolved entry: %+v", all)
	}
	if missing := ReadARPTable(filepath.Join(t.TempDir(), "absent"), "eth1"); missing != nil {
		t.Fatalf("unreadable table must yield no observations: %+v", missing)
	}
}
