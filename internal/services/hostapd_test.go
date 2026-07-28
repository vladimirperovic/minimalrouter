package services

import (
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestGenerateHostapdJoinsProtectedLANBridge(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WiFi.Enabled = true
	cfg.WiFi.Interface = "wlan0"
	cfg.WiFi.SSID = "Office"
	cfg.WiFi.Passphrase = "secure-wifi-passphrase"
	cfg.WiFi.Band = "5ghz"
	cfg.WiFi.Channel = 36

	out, err := GenerateHostapd(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"interface=wlan0",
		"bridge=" + config.WiFiBridgeInterface,
		"wpa_key_mgmt=WPA-PSK SAE",
		"ieee80211w=1",
		"rsn_pairwise=CCMP",
	} {
		if !strings.Contains(out, expected) {
			t.Fatalf("missing %q in hostapd config:\n%s", expected, out)
		}
	}
	if strings.Contains(out, "TKIP") {
		t.Fatal("hostapd config enabled TKIP")
	}
}

func TestLANGeneratorsUseBridgeWhenWiFiEnabled(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WiFi.Enabled = true
	cfg.WiFi.Passphrase = "secure-wifi-passphrase"

	dnsmasq, err := GenerateDnsmasq(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	nftables, err := GenerateNftables(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dnsmasq, "interface="+config.WiFiBridgeInterface) {
		t.Fatalf("dnsmasq is not bound to the Wi-Fi LAN bridge:\n%s", dnsmasq)
	}
	if !strings.Contains(nftables, `iifname "`+config.WiFiBridgeInterface+`"`) {
		t.Fatalf("firewall does not protect the Wi-Fi LAN bridge:\n%s", nftables)
	}
}
