package api

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/dnsfilter"
	"github.com/vladimirperovic/minimalrouter/internal/services"
	"github.com/vladimirperovic/minimalrouter/internal/telemetry"
)

type dnsFilterDevice struct {
	IP       string `json:"ip"`
	Name     string `json:"name"`
	Reserved bool   `json:"reserved"`
}
type dnsFilterContext struct {
	ConfigRevision config.Revision          `json:"config_revision"`
	RouterTime     string                   `json:"router_time"`
	Timezone       string                   `json:"timezone"`
	BundledEnabled bool                     `json:"bundled_enabled"`
	Profiles       []services.ProfileStatus `json:"profiles"`
	Devices        []dnsFilterDevice        `json:"devices"`
	Blockers       []string                 `json:"blockers"`
}

func (s *Server) dnsFilterContext() dnsFilterContext {
	cfg, now := s.engine.GetCurrentConfig(), time.Now()
	result := dnsFilterContext{ConfigRevision: cfg.Revision, RouterTime: now.Format(time.RFC3339), Timezone: now.Location().String(), BundledEnabled: cfg.AdGuard.Enabled, Profiles: services.DeviceProfileStatuses(cfg, now), Devices: []dnsFilterDevice{}, Blockers: []string{}}
	devices := map[string]dnsFilterDevice{}
	for _, lease := range telemetry.CurrentDHCPLeases() {
		devices[lease.IPAddress] = dnsFilterDevice{IP: lease.IPAddress, Name: lease.Hostname}
	}
	for _, lease := range cfg.DHCP.StaticLeases {
		devices[lease.IPAddress] = dnsFilterDevice{IP: lease.IPAddress, Name: lease.Hostname, Reserved: true}
	}
	for _, device := range devices {
		result.Devices = append(result.Devices, device)
	}
	sort.Slice(result.Devices, func(i, j int) bool { return result.Devices[i].IP < result.Devices[j].IP })
	if s.engine.GetPendingTransaction() != nil {
		result.Blockers = append(result.Blockers, "Confirm or roll back the pending network change first.")
	}
	if s.engine.GetStatus().RecoveryRequired {
		result.Blockers = append(result.Blockers, "Resolve configuration recovery before changing DNS protection.")
	}
	return result
}

func (s *Server) handleDNSFilterProfiles(w http.ResponseWriter, r *http.Request) {
	writeDNSActivityJSON(w, s.dnsFilterContext())
}

func (s *Server) handleDNSFilterOperations(w http.ResponseWriter, r *http.Request) {
	if !s.requireDNSFilter(w) {
		return
	}
	writeDNSActivityJSON(w, map[string]any{"operations": s.dnsFilter.History(), "retention": dnsfilter.OperationRetention})
}

type dnsDecisionLayer struct {
	Source string `json:"source"`
	Action string `json:"action"`
	Reason string `json:"reason"`
}

type dnsDomainExplanation struct {
	dnsfilter.Check
	ConfigRevision config.Revision         `json:"config_revision"`
	DeviceIP       string                  `json:"device_ip,omitempty"`
	Scope          string                  `json:"scope"`
	Layers         []dnsDecisionLayer      `json:"layers"`
	Profile        *services.ProfileStatus `json:"profile,omitempty"`
	Coverage       []string                `json:"coverage"`
}

func localDNSMatch(cfg config.SystemConfig, leases []telemetry.DHCPLease, domain string) (string, string) {
	for _, record := range cfg.DNS.Records {
		if strings.EqualFold(strings.TrimSuffix(record.Name, "."), domain) {
			return "local_record", record.IP
		}
	}
	if !cfg.DHCP.Enabled {
		return "", ""
	}
	matches := func(name string) bool {
		return name != "" && name != "*" && (strings.EqualFold(name, domain) || strings.EqualFold(name+"."+cfg.System.Domain, domain))
	}
	// Only active leases are evidence of a DHCP name. A reservation alone is
	// not proof the resolver currently publishes that hostname.
	for _, lease := range leases {
		if matches(lease.Hostname) {
			return "dhcp_name", lease.IPAddress
		}
	}
	return "", ""
}

func explainDNSDomain(cfg config.SystemConfig, leases []telemetry.DHCPLease, result dnsfilter.Check, device string, now time.Time) dnsDomainExplanation {
	out := dnsDomainExplanation{Check: result, ConfigRevision: cfg.Revision, DeviceIP: device, Scope: "configured_policy", Layers: []dnsDecisionLayer{}, Coverage: []string{"A policy check is not proof of a blocked query or a visit.", "Device schedules depend on router DNS answers and IPv4 destination sets; runtime enforcement is not measured per profile.", "External encrypted DNS, VPNs and mobile data can bypass DNS category filtering."}}
	if cfg.AdGuard.Enabled && !result.Exception {
		for _, domain := range services.BuiltinBlocklist() {
			if result.Domain == domain || strings.HasSuffix(result.Domain, "."+domain) {
				out.Action = "Block"
				out.Matches = append(out.Matches, dnsfilter.Match{Category: "bundled", Domain: domain, Enabled: true})
			}
		}
	}
	for _, match := range out.Matches {
		action := "inactive"
		if match.Enabled {
			action = "block"
		}
		out.Layers = append(out.Layers, dnsDecisionLayer{match.Category, action, "Matches " + match.Domain})
	}
	if out.Exception {
		out.Layers = append(out.Layers, dnsDecisionLayer{"exception", "allow", "An exception allows this domain and its subdomains through DNS lists; device schedules still apply."})
	}
	kind, address := localDNSMatch(cfg, leases, result.Domain)
	if kind != "" {
		out.Action = "Local DNS record"
		out.Layers = append(out.Layers, dnsDecisionLayer{kind, "local", "Local address " + address + " takes precedence over DNS lists."})
	}
	for _, profile := range services.DeviceProfileStatuses(cfg, now) {
		found := false
		for _, ip := range profile.IPAddresses {
			if ip == device {
				found = true
			}
		}
		if !found {
			continue
		}
		out.Profile = &profile
		matched := false
		for _, service := range profile.Services {
			for _, domain := range services.ServiceDomains[service] {
				if result.Domain == domain || strings.HasSuffix(result.Domain, "."+domain) {
					matched = true
				}
			}
		}
		if matched {
			out.Layers = append(out.Layers, dnsDecisionLayer{"device_schedule", profile.State, "Configured schedule for " + profile.Name + "; actual packet enforcement remains unverified."})
			if profile.State == "blocked" && kind == "" && out.Action != "Block" {
				out.Action = "Scheduled block"
			}
		}
		break
	}
	if len(out.Layers) == 0 {
		out.Layers = append(out.Layers, dnsDecisionLayer{"lists", "no_match", "No matching rule in the available local catalogs. This does not prove unrestricted access."})
	}
	return out
}
