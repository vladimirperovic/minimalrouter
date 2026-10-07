package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/dnsactivity"
)

func TestParseDNSQueryLineAcceptsOnlyQueryRecords(t *testing.T) {
	cases := []struct {
		line, client, name string
		ok                 bool
	}{
		{"Oct  6 12:00:00 dnsmasq[1234]: query[A] Example.COM. from 192.168.1.50\n", "192.168.1.50", "example.com", true},
		{"Oct  6 12:00:00 dnsmasq[1234]: query[AAAA] ss.phncdn.com from fd00::5", "fd00::5", "ss.phncdn.com", true},
		{"Oct  6 12:00:00 dnsmasq[1234]: query[type=65] www.youtube.com from ::ffff:192.168.1.9", "192.168.1.9", "www.youtube.com", true},
		{"Oct  6 12:00:00 dnsmasq[1234]: query[A] _dmarc.example.org from 10.8.0.2", "10.8.0.2", "_dmarc.example.org", true},
		{"Oct  6 12:00:00 dnsmasq[1234]: forwarded example.com to 1.1.1.1", "", "", false},
		{"Oct  6 12:00:00 dnsmasq[1234]: reply example.com is 93.184.216.34", "", "", false},
		{"Oct  6 12:00:00 dnsmasq[1234]: query[PTR] 50.1.168.192.in-addr.arpa from 192.168.1.50", "", "", false},
		{"Oct  6 12:00:00 dnsmasq[1234]: query[SOA] 1.168.192.in-addr.arpa from 192.168.1.50", "", "", false},
		{"Oct  6 12:00:00 dnsmasq[1234]: query[A] example.com from 127.0.0.1", "", "", false},
		{"Oct  6 12:00:00 dnsmasq[1234]: query[A] wpad from 192.168.1.50", "", "", false},
		{"Oct  6 12:00:00 dnsmasq[1234]: query[A] bad name.com from 192.168.1.50", "", "", false},
		{"Oct  6 12:00:00 dnsmasq[1234]: query[A] evil\\032.com from 192.168.1.50", "", "", false},
		{"Oct  6 12:00:00 dnsmasq[1234]: query[A] example.com from not-an-ip", "", "", false},
		{"Oct  6 12:00:00 dnsmasq-dhcp[1234]: DHCPACK(br0) 192.168.1.50 aa:bb:cc:dd:ee:ff phone", "", "", false},
		{"Oct  6 12:00:00 other[1]: query[A] example.com from 192.168.1.50", "", "", false},
		{"query[A] example.com from 192.168.1.50", "", "", false},
		{"Oct  6 12:00:00 dnsmasq[1234]: query[A] " + strings.Repeat("a", 64) + ".com from 192.168.1.50", "", "", false},
	}
	for _, tc := range cases {
		client, name, ok := parseDNSQueryLine(tc.line)
		if ok != tc.ok || client != tc.client || name != tc.name {
			t.Errorf("parseDNSQueryLine(%q) = %q, %q, %v; want %q, %q, %v", tc.line, client, name, ok, tc.client, tc.name, tc.ok)
		}
	}
}

type dnsmasqStub struct {
	tests, restarts int
	failTest        bool
	failRestarts    int
}

