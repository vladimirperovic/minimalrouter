package services

import (
	"fmt"
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestNetworkReviewIsConcreteBoundedAndRedactsCredentials(t *testing.T) {
	current := config.DefaultConfig()
	next := current
	next.LAN.IPAddress = "192.168.1.2"
	next.WAN.Username = "review-private-user"
	next.WAN.Password = "review-private-password"
	next.DNS.Records = []config.DNSRecord{{Name: "nas.home.arpa", IP: "192.168.1.20"}}
	changes := DescribeNetworkChanges(current, next)
	text := strings.Join(changes, "\n")
	if !strings.Contains(text, "192.168.1.1 → 192.168.1.2") || !strings.Contains(text, "nas.home.arpa → 192.168.1.20") {
		t.Fatal(text)
	}
	if strings.Contains(text, next.WAN.Username) || strings.Contains(text, next.WAN.Password) {
		t.Fatal("preview exposed credentials")
	}
	next.DNS.Records = nil
	for i := 0; i < 100; i++ {
		next.DNS.Records = append(next.DNS.Records, config.DNSRecord{Name: fmt.Sprintf("host-%d.home.arpa", i), IP: "192.168.1.20"})
	}
	changes = DescribeNetworkChanges(current, next)
	if len(changes) != 51 || !strings.Contains(changes[50], "additional network changes") {
		t.Fatal("unbounded preview", len(changes))
	}
}
