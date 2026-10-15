// Package dnsfilter implements opt-in network DNS blocking outside the immutable
// bootstrap configuration contract. Downloads run as routerd; the root helper
// accepts only validated domains and writes one fixed dnsmasq drop-in.
package dnsfilter

import (
	"errors"
	"net/netip"
	"strings"

	"golang.org/x/net/publicsuffix"
)

const (
	SocketPath       = "/run/minimalrouter/dns-filter.sock"
	DropInPath       = "/etc/dnsmasq.d/minimalrouter-dns-filter.conf"
	MaxDomains       = 800000
	MaxFeedDomains   = 300000
	MaxFeedBytes     = 24 << 20
	MaxStreamBytes   = 48 << 20
	MaxMetadataBytes = 128 << 10
)

type Source struct {
	ID, Label, File string
	// Extra names are added to every downloaded copy of the list, for
	// endpoints the category depends on that the upstream list misses.
	Extra []string
}

// bypassExtra holds names the bypass category depends on but the upstream list
// misses or may drop. dnsmasq matches only the queried name, never the CNAMEs
// in an upstream answer, so every alias on the way to a blocked target that a
// device may query directly has to be listed as well.
//
//   - mask.icloud.com and mask-h2.icloud.com are the iCloud Private Relay
//     ingress names Apple documents; Safari Private Browsing's Oblivious DoH
//     uses the same relay. Both are CNAMEs (TTL up to about 5.5 hours) to
//     mask.apple-dns.net (TTL 120 s). Apple devices that cached the CNAME
//     before the category was on re-resolve only that target.
//   - mask-canary.icloud.com reaches the same target through
//     mask-canary.v.aaplimg.com.
//   - use-application-dns.net is Firefox's canary: a negative answer turns off
//     Firefox's default-on DNS-over-HTTPS for the network.
var bypassExtra = []string{
	"mask.icloud.com", "mask-h2.icloud.com", "mask.apple-dns.net",
	"mask-canary.icloud.com", "mask-canary.v.aaplimg.com",
	"use-application-dns.net",
}

func Sources() []Source {
	return []Source{
		{"threats", "Malware, phishing & scams", "tif.mini", nil},
		{"ads", "Ads & trackers", "light", nil},
		{"adult", "Adult content", "nsfw", nil},
		{"gambling", "Gambling", "gambling.mini", nil},
		// Encrypted DNS, Apple relay ingress, VPN, Tor and proxy endpoints, so
		// devices cannot quietly resolve around the other categories.
		{"bypass", "Encrypted DNS, VPN & proxy bypass", "doh-vpn-proxy-bypass", bypassExtra},
	}
}

func (s Source) URL() string {
	return "https://cdn.jsdelivr.net/gh/hagezi/dns-blocklists@latest/wildcard/" + s.File + "-onlydomains.txt"
}

type Exception struct {
	Domain string `json:"domain"`
	Reason string `json:"reason,omitempty"`
}

type Policy struct {
	Revision   uint64          `json:"revision"`
	Categories map[string]bool `json:"categories"`
	Exceptions []Exception     `json:"exceptions"`
}

func DefaultPolicy() Policy { return Policy{Categories: map[string]bool{}, Exceptions: []Exception{}} }

// Public domain only: no IP, URL, wildcard, local suffix or public suffix.
func Domain(raw string) (string, bool) {
	name := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
	if len(name) > 253 || !strings.Contains(name, ".") {
		return "", false
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return "", false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", false
			}
		}
	}
	suffix, _ := publicsuffix.PublicSuffix(name)
	if suffix == name || strings.HasSuffix(name, ".local") || strings.HasSuffix(name, ".lan") || strings.HasSuffix(name, ".arpa") {
		return "", false
	}
	return name, true
}

func (p Policy) Validate() error {
	if len(p.Categories) > len(Sources()) || len(p.Exceptions) > 200 {
		return errors.New("DNS filter policy exceeds safety limits")
	}
	for key := range p.Categories {
		found := false
		for _, source := range Sources() {
			if source.ID == key {
				found = true
			}
		}
		if !found {
			return errors.New("unknown DNS filter category")
		}
	}
	seen := map[string]bool{}
	for _, e := range p.Exceptions {
		d, ok := Domain(e.Domain)
		if !ok || d != e.Domain || seen[d] || len(e.Reason) > 200 || strings.ContainsAny(e.Reason, "\x00\r\n") {
			return errors.New("invalid or duplicate blocking exception")
		}
		seen[d] = true
	}
	return nil
}

// Exceptions intentionally cover a domain and all its subdomains. This matches
// dnsmasq's server routing semantics; exact-only exceptions are not advertised.
func (p Policy) Allowed(domain string) bool {
	for _, e := range p.Exceptions {
		if domain == e.Domain || strings.HasSuffix(domain, "."+e.Domain) {
			return true
		}
	}
	return false
}

type Applied struct {
	Policy            Policy            `json:"policy"`
	Domains           int               `json:"domains"`
	AppliedAt         int64             `json:"applied_at"`
	Healthy           bool              `json:"healthy"`
	Sources           map[string]string `json:"sources"`
	HealthCheckedAt   int64             `json:"health_checked_at"`
	HealthChecking    bool              `json:"health_checking"`
	ActivationPending bool              `json:"activation_pending"`
}

type Request struct {
	Version          int               `json:"version"`
	Operation        string            `json:"operation"`
	ExpectedRevision uint64            `json:"expected_revision"`
	Policy           Policy            `json:"policy"`
	Sources          map[string]string `json:"sources"`
}

type Response struct {
	Error string  `json:"error,omitempty"`
	State Applied `json:"state"`
}
