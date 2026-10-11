package apply

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestFirewallCounterProjectionAndGeneration(t *testing.T) {
	raw := `{"nftables":[{"table":{"family":"inet","name":"minimalrouter","handle":9}},{"counter":{"family":"inet","table":"minimalrouter","name":"mr_seen","handle":1,"packets":100}},{"counter":{"family":"inet","table":"minimalrouter","name":"mr_accepted","handle":2,"packets":90}},{"rule":{"secret":"not-for-the-response"}}]}`
	v, err := ParseFirewallCounters(raw, "boot-a")
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
		next, err := ParseFirewallCounters(changed, "boot-a")
		if err != nil || next.Generation == v.Generation {
			t.Fatal("table recreation not detected")
		}
	}
	next, err := ParseFirewallCounters(raw, "boot-b")
	if err != nil || next.Generation == v.Generation {
		t.Fatal("boot not detected")
	}
	for _, invalid := range []string{`{}`, `not-json`, strings.ReplaceAll(raw, "mr_seen", "other"), strings.ReplaceAll(raw, `"handle":9`, `"handle":0`), strings.ReplaceAll(raw, "mr_accepted", "mr_seen")} {
		if _, err := ParseFirewallCounters(invalid, "boot"); err == nil {
			t.Fatal("invalid projection accepted", invalid)
		}
	}
}

func TestFirewallObservationProjectsOnlyOwnedBaseChainsAndCounters(t *testing.T) {
	raw := `{"nftables":[{"table":{"family":"inet","name":"minimalrouter","handle":9}},{"counter":{"family":"inet","table":"minimalrouter","name":"mr_seen","handle":1,"packets":100}},{"counter":{"family":"inet","table":"minimalrouter","name":"mr_accepted","handle":2,"packets":90}},
 {"chain":{"family":"inet","table":"minimalrouter","name":"input","type":"filter","hook":"input","prio":0,"policy":"drop"}},
 {"chain":{"family":"inet","table":"minimalrouter","name":"forward","type":"filter","hook":"output","prio":0,"policy":"drop"}},
 {"chain":{"family":"inet","table":"other","name":"output","type":"filter","hook":"output","prio":0,"policy":"drop"}},
 {"counter":{"family":"inet","table":"minimalrouter","name":"mr_revision_42","packets":0}},
 {"counter":{"family":"inet","table":"minimalrouter","name":"mr_rule_0123456789abcdef01234567","packets":22}},
 {"counter":{"family":"inet","table":"minimalrouter","name":"private-name","packets":99}}]}`
	v, err := ParseFirewallCounters(raw, "boot")
	if err != nil {
		t.Fatal(err)
	}
	if v.LoadedRevision == nil || *v.LoadedRevision != 42 || len(v.Policies) != 1 || v.Policies["input"] != "drop" || len(v.RulePackets) != 1 || v.RulePackets["mr_rule_0123456789abcdef01234567"] != 22 || v.ObservedAt.IsZero() {
		t.Fatalf("bad projection: %+v", v)
	}
	duplicate := strings.Replace(raw, `{"counter":{"family":"inet","table":"minimalrouter","name":"private-name","packets":99}}`, `{"counter":{"family":"inet","table":"minimalrouter","name":"mr_revision_43","packets":0}}`, 1)
	if _, err := ParseFirewallCounters(duplicate, "boot"); err == nil {
		t.Fatal("ambiguous revision accepted")
	}
	var extra strings.Builder
	for i := 0; i < 1100; i++ {
		fmt.Fprintf(&extra, `,{"counter":{"family":"inet","table":"minimalrouter","name":"mr_rule_%024x","packets":1}}`, i)
	}
	v, err = ParseFirewallCounters(strings.TrimSuffix(raw, "]}")+extra.String()+"]}", "boot")
	if err != nil || len(v.RulePackets) != 1024 {
		t.Fatal("unbounded projection", err)
	}
}
