package telemetry

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	dnsmasqLeasePath    = "/run/minimalrouter/dnsmasq.leases"
	maxDHCPLeaseBytes   = 1 << 20
	maxDHCPLeaseEntries = 4096
)

func readDHCPLeases(path string) []DHCPLease {
	file, err := os.Open(path)
	if err != nil {
		return []DHCPLease{}
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxDHCPLeaseBytes+1))
	if err != nil {
		return []DHCPLease{}
	}
	if len(data) > maxDHCPLeaseBytes {
		return []DHCPLease{}
	}
	leases, err := parseDHCPLeases(data)
	if err != nil {
		return []DHCPLease{}
	}
	return activeDHCPLeasesAt(leases, time.Now().Unix())
}

func activeDHCPLeasesAt(leases []DHCPLease, now int64) []DHCPLease {
	active := make([]DHCPLease, 0, len(leases))
	for _, lease := range leases {
		if lease.ExpiresAt == 0 || lease.ExpiresAt > now {
			active = append(active, lease)
		}
	}
	return active
}

func parseDHCPLeases(data []byte) ([]DHCPLease, error) {
	if len(data) > maxDHCPLeaseBytes {
		return nil, errors.New("dnsmasq lease file is too large")
	}

	leases := make([]DHCPLease, 0)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 64*1024)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 5 {
			continue
		}
		expiresAt, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil || expiresAt < 0 {
			continue
		}
		mac, err := net.ParseMAC(fields[1])
		if err != nil || len(mac) != 6 {
			continue
		}
		ip := net.ParseIP(fields[2])
		if ip == nil || ip.To4() == nil {
			continue
		}
		hostname := fields[3]
		if hostname == "*" {
			hostname = ""
		}
		if len(hostname) > 253 || strings.ContainsAny(hostname, "\x00\r\n\t") {
			continue
		}
		leases = append(leases, DHCPLease{
			ExpiresAt: expiresAt,
			MAC:       strings.ToLower(mac.String()),
			IPAddress: ip.To4().String(),
			Hostname:  hostname,
		})
		if len(leases) >= maxDHCPLeaseEntries {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return leases, nil
}