func setupDNSActivityFiles(t *testing.T, enabled bool) (string, *dnsmasqStub) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "queries.log")
	dropInPath := filepath.Join(dir, "minimalrouter-dns-activity.conf")
	previousDropIn, previousLog, previousTest, previousRestart := dnsDropInPath, dnsQueryLogPath, testDnsmasqConfig, restartDnsmasq
	dnsDropInPath, dnsQueryLogPath = dropInPath, logPath
	stub := &dnsmasqStub{}
	testDnsmasqConfig = func() error {
		stub.tests++
		if stub.failTest {
			return fmt.Errorf("dnsmasq: bad option")
		}
		return nil
	}
	restartDnsmasq = func() error {
		stub.restarts++
		if stub.failRestarts > 0 {
			stub.failRestarts--
			return fmt.Errorf("dnsmasq failed to start")
		}
		return nil
	}
	t.Cleanup(func() {
		dnsDropInPath, dnsQueryLogPath, testDnsmasqConfig, restartDnsmasq = previousDropIn, previousLog, previousTest, previousRestart
		dnsActivity.Lock()
		dnsActivity.pending, dnsActivity.dropped = nil, 0
		dnsActivity.Unlock()
	})
	if enabled {
		if err := os.WriteFile(dropInPath, []byte(dnsactivity.DropInFor(logPath)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return logPath, stub
}

func drain(enabled bool) dnsactivity.Response {
	return dnsActivityDrain(dnsactivity.Request{Version: dnsactivity.ProtocolVersion, Enabled: enabled})
}

func TestEnsureDNSQueryLoggingInstallsAndRemovesDropIn(t *testing.T) {
	_, stub := setupDNSActivityFiles(t, false)
	if err := ensureDNSQueryLogging(false); err != nil || stub.restarts != 0 {
		t.Fatalf("disabled-to-disabled restarted dnsmasq: %v restarts=%d", err, stub.restarts)
	}
	if err := ensureDNSQueryLogging(true); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(dnsDropInPath)
	if err != nil || string(content) != dnsactivity.DropInFor(dnsQueryLogPath) {
		t.Fatalf("drop-in = %q, %v", content, err)
	}
	if stub.tests != 1 || stub.restarts != 1 {
		t.Fatalf("enable ran %d tests and %d restarts, want 1 and 1", stub.tests, stub.restarts)
	}
	if err := ensureDNSQueryLogging(true); err != nil || stub.restarts != 1 {
		t.Fatalf("unchanged drop-in restarted dnsmasq again: %v restarts=%d", err, stub.restarts)
	}
	if err := ensureDNSQueryLogging(false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dnsDropInPath); !os.IsNotExist(err) || stub.restarts != 2 {
		t.Fatalf("disable left the drop-in or did not restart: %v restarts=%d", err, stub.restarts)
	}
}

func TestEnsureDNSQueryLoggingFailsClosed(t *testing.T) {
	_, stub := setupDNSActivityFiles(t, false)
	stub.failTest = true
	if err := ensureDNSQueryLogging(true); err == nil {
		t.Fatal("a rejected configuration was reported as enabled")
	}
	if _, err := os.Stat(dnsDropInPath); !os.IsNotExist(err) || stub.restarts != 0 {
		t.Fatalf("rejected drop-in remained or dnsmasq was restarted: %v restarts=%d", err, stub.restarts)
	}
	stub.failTest = false
	stub.failRestarts = 1
	if err := ensureDNSQueryLogging(true); err == nil {
		t.Fatal("a failed restart was reported as enabled")
	}
	if _, err := os.Stat(dnsDropInPath); !os.IsNotExist(err) || stub.restarts != 2 {
		t.Fatalf("failed restart did not restore dnsmasq without the drop-in: %v restarts=%d", err, stub.restarts)
	}
	stub.failTest = true
	if response := drain(true); response.Success {
		t.Fatal("drain reported success although logging could not be enabled")
	}
}

func TestDNSActivityDrainAggregatesAndTruncatesLog(t *testing.T) {
	logPath, _ := setupDNSActivityFiles(t, true)
	lines := strings.Join([]string{
		"Oct  6 12:00:00 dnsmasq[1]: query[A] www.example.com from 192.168.1.50",
		"Oct  6 12:00:00 dnsmasq[1]: forwarded www.example.com to 1.1.1.1",
		"Oct  6 12:00:01 dnsmasq[1]: query[AAAA] www.example.com from 192.168.1.50",
		"Oct  6 12:00:02 dnsmasq[1]: query[A] pornhub.com from 192.168.1.60",
		"Oct  6 12:00:03 dnsmasq[1]: " + strings.Repeat("x", 5000),
		"Oct  6 12:00:04 dnsmasq[1]: query[A] after.example.com from 192.168.1.61",
		"Oct  6 12:00:05 dnsmasq[1]: query[A] partial.example.com from 192.168",
	}, "\n")
	if err := os.WriteFile(logPath, []byte(lines), 0600); err != nil {
		t.Fatal(err)
	}
	response := drain(true)
	if !response.Success || !response.Enabled {
		t.Fatalf("drain failed: %+v", response)
	}
	got := map[string]uint32{}
	for _, entry := range response.Entries {
		got[entry.Client+" "+entry.Name] = entry.Count
	}
	want := map[string]uint32{"192.168.1.50 www.example.com": 2, "192.168.1.60 pornhub.com": 1, "192.168.1.61 after.example.com": 1}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	if info, err := os.Stat(logPath); err != nil || info.Size() != 0 {
		t.Fatalf("query log was not truncated: %v %v", info, err)
	}
	if again := drain(true); len(again.Entries) != 0 {
		t.Fatalf("drained entries were returned twice: %+v", again.Entries)
	}
}

func TestDNSActivityDisabledRemovesLogAndMemory(t *testing.T) {
	logPath, _ := setupDNSActivityFiles(t, true)
	if err := os.WriteFile(logPath, []byte("Oct  6 12:00:00 dnsmasq[1]: query[A] a.example.com from 192.168.1.50\n"), 0600); err != nil {
		t.Fatal(err)
	}
	collectDNSQueryLog(time.Now())
	response := drain(false)
	if !response.Success || response.Enabled || len(response.Entries) != 0 {
		t.Fatalf("disabled logging still returned lookups: %+v", response)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("query log survived disable: %v", err)
	}
}

func TestDNSActivityRefusesSymlinkedLog(t *testing.T) {
	logPath, _ := setupDNSActivityFiles(t, true)
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("Oct  6 12:00:00 dnsmasq[1]: query[A] a.example.com from 192.168.1.50\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, logPath); err != nil {
		t.Fatal(err)
	}
	collectDNSQueryLog(time.Now())
	if info, err := os.Stat(target); err != nil || info.Size() == 0 {
		t.Fatal("symlink target was truncated")
	}
	if len(dnsActivity.pending) != 0 {
		t.Fatal("symlinked log was parsed")
	}
}

func TestDNSActivityBoundsMemoryAndPages(t *testing.T) {
	logPath, _ := setupDNSActivityFiles(t, true)
	var builder strings.Builder
	total := dnsPendingMaxEntries + 50
	for i := 0; i < total; i++ {
		fmt.Fprintf(&builder, "Oct  6 12:00:00 dnsmasq[1]: query[A] host%d.%s.example.com from 192.168.1.50\n", i, strings.Repeat("b", 60))
	}
	if err := os.WriteFile(logPath, []byte(builder.String()), 0600); err != nil {
		t.Fatal(err)
	}
	seen, dropped, pages := 0, uint64(0), 0
	for {
		response := drain(true)
		encoded, err := json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		if len(encoded) > dnsactivity.MaxResponseBytes {
			t.Fatalf("page %d is %d bytes, over the IPC response limit", pages, len(encoded))
		}
		if _, err := dnsactivity.DecodeResponse(strings.NewReader(string(encoded))); err != nil {
			t.Fatalf("page %d does not decode: %v", pages, err)
		}
		seen += len(response.Entries)
		dropped += response.Dropped
		pages++
		if !response.More {
			break
		}
		if pages > 100 {
			t.Fatal("drain never finished")
		}
	}
	if seen != dnsPendingMaxEntries || dropped != 50 {
		t.Fatalf("seen=%d dropped=%d, want %d and 50", seen, dropped, dnsPendingMaxEntries)
	}
	if pages < 2 {
		t.Fatal("a large backlog was returned in one page")
	}
}
