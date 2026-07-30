package services

import (
	"bytes"
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestDeviceProfileRulesAllowWeekdayWindowAndEntireWeekend(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.AdGuard.Enabled = true
	cfg.AdGuard.DeviceProfiles = []config.DeviceProfile{{
		ID:          "kids",
		Name:        "Kids",
		IPAddresses: []string{"192.168.1.50"},
		Services:    []string{"youtube", "steam"},
		Enabled:     true,
		Schedule: config.WeeklyAccessSchedule{
			WeekdayWindows: []config.AccessWindow{{Start: "17:00", End: "21:00"}},
			WeekendMode:    "all_day",
		},
	}}
	var rules bytes.Buffer
	writeDeviceProfileObjects(&rules, &cfg)
	output := rules.String()
	for _, expected := range []string{
		"set svc_steam",
		"set svc_youtube",
		"meta day { monday, tuesday, wednesday, thursday, friday } meta hour \"17:00\"-\"21:00\" return",
		"meta day { saturday, sunday } return",
		"ip saddr 192.168.1.50 ip daddr @svc_youtube drop",
		"ip saddr 192.168.1.50 udp dport { 53, 853 } drop",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("missing %q in generated rules:\n%s", expected, output)
		}
	}
}

func TestDNSFilterNftsetsUseDnsmasqNftSyntax(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.AdGuard.Enabled = true
	cfg.AdGuard.DeviceProfiles = []config.DeviceProfile{{
		ID: "kids", Name: "Kids", IPAddresses: []string{"192.168.1.50"},
		Services: []string{"youtube"}, Enabled: true,
		Schedule: config.WeeklyAccessSchedule{WeekendMode: "blocked"},
	}}
	var output bytes.Buffer
	writeDNSFilterNftsets(&output, &cfg)
	if !strings.Contains(output.String(), "nftset=/youtube.com/googlevideo.com/ytimg.com/youtu.be/4#inet#minimalrouter#svc_youtube") {
		t.Fatalf("unexpected dnsmasq nftset output: %s", output.String())
	}
}
