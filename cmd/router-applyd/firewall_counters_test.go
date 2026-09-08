package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFirewallCounterProjectionAndGeneration(t *testing.T) {
	raw := `{"nftables":[{"table":{"family":"inet","name":"minimalrouter","handle":9}},{"counter":{"family":"inet","table":"minimalrouter","name":"mr_seen","handle":1,"packets":100}},{"counter":{"family":"inet","table":"minimalrouter","name":"mr_accepted","handle":2,"packets":90}},{"rule":{"secret":"not-for-the-response"}}]}`
	v, err := parseFirewallCounters(raw, "boot-a")
	if err != nil {
		t.Fatal(err)
	}
	if v.Seen != 100 || v.Accepted != 90 {
		t.Fatal(v)
	}
	marshaled, _ := json.Marshal(v)
	if strings.Contains(string(marshaled), "secret") {
		t.Fatal("raw rules leaked")
	}
	for _, changed := range []string{strings.Replace(raw, `"handle":9`, `"handle":10`, 1)} {
		next, err := parseFirewallCounters(changed, "boot-a")
		if err != nil || next.Generation == v.Generation {
			t.Fatal("table recreation not detected")
		}
	}
	next, err := parseFirewallCounters(raw, "boot-b")
	if err != nil || next.Generation == v.Generation {
		t.Fatal("boot not detected")
	}
	for _, invalid := range []string{`{}`, `not-json`, strings.ReplaceAll(raw, "mr_seen", "other"), strings.ReplaceAll(raw, `"handle":9`, `"handle":0`), strings.ReplaceAll(raw, "mr_accepted", "mr_seen")} {
		if _, err := parseFirewallCounters(invalid, "boot"); err == nil {
			t.Fatal("invalid projection accepted", invalid)
		}
	}
}
