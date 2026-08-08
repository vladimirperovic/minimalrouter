package recovery

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/auth"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// Manager performs local-console recovery operations against the canonical
// store. It intentionally has no HTTP transport.
type Manager struct {
	Store *config.SQLiteStore
}

func (m Manager) ResetAuthentication(password string, disableTOTP bool) error {
	if m.Store == nil {
		return errors.New("recovery store is unavailable")
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("new administrator password: %w", err)
	}
	if err := m.Store.RecoveryResetAuthentication(hash, disableTOTP); err != nil {
		return err
	}
	return nil
}

func normalizedNetwork(cidr string) string {
	_, network, err := net.ParseCIDR(strings.TrimSpace(cidr))
	if err != nil || network == nil {
		return strings.TrimSpace(cidr)
	}
	return network.String()
}

// replaceNetworkEntry migrates only exact old-network entries. ensureNew is
// reserved for management trust: a recovery LAN must always be administrable
// from its new subnet, while service allowlists must never gain privileges they
// did not already have merely because the LAN address moved.
func replaceNetworkEntry(entries []string, oldNetwork, newNetwork string, ensureNew bool) []string {
	result := make([]string, 0, len(entries)+1)
	seen := make(map[string]struct{}, len(entries)+1)
	for _, entry := range entries {
		normalized := normalizedNetwork(entry)
		if normalized == oldNetwork {
			normalized = newNetwork
		}
		if normalized == "" {
			continue
		}
		if _, duplicate := seen[normalized]; duplicate {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	if ensureNew {
		if _, exists := seen[newNetwork]; !exists {
			result = append(result, newNetwork)
		}
	}
	return result
}

func replaceTrustedLANNetwork(entries []string, oldNetwork, newNetwork string) []string {
	return replaceNetworkEntry(entries, oldNetwork, newNetwork, true)
}

func ipv4Uint(ip net.IP) (uint32, bool) {
	v4 := ip.To4()
	if v4 == nil {
		return 0, false
	}
	return binary.BigEndian.Uint32(v4), true
}

func ipInRange(ip net.IP, start, end uint32) bool {
	value, ok := ipv4Uint(ip)
	return ok && value >= start && value <= end
}

// filterStaticLeasesForRecovery keeps only assignments that remain valid after
// the emergency LAN move. A static address colliding with the new gateway or
// recovery DHCP pool is dropped rather than making the entire recovery action
// fail validation when management access is already impaired.
func filterStaticLeasesForRecovery(leases []config.StaticLease, network *net.IPNet, gateway net.IP, start, end uint32) []config.StaticLease {
	result := make([]config.StaticLease, 0, len(leases))
	for _, lease := range leases {
		ip := net.ParseIP(strings.TrimSpace(lease.IPAddress))
		if ip == nil || ip.To4() == nil || !network.Contains(ip) || ip.Equal(gateway) || ipInRange(ip, start, end) {
			continue
		}
		result = append(result, lease)
	}
	return result
}

func (m Manager) SetLAN(interfaceName, cidr string) (config.Snapshot, error) {
	current, err := m.latest()
	if err != nil {
		return config.Snapshot{}, err
	}
	ip, network, err := net.ParseCIDR(strings.TrimSpace(cidr))
	if err != nil || ip.To4() == nil || !ip.Equal(ip.To4()) {
		return config.Snapshot{}, errors.New("LAN CIDR must contain an IPv4 host address")
	}
	prefix, bits := network.Mask.Size()
	if bits != 32 || prefix < 16 || prefix > 24 {
		return config.Snapshot{}, errors.New("recovery LAN prefix must be between /16 and /24")
	}
	if strings.TrimSpace(interfaceName) == "" || interfaceName == current.WAN.Interface {
		return config.Snapshot{}, errors.New("LAN interface must be non-empty and distinct from WAN")
	}

	startValue, endValue := dhcpRange(network, ip)
	start := uint32IP(startValue).String()
	end := uint32IP(endValue).String()
	oldNetwork := normalizedNetwork(current.LAN.CIDR)
	newNetwork := network.String()
	next := current.DeepCopy()
	next.Revision++
	next.UpdatedAt = time.Now().UTC()
	next.LAN.Interface = interfaceName
	next.LAN.IPAddress = ip.String()
	next.LAN.CIDR = ip.String() + fmt.Sprintf("/%d", prefix)
	next.LAN.Netmask = net.IP(network.Mask).String()
	next.DHCP.RangeStart = start
	next.DHCP.RangeEnd = end
	// Static assignments from the old subnet cannot be safely guessed into a
	// new address space. Keep only entries already valid in the target network
	// and outside the recovery DHCP pool/gateway; the operator can reassign the
	// rest after management is restored.
	next.DHCP.StaticLeases = filterStaticLeasesForRecovery(next.DHCP.StaticLeases, network, ip, startValue, endValue)
	// Recovery must not successfully move the address only to have the API
	// trusted-network middleware reject every client on that new LAN.
	next.TrustedNetworks = replaceTrustedLANNetwork(next.TrustedNetworks, oldNetwork, newNetwork)
	// ExtraLAN service allowlists frequently name the home LAN subnet. Move an
	// exact old-LAN entry with the recovery address, but never add the new LAN
	// to an allowlist that did not previously trust the old LAN.
	for i := range next.Firewall.ExtraLANs {
		next.Firewall.ExtraLANs[i].AllowFrom = replaceNetworkEntry(next.Firewall.ExtraLANs[i].AllowFrom, oldNetwork, newNetwork, false)
	}
	if err := next.Validate(); err != nil {
		return config.Snapshot{}, fmt.Errorf("recovery LAN configuration is invalid: %w", err)
	}
	if err := next.ValidateScenarioSafety(); err != nil {
		return config.Snapshot{}, fmt.Errorf("recovery LAN scenario is unsafe: %w", err)
	}
	return m.Store.RecoverySaveConfig(current, next, nil, false)
}

func (m Manager) RestoreSnapshot(id string) (config.Snapshot, error) {
	current, err := m.latest()
	if err != nil {
		return config.Snapshot{}, err
	}
	target, err := m.Store.GetSnapshot(strings.TrimSpace(id))
	if err != nil {
		return config.Snapshot{}, err
	}
	var restored config.SystemConfig
	if err := json.Unmarshal([]byte(target.ConfigJSON), &restored); err != nil {
		return config.Snapshot{}, fmt.Errorf("decode snapshot: %w", err)
	}
	restored.Revision = current.Revision + 1
	restored.UpdatedAt = time.Now().UTC()
	if err := restored.Validate(); err != nil {
		return config.Snapshot{}, fmt.Errorf("snapshot configuration is no longer valid: %w", err)
	}
	if err := restored.ValidateScenarioSafety(); err != nil {
		return config.Snapshot{}, fmt.Errorf("snapshot scenario is no longer safe: %w", err)
	}
	return m.Store.RecoverySaveConfig(current, restored, nil, false)
}

func (m Manager) FactoryReset(wanInterface, lanInterface, password string) (config.Snapshot, error) {
	current, err := m.latest()
	if err != nil {
		return config.Snapshot{}, err
	}
	wanInterface = strings.TrimSpace(wanInterface)
	lanInterface = strings.TrimSpace(lanInterface)
	if wanInterface == "" || lanInterface == "" || wanInterface == lanInterface {
		return config.Snapshot{}, errors.New("factory reset requires distinct WAN and LAN interfaces")
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return config.Snapshot{}, fmt.Errorf("new administrator password: %w", err)
	}
	reset := config.DefaultConfig()
	reset.Revision = current.Revision + 1
	reset.UpdatedAt = time.Now().UTC()
	reset.WAN.Interface = wanInterface
	reset.LAN.Interface = lanInterface
	if err := reset.Validate(); err != nil {
		return config.Snapshot{}, fmt.Errorf("factory defaults are invalid: %w", err)
	}
	if err := reset.ValidateScenarioSafety(); err != nil {
		return config.Snapshot{}, fmt.Errorf("factory defaults are unsafe: %w", err)
	}
	return m.Store.RecoverySaveConfig(current, reset, &hash, true)
}

func (m Manager) ListSnapshots() ([]config.Snapshot, error) {
	if m.Store == nil {
		return nil, errors.New("recovery store is unavailable")
	}
	return m.Store.ListSnapshots()
}

func (m Manager) latest() (config.SystemConfig, error) {
	if m.Store == nil {
		return config.SystemConfig{}, errors.New("recovery store is unavailable")
	}
	current, err := m.Store.GetLatestConfig()
	if err != nil {
		return config.SystemConfig{}, fmt.Errorf("read canonical configuration: %w", err)
	}
	return current, nil
}

// dhcpRange returns a contiguous recovery pool of up to 101 addresses while
// never including the gateway. The traditional .100-.200 pool remains the
// default when safe. If the requested gateway occupies that range, the larger
// usable side of the subnet is selected instead. Recovery supports /16-/24,
// so at least one useful side always exists.
func dhcpRange(network *net.IPNet, gateway net.IP) (uint32, uint32) {
	base := binary.BigEndian.Uint32(network.IP.To4())
	mask := binary.BigEndian.Uint32(network.Mask)
	broadcast := base | ^mask
	firstUsable := base + 1
	lastUsable := broadcast - 1
	preferredStart := base + 100
	preferredEnd := base + 200
	gatewayValue, ok := ipv4Uint(gateway)
	if !ok || gatewayValue < preferredStart || gatewayValue > preferredEnd {
		return preferredStart, preferredEnd
	}

	belowCount := gatewayValue - firstUsable
	aboveCount := lastUsable - gatewayValue
	const maxPoolSize uint32 = 101
	if aboveCount >= belowCount {
		start := gatewayValue + 1
		end := start + maxPoolSize - 1
		if end > lastUsable {
			end = lastUsable
		}
		return start, end
	}
	end := gatewayValue - 1
	start := firstUsable
	if end-firstUsable+1 > maxPoolSize {
		start = end - maxPoolSize + 1
	}
	return start, end
}

func uint32IP(value uint32) net.IP {
	ip := make(net.IP, net.IPv4len)
	binary.BigEndian.PutUint32(ip, value)
	return ip
}
