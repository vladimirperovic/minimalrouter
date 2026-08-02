package telemetry

import (
	"fmt"
	"testing"
	"time"
)

func TestParseWireGuardPeers(t *testing.T) {
	out := "sFZm9wTPd584YJoic8JsdrMmKk9y6kJgynj5g7tiqUE=\tNAD6L65kgOUoXlc0GWFJ6UpfQaEWHofUInCznUDk6yA=\t51820\toff\n" +
		"na0t+0NmolDiBbEsorMkT/zwya26DaEwrFxsAhxXYlA=\t(none)\t(none)\t10.6.0.3/32\t0\t0\t0\t25\n" +
		"pItVJx9or1aPvC5iDPZGrVW21dwYc04Rsv0Tyb0Jtlw=\t(none)\t(none)\t10.6.0.2/32\t0\t0\t0\t25\n"

	got := parseWireGuardDump(out)

	if len(got) != 2 {
		t.Fatalf("expected 2 peers, got %d: %#v", len(got), got)
	}
	if got[0].PublicKey != "na0t+0NmolDiBbEsorMkT/zwya26DaEwrFxsAhxXYlA=" {
		t.Errorf("unexpected public key %q", got[0].PublicKey)
	}
	if got[0].AllowedIPs != "10.6.0.3/32" {
		t.Errorf("unexpected allowed ips %q", got[0].AllowedIPs)
	}
	if got[0].Endpoint != "" {
		t.Errorf("expected empty endpoint, got %q", got[0].Endpoint)
	}
	if got[0].Online {
		t.Error("peer with handshake 0 should not be online")
	}
	if got[1].PublicKey != "pItVJx9or1aPvC5iDPZGrVW21dwYc04Rsv0Tyb0Jtlw=" {
		t.Errorf("unexpected second public key %q", got[1].PublicKey)
	}
}

func TestParseWireGuardDumpSkipsInterfaceLine(t *testing.T) {
	recent := time.Now().Unix() - 30
	out := "interface\tfakekey\t51820\n" +
		fmt.Sprintf("pk1\tpsk\t1.2.3.4:51820\t10.6.0.9/32\t%d\t1024\t2048\t25\n", recent)
	got := parseWireGuardDump(out)
	if len(got) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(got))
	}
	if got[0].Endpoint != "1.2.3.4:51820" {
		t.Errorf("unexpected endpoint %q", got[0].Endpoint)
	}
	if got[0].RXBytes != 1024 || got[0].TXBytes != 2048 {
		t.Errorf("unexpected transfer %d/%d", got[0].RXBytes, got[0].TXBytes)
	}
	if !got[0].Online {
		t.Error("peer with recent handshake should be online")
	}
}

func TestCountActiveWireGuardPeers(t *testing.T) {
	recent := time.Now().Unix() - 30
	out := "interface\tfakekey\t51820\n" +
		fmt.Sprintf("pk1\tpsk\t(none)\t10.0.0.2/32\t%d\t0\t0\t25\n", recent) +
		"pk2\tpsk\t(none)\t10.0.0.3/32\t0\t0\t0\t25\n"
	peers := parseWireGuardDump(out)
	active := 0
	for _, p := range peers {
		if p.Online {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("expected 1 active peer, got %d", active)
	}
}
