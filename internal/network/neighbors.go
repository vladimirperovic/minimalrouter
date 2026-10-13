package network

import (
	"net"
	"os"
	"strconv"
	"strings"
)

// ARPTablePath is the kernel's IPv4 neighbour table, readable without
// privileges.
const ARPTablePath = "/proc/net/arp"

// Neighbor is an IPv4 address the router has recently resolved on a LAN
// interface. Its presence proves the address is in use by that MAC; its
// absence proves nothing, because the table only holds recently active hosts.
type Neighbor struct {
	IP        string
	MAC       string
	Interface string
}

// ReadARPTable parses a /proc/net/arp style table and returns the resolved
// (ATF_COM) entries on iface, or on every interface when iface is empty.
// Read errors and malformed rows yield no observations rather than a guess.
func ReadARPTable(path, iface string) []Neighbor {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) < 2 {
		return nil
	}
	var out []Neighbor
	for _, line := range lines[1:] {
		// IP address, HW type, Flags, HW address, Mask, Device
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		flags, err := strconv.ParseUint(strings.TrimPrefix(fields[2], "0x"), 16, 32)
		if err != nil || flags&0x2 == 0 {
			continue
		}
		if iface != "" && fields[5] != iface {
			continue
		}
		ip := net.ParseIP(fields[0]).To4()
		mac, err := net.ParseMAC(fields[3])
		if ip == nil || err != nil || len(mac) != 6 || mac.String() == "00:00:00:00:00:00" {
			continue
		}
		out = append(out, Neighbor{IP: ip.String(), MAC: mac.String(), Interface: fields[5]})
	}
	return out
}
