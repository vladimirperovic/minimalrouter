package apply

import (
	"fmt"
	"reflect"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// ChangePreview is a read-only assessment of a candidate configuration. It
// shares the same transition-safety and confirmation rules as the real apply
// path, but never generates files, snapshots state, or contacts router-applyd.
type ChangePreview struct {
	Changes              []string `json:"changes"`
	Risk                 string   `json:"risk"`
	RequiresConfirmation bool     `json:"requires_confirmation"`
	RollbackSeconds      int      `json:"rollback_seconds,omitempty"`
	ExpectedInterruption string   `json:"expected_interruption"`
}

func PreviewTransition(current, candidate config.SystemConfig) (ChangePreview, error) {
	if candidate.Revision != current.Revision {
		return ChangePreview{}, fmt.Errorf("stale revision: expected %d, received %d", current.Revision, candidate.Revision)
	}
	if candidate.LAN.Interface != current.LAN.Interface {
		return ChangePreview{}, fmt.Errorf("live LAN interface changes are unsupported; use the local recovery console")
	}
	if !sameIPv4Network(current.LAN.CIDR, candidate.LAN.CIDR) {
		return ChangePreview{}, fmt.Errorf("live LAN subnet changes are unsupported; use the local recovery console")
	}
	if candidate.System.HTTPSPort != current.System.HTTPSPort {
		return ChangePreview{}, fmt.Errorf("live management-port changes are unsupported")
	}
	if candidate.System.ManagementAccess == "wireguard_only" && current.System.ManagementAccess != "wireguard_only" && !current.WireGuard.Enabled {
		return ChangePreview{}, fmt.Errorf("enable and verify WireGuard in a separate transaction before restricting management access")
	}
	if err := candidate.Validate(); err != nil {
		return ChangePreview{}, fmt.Errorf("validation failed: %w", err)
	}
	if err := candidate.ValidateScenarioSafety(); err != nil {
		return ChangePreview{}, fmt.Errorf("scenario safety validation failed: %w", err)
	}
	if err := validateTransitionSafety(current, candidate); err != nil {
		return ChangePreview{}, fmt.Errorf("transition safety validation failed: %w", err)
	}

	changes := changedSections(current, candidate)
	confirmation := requiresConfirmation(current, candidate)
	risk := "low"
	if confirmation {
		risk = "high"
	} else if containsAny(changes, "WAN / PPPoE", "Firewall", "WireGuard", "DHCP / DNS", "QoS") {
		risk = "medium"
	}
	interruption := "No management interruption expected. Affected services may restart briefly."
	rollbackSeconds := 0
	if confirmation {
		rollbackSeconds = int(confirmationTimeout / 1e9)
		interruption = "Management connectivity may change. The candidate is provisional until confirmed."
	}
	if len(changes) == 0 {
		interruption = "No effective configuration changes detected."
	}
	return ChangePreview{
		Changes:              changes,
		Risk:                 risk,
		RequiresConfirmation: confirmation,
		RollbackSeconds:      rollbackSeconds,
		ExpectedInterruption: interruption,
	}, nil
}

func changedSections(current, candidate config.SystemConfig) []string {
	type section struct {
		name string
		a    any
		b    any
	}
	sections := []section{
		{"System / management", current.System, candidate.System},
		{"WAN / PPPoE", current.WAN, candidate.WAN},
		{"LAN", current.LAN, candidate.LAN},
		{"DHCP / DNS", struct{ DHCP, DNS any }{current.DHCP, current.DNS}, struct{ DHCP, DNS any }{candidate.DHCP, candidate.DNS}},
		{"Firewall", current.Firewall, candidate.Firewall},
		{"WireGuard", struct{ Server, Client any }{current.WireGuard, current.WGClient}, struct{ Server, Client any }{candidate.WireGuard, candidate.WGClient}},
		{"Dynamic DNS / tunnel", current.Cloudflare, candidate.Cloudflare},
		{"Squid proxy", current.SquidProxy, candidate.SquidProxy},
		{"DNS filtering / device profiles", current.AdGuard, candidate.AdGuard},
		{"QoS", current.QoS, candidate.QoS},
		{"Traffic accounting", current.Accounting, candidate.Accounting},
		{"Wi-Fi", current.WiFi, candidate.WiFi},
		{"Trusted management networks", current.TrustedNetworks, candidate.TrustedNetworks},
	}
	changes := make([]string, 0, len(sections))
	for _, item := range sections {
		if !reflect.DeepEqual(item.a, item.b) {
			changes = append(changes, item.name)
		}
	}
	return changes
}

func containsAny(values []string, wanted ...string) bool {
	for _, value := range values {
		for _, candidate := range wanted {
			if value == candidate {
				return true
			}
		}
	}
	return false
}
