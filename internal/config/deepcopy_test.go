package config

import (
	"encoding/json"
	"testing"
)

// EqualSection compares JSON, so a copy must encode exactly like its source.
// DeepCopy used to turn nil WireGuard peers, extra LANs and device profiles
// into empty slices; a backup restore preview then reported an
// "isolated-segment access policy" change for an unchanged configuration.
func TestDeepCopyEncodesIdenticallyIncludingNilSlices(t *testing.T) {
	withNil := DefaultConfig()
	withNil.WireGuard.Peers = nil
	withNil.Firewall.ExtraLANs = nil
	withNil.AdGuard.DeviceProfiles = nil

	withEmpty := DefaultConfig()
	withEmpty.WireGuard.Peers = []WireGuardPeer{}
	withEmpty.Firewall.ExtraLANs = []ExtraLANConfig{}
	withEmpty.AdGuard.DeviceProfiles = []DeviceProfile{{ID: "p", Name: "P", IPAddresses: []string{"192.168.1.20"}, Services: []string{"youtube"}}}

	for name, cfg := range map[string]SystemConfig{"nil": withNil, "empty": withEmpty} {
		want, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(cfg.DeepCopy())
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("%s: DeepCopy changed the encoding\nwant %s\ngot  %s", name, want, got)
		}
		if !EqualSection(cfg.Firewall.ExtraLANs, cfg.DeepCopy().Firewall.ExtraLANs) {
			t.Fatalf("%s: extra LANs differ after DeepCopy", name)
		}
	}
}
