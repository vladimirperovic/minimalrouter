package services

import (
	"bytes"
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestDeviceProfileRulesAllowPerDayWindows(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.AdGuard.Enabled = true
	cfg.AdGuard.DeviceProfiles = []config.DeviceProfile{{
		ID:          "kids",
		Name:        "Kids",
		IPAddresses: []string{"192.168.1.50"},
		Services:    []string{"youtube", "steam"},
		Enabled:     true,
		Schedule: config.WeeklyAccessSchedule{DayWindows: map[string][]config.AccessWindow{
			"monday":  {{Start: "17:00", End: "21:00"}},
			"saturday": {{Start: "00:00", End: "23:59"}},
		}},
	}}
	var rules bytes.Buffer
	writeDeviceProfileObjects(&rules, &cfg)
	output := rules.String()
	for _, expected := range []string{
		"set svc_steam",
		"set svc_youtube",
		"meta day monday meta hour \"17:00\"-\"21:00\" return",
		"meta day saturday meta hour \"00:00\"-\"23:59\" return",
		"ip saddr 192.168.1.50 ip daddr @svc_youtube drop",
		"ip saddr 192.168.1.50 udp dport { 53, 853 } drop",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("missing %q in generated rules:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "meta day sunday") {
		t.Fatalf("unexpected Sunday access in generated rules:\n%s", output)
	}
}

func TestLegacyScheduleStillGeneratesRules(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.AdGuard.Enabled = true
	cfg.AdGuard.DeviceProfiles = []config.DeviceProfile{{
		ID: "legacy", Name: "Legacy", IPAddresses: []string{"192.168.1.60"},
		Services: []string{"youtube"}, Enabled: true,
		Schedule: config.WeeklyAccessSchedule{
			WeekdayWindows: []config.AccessWindow{{Start: "19:00", End: "23:00"}},
			WeekendMode:    "all_day",
		},
	}}
	var rules bytes.Buffer
	writeDeviceProfileObjects(&rules, &cfg)
	output := rules.String()
	if !strings.Contains(output, "meta day friday meta hour \"19:00\"-\"23:00\" return") ||
		!strings.Contains(output, "meta day sunday meta hour \"00:00\"-\"23:59\" return") {
		t.Fatalf("legacy schedule was not translated:\n%s", output)
	}
}

func TestDNSFilterNftsetsUseDnsmasqNftSyntax(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.AdGuard.Enabled = true
	cfg.AdGuard.DeviceProfiles = []config.DeviceProfile{{
		ID: "kids", Name: "Kids", IPAddresses: []string{"192.168.1.50"},
		Services: []string{"youtube"}, Enabled: true,
		Schedule: config.WeeklyAccessSchedule{DayWindows: map[string][]config.AccessWindow{}},
	}}
	var output bytes.Buffer
	writeDNSFilterNftsets(&output, &cfg)
	if !strings.Contains(output.String(), "nftset=/youtube.com/googlevideo.com/ytimg.com/youtu.be/4#inet#minimalrouter#svc_youtube") {
		t.Fatalf("unexpected dnsmasq nftset output: %s", output.String())
	}
}
