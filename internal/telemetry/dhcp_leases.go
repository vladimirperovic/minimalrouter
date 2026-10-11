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
	dnsmasqLeasePath    = "/var/lib/minimalrouter-dhcp/dnsmasq.leases"
	maxDHCPLeaseBytes   = 1 << 20
	maxDHCPLeaseEntries = 4096
)

func readDHCPLeases(path string) []DHCPLease {
	return readDHCPLeaseSnapshot(path).Leases
}

// DHCPLeaseStatus distinguishes an empty lease table from an unreadable or
// incomplete one. CollectedAt records observation time, not client liveness.
type DHCPLeaseStatus struct {
	Available       bool      `json:"available"`
	CollectedAt     time.Time `json:"collected_at"`
	Truncated       bool      `json:"truncated"`
	RejectedEntries int       `json:"rejected_entries"`
	Error           string    `json:"error,omitempty"`
}

type DHCPLeaseSnapshot struct {
	DHCPLeaseStatus
	Leases []DHCPLease `json:"leases"`
}

func readDHCPLeaseSnapshot(path string) DHCPLeaseSnapshot {
	snapshot := DHCPLeaseSnapshot{DHCPLeaseStatus: DHCPLeaseStatus{CollectedAt: time.Now().UTC()}, Leases: []DHCPLease{}}
	file, err := os.Open(path)
	if err != nil {
		snapshot.Error = "DHCP lease file is unavailable"
		return snapshot
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxDHCPLeaseBytes+1))
	if err != nil {
		snapshot.Error = "DHCP leases could not be read"
		return snapshot
	}
	if len(data) > maxDHCPLeaseBytes {
		snapshot.Error = "DHCP lease file exceeds the safe read limit"
		snapshot.Truncated = true
		return snapshot
	}
	leases, err := parseDHCPLeasesWithStatus(data, &snapshot.DHCPLeaseStatus)
	if err != nil {
		snapshot.Error = "DHCP lease data could not be parsed"
		return snapshot
	}
	snapshot.Available = true
	snapshot.Leases = activeDHCPLeasesAt(leases, snapshot.CollectedAt.Unix())
	return snapshot
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
	return parseDHCPLeasesWithStatus(data, &DHCPLeaseStatus{})
}

func parseDHCPLeasesWithStatus(data []byte, status *DHCPLeaseStatus) ([]DHCPLease, error) {
	if len(data) > maxDHCPLeaseBytes {
		return nil, errors.New("dnsmasq lease file is too large")
	}

	leases := make([]DHCPLease, 0)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 64*1024)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 5 {
			status.RejectedEntries++
			continue
		}
		expiresAt, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil || expiresAt < 0 {
			status.RejectedEntries++
			continue
		}
		mac, err := net.ParseMAC(fields[1])
		if err != nil || len(mac) != 6 {
			status.RejectedEntries++
			continue
		}
		ip := net.ParseIP(fields[2])
		if ip == nil || ip.To4() == nil {
			status.RejectedEntries++
			continue
		}
		hostname := fields[3]
		if hostname == "*" {
			hostname = ""
		}
		if len(hostname) > 253 || strings.ContainsAny(hostname, "\x00\r\n\t") {
			status.RejectedEntries++
			continue
		}
		if len(leases) >= maxDHCPLeaseEntries {
			status.Truncated = true
			break
		}
		leases = append(leases, DHCPLease{
			ExpiresAt: expiresAt,
			MAC:       strings.ToLower(mac.String()),
			IPAddress: ip.To4().String(),
			Hostname:  hostname,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return leases, nil
}
