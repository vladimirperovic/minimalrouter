package health

import (
	"fmt"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/gateway"
	"github.com/vladimirperovic/minimalrouter/internal/storage"
	"github.com/vladimirperovic/minimalrouter/internal/telemetry"
)

type State string

const (
	StateHealthy          State = "healthy"
	StateWarning          State = "warning"
	StateDegraded         State = "degraded"
	StateRecoveryRequired State = "recovery_required"
	StateUnknown          State = "unknown"
)

type Check struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	State   State  `json:"state"`
	Summary string `json:"summary"`
}

type Snapshot struct {
	State       State     `json:"state"`
	Headline    string    `json:"headline"`
	Checks      []Check   `json:"checks"`
	GeneratedAt time.Time `json:"generated_at"`
}

type Input struct {
	Config                config.SystemConfig
	Runtime               telemetry.RuntimeStatus
	Engine                apply.EngineStatus
	Gateway               gateway.Summary
	GatewayConfigured     bool
	UpdateTrustConfigured bool
	Facts                 RuntimeFacts
	LastBackupAt          *time.Time
	DNSResolves           *bool
	DNSError              string
	Now                   time.Time
}

func Build(input Input) Snapshot {
	now := input.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	checks := make([]Check, 0, 14)
	add := func(id, label string, state State, summary string) {
		checks = append(checks, Check{ID: id, Label: label, State: state, Summary: summary})
	}

	if input.Engine.RecoveryRequired {
		reason := input.Engine.RecoveryReason
		if reason == "" {
			reason = "Canonical reconciliation is required before configuration changes."
		}
		add("recovery", "Recovery", StateRecoveryRequired, reason)
	} else if input.Engine.Applying {
		add("recovery", "Configuration engine", StateWarning, "A configuration transaction is currently being applied.")
	} else {
		add("recovery", "Configuration engine", StateHealthy, "Canonical configuration is available for normal operation.")
	}

	storageStatus := input.Runtime.Storage
	switch {
	case !storageStatus.Available:
		add("storage", "Storage", StateUnknown, "Filesystem pressure telemetry is unavailable.")
	case storageStatus.Level == storage.PressureCritical:
		add("storage", "Storage", StateDegraded, fmt.Sprintf("Disk is %.1f%% used; durable mutations are blocked.", storageStatus.UsagePercent))
	case storageStatus.Level == storage.PressureWarning:
		add("storage", "Storage", StateWarning, fmt.Sprintf("Disk is %.1f%% used; free space should be reclaimed.", storageStatus.UsagePercent))
	default:
		add("storage", "Storage", StateHealthy, fmt.Sprintf("Disk usage is %.1f%%.", storageStatus.UsagePercent))
	}

	if input.Runtime.MemoryTotalBytes == 0 {
		add("memory", "Memory", StateUnknown, "Memory telemetry is unavailable.")
	} else {
		memoryPercent := float64(input.Runtime.MemoryUsedBytes) / float64(input.Runtime.MemoryTotalBytes) * 100
		switch {
		case memoryPercent >= 90:
			add("memory", "Memory", StateDegraded, fmt.Sprintf("Memory usage is %.1f%%.", memoryPercent))
		case memoryPercent >= 80:
			add("memory", "Memory", StateWarning, fmt.Sprintf("Memory usage is %.1f%%.", memoryPercent))
		default:
			add("memory", "Memory", StateHealthy, fmt.Sprintf("Memory usage is %.1f%%.", memoryPercent))
		}
	}

	if input.Runtime.ConntrackMax == 0 {
		add("conntrack", "Connection tracking", StateUnknown, "Conntrack telemetry is unavailable.")
	} else {
		usage := input.Runtime.ConntrackUsagePercent
		switch {
		case usage >= 90:
			add("conntrack", "Connection tracking", StateDegraded, fmt.Sprintf("Conntrack table is %.1f%% full.", usage))
		case usage >= 75:
			add("conntrack", "Connection tracking", StateWarning, fmt.Sprintf("Conntrack table is %.1f%% full.", usage))
		default:
			add("conntrack", "Connection tracking", StateHealthy, fmt.Sprintf("Conntrack table is %.1f%% full.", usage))
		}
	}

	if input.Runtime.Available {
		if input.Runtime.TimeSynchronized {
			add("time", "Time synchronization", StateHealthy, "Kernel clock reports synchronized time.")
		} else {
			add("time", "Time synchronization", StateWarning, "Clock is not synchronized; TOTP, TLS and signed-update checks may be affected.")
		}
	} else {
		add("time", "Time synchronization", StateUnknown, "Runtime time telemetry is unavailable.")
	}

	if !input.Config.WAN.Enabled {
		add("gateway", "WAN / gateway", StateHealthy, "WAN is intentionally disabled.")
	} else if !input.Runtime.WANConnected {
		add("gateway", "WAN / gateway", StateDegraded, "WAN is enabled but no connected IPv4 PPP interface is reported.")
	} else if !input.GatewayConfigured || !input.Gateway.Available || !input.Gateway.Enabled {
		add("gateway", "WAN / gateway", StateWarning, "WAN is connected but active gateway-quality monitoring is unavailable or disabled.")
	} else {
		switch string(input.Gateway.State) {
		case "healthy":
			add("gateway", "WAN / gateway", StateHealthy, "Gateway quality is healthy.")
		case "degraded":
			add("gateway", "WAN / gateway", StateWarning, "Gateway quality is degraded.")
		case "flapping":
			add("gateway", "WAN / gateway", StateWarning, "PPPoE is reconnecting frequently.")
		case "offline":
			add("gateway", "WAN / gateway", StateDegraded, "Gateway monitoring reports the WAN offline.")
		default:
			add("gateway", "WAN / gateway", StateUnknown, "Gateway health has not produced a stable classification yet.")
		}
	}

	if !input.Facts.Available {
		add("services", "Core services", StateUnknown, "Installed-appliance service telemetry is unavailable.")
	} else {
		if input.Facts.RouterdHealthy && input.Facts.ApplydHealthy && input.Facts.ApplySocketAvailable {
			add("services", "Core services", StateHealthy, "routerd and router-applyd supervision are healthy.")
		} else {
			add("services", "Core services", StateDegraded, "A supervised router process or the privileged apply socket is unavailable; inspect crash-loop/service state.")
		}

		if input.Config.DHCP.Enabled || input.Config.DHCP.DNSEnabled {
			if !input.Facts.DnsmasqStarted {
				add("dns_dhcp", "DNS / DHCP", StateDegraded, "DNS/DHCP is configured but dnsmasq is not started.")
			} else if input.DNSResolves != nil && !*input.DNSResolves {
				detail := ""
				if input.DNSError != "" {
					detail = ": " + input.DNSError
				}
				add("dns_dhcp", "DNS / DHCP", StateDegraded, "dnsmasq is started but cannot resolve public names"+detail)
			} else {
				add("dns_dhcp", "DNS / DHCP", StateHealthy, "dnsmasq is started and resolves public names.")
			}
		}

		if input.Config.WAN.Enabled {
			if input.Facts.PPPoEStarted {
				add("pppoe_service", "PPPoE service", StateHealthy, "The PPPoE OpenRC service is started.")
			} else {
				add("pppoe_service", "PPPoE service", StateDegraded, "WAN is enabled but the PPPoE OpenRC service is not started.")
			}
		}

		if input.Config.WireGuard.Enabled {
			if input.Facts.WireGuardInterfaceUp {
				add("wireguard", "WireGuard", StateHealthy, "The configured WireGuard interface is up.")
			} else {
				add("wireguard", "WireGuard", StateDegraded, "WireGuard is enabled but its interface is unavailable or down.")
			}
		}
	}

	if !input.UpdateTrustConfigured {
		add("update", "Updates", StateWarning, "The firmware verification trust anchor is not configured.")
	} else if input.Facts.UpdateStateAvailable && input.Facts.UpdatePending != "" {
		add("update", "Updates", StateWarning, "A verified update is staged and pending activation.")
	} else {
		add("update", "Updates", StateHealthy, "Signed update trust is configured with no pending activation reported.")
	}

	if input.LastBackupAt == nil {
		add("backup", "Encrypted backup", StateWarning, "No successful encrypted backup export is recorded in retained audit history.")
	} else {
		age := now.Sub(input.LastBackupAt.UTC())
		switch {
		case age > 30*24*time.Hour:
			add("backup", "Encrypted backup", StateDegraded, fmt.Sprintf("Last recorded encrypted backup export was %d days ago.", int(age.Hours()/24)))
		case age > 7*24*time.Hour:
			add("backup", "Encrypted backup", StateWarning, fmt.Sprintf("Last recorded encrypted backup export was %d days ago.", int(age.Hours()/24)))
		default:
			add("backup", "Encrypted backup", StateHealthy, "A recent encrypted backup export is recorded.")
		}
	}

	overall := StateHealthy
	for _, check := range checks {
		overall = worse(overall, check.State)
	}
	headline := "Appliance is healthy"
	switch overall {
	case StateRecoveryRequired:
		headline = "Recovery is required"
	case StateDegraded:
		headline = "Appliance is degraded"
	case StateWarning:
		headline = "Appliance has warnings"
	case StateUnknown:
		headline = "Appliance health is partially unknown"
	}
	return Snapshot{State: overall, Headline: headline, Checks: checks, GeneratedAt: now.UTC()}
}

func worse(current, candidate State) State {
	severity := func(state State) int {
		switch state {
		case StateRecoveryRequired:
			return 5
		case StateDegraded:
			return 4
		case StateWarning:
			return 3
		case StateUnknown:
			return 2
		case StateHealthy:
			return 1
		default:
			return 0
		}
	}
	if severity(candidate) > severity(current) {
		return candidate
	}
	return current
}
