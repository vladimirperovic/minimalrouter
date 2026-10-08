package main

import (
	"strings"
	"testing"
	"time"
)

func TestServiceDestinationsRetainRemainingSeconds(t *testing.T) {
	raw := []byte(`{"nftables":[{"set":{"family":"inet","table":"minimalrouter","name":"acct4","type":"ipv4_addr","elem":["192.0.2.10"]}},{"set":{"family":"inet","table":"minimalrouter","name":"svc_youtube","type":"ipv4_addr","elem":[{"elem":{"val":"203.0.113.9","timeout":14400,"expires":300}},{"elem":{"val":"203.0.113.10","expires":1}}]}}]}`)
	got, err := serviceDestinationBatch(raw, map[string]bool{"svc_youtube": true}, 2500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "add element inet minimalrouter svc_youtube { 203.0.113.9 timeout 296s }\n" {
		t.Fatalf("lifetimes were reset or mis-scaled: %s", got)
	}
	if dropped, err := serviceDestinationBatch(raw, map[string]bool{}, 0); err != nil || len(dropped) != 0 {
		t.Fatal("removed service was replayed")
	}
}

func TestServiceDestinationValidationAndBounds(t *testing.T) {
	for _, address := range []string{"203.0.113.9; flush ruleset", "::1", "0.0.0.0"} {
		raw := []byte(`{"nftables":[{"set":{"family":"inet","table":"minimalrouter","name":"svc_youtube","type":"ipv4_addr","elem":[{"elem":{"val":"` + address + `","expires":300}}]}}]}`)
		if _, err := serviceDestinationBatch(raw, map[string]bool{"svc_youtube": true}, 0); err == nil {
			t.Fatalf("accepted %q", address)
		}
	}
	generated := []byte("  set svc_youtube { type ipv4_addr; flags timeout; timeout 4h; }\n")
	bounded := boundServiceSets(generated)
	if !strings.Contains(string(bounded), "size 16384;") {
		t.Fatal("service set remains unbounded")
	}
}
