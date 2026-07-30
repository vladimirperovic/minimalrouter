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
	if err := m.Store.SetAdminHash(hash); err != nil {
		return fmt.Errorf("store administrator password: %w", err)
	}
	if disableTOTP {
		if err := m.Store.ClearAdminTOTPSecret(); err != nil {
			return fmt.Errorf("clear TOTP: %w", err)
		}
	}
	if err := m.Store.DeleteAllSessions(); err != nil {
		return fmt.Errorf("revoke sessions: %w", err)
	}
	return nil
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

	snapshot, err := m.Store.CreateSnapshot(current)
	if err != nil {
		return config.Snapshot{}, fmt.Errorf("create pre-recovery snapshot: %w", err)
	}
	start, end := dhcpRange(network)
	current.Revision++
	current.UpdatedAt = time.Now().UTC()
	current.LAN.Interface = interfaceName
	current.LAN.IPAddress = ip.String()
	current.LAN.CIDR = ip.String() + fmt.Sprintf("/%d", prefix)
	current.LAN.Netmask = net.IP(network.Mask).String()
	current.DHCP.RangeStart = start
	current.DHCP.RangeEnd = end
	if err := current.Validate(); err != nil {
		return config.Snapshot{}, fmt.Errorf("recovery LAN configuration is invalid: %w", err)
	}
	if err := m.Store.SaveConfig(current); err != nil {
		return config.Snapshot{}, fmt.Errorf("persist recovery LAN configuration: %w", err)
	}
	return snapshot, nil
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
	preRestore, err := m.Store.CreateSnapshot(current)
	if err != nil {
		return config.Snapshot{}, fmt.Errorf("create pre-restore snapshot: %w", err)
	}
	restored.Revision = current.Revision + 1
	restored.UpdatedAt = time.Now().UTC()
	if err := restored.Validate(); err != nil {
		return config.Snapshot{}, fmt.Errorf("snapshot configuration is no longer valid: %w", err)
	}
	if err := m.Store.SaveConfig(restored); err != nil {
		return config.Snapshot{}, fmt.Errorf("persist restored snapshot: %w", err)
	}
	return preRestore, nil
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
	snapshot, err := m.Store.CreateSnapshot(current)
	if err != nil {
		return config.Snapshot{}, fmt.Errorf("create pre-reset snapshot: %w", err)
	}
	reset := config.DefaultConfig()
	reset.Revision = current.Revision + 1
	reset.UpdatedAt = time.Now().UTC()
	reset.WAN.Interface = wanInterface
	reset.LAN.Interface = lanInterface
	if err := reset.Validate(); err != nil {
		return config.Snapshot{}, fmt.Errorf("factory defaults are invalid: %w", err)
	}
	if err := m.Store.SaveConfig(reset); err != nil {
		return config.Snapshot{}, fmt.Errorf("persist factory defaults: %w", err)
	}
	if err := m.Store.SetAdminHash(hash); err != nil {
		return config.Snapshot{}, fmt.Errorf("store reset administrator password: %w", err)
	}
	if err := m.Store.ClearAdminTOTPSecret(); err != nil {
		return config.Snapshot{}, fmt.Errorf("clear TOTP: %w", err)
	}
	if err := m.Store.DeleteAllSessions(); err != nil {
		return config.Snapshot{}, fmt.Errorf("revoke sessions: %w", err)
	}
	return snapshot, nil
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

func dhcpRange(network *net.IPNet) (string, string) {
	base := binary.BigEndian.Uint32(network.IP.To4())
	return uint32IP(base + 100).String(), uint32IP(base + 200).String()
}

func uint32IP(value uint32) net.IP {
	ip := make(net.IP, net.IPv4len)
	binary.BigEndian.PutUint32(ip, value)
	return ip
}
