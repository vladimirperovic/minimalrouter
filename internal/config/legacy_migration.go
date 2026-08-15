package config

import (
	"encoding/binary"
	"fmt"
	"net"
	"strings"
)

// MigrateLegacyFields fills fields that were added after the original
// configuration schema. It intentionally mutates only fields that are absent
// and can be derived without guessing beyond the existing configuration.
//
// Older ExtraLAN records did not persist RouterAddress. The first usable host
// in the configured subnet is the deterministic router-side gateway that the
// current ExtraLAN implementation expects.
func (c *SystemConfig) MigrateLegacyFields() {
	for i := range c.Firewall.ExtraLANs {
		lan := &c.Firewall.ExtraLANs[i]
		if !lan.Enabled || strings.TrimSpace(lan.RouterAddress) != "" {
			continue
		}

		_, network, err := net.ParseCIDR(strings.TrimSpace(lan.CIDR))
		if err != nil || network == nil || network.IP.To4() == nil {
			continue
		}
		prefix, bits := network.Mask.Size()
		if bits != 32 || prefix >= 31 {
			// /31 and /32 have no address that passes the current
			// network/broadcast gateway validation.
			continue
		}

		networkValue := binary.BigEndian.Uint32(network.IP.To4())
		broadcastValue := networkValue | ^binary.BigEndian.Uint32(network.Mask)
		gatewayValue := networkValue + 1
		if gatewayValue >= broadcastValue {
			continue
		}

		gateway := make(net.IP, net.IPv4len)
		binary.BigEndian.PutUint32(gateway, gatewayValue)
		lan.RouterAddress = fmt.Sprintf("%s/%d", gateway.String(), prefix)
	}
}
