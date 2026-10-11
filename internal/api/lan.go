package api

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/accounting"
	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/telemetry"
)

type lanPool struct {
	Start          string  `json:"start"`
	End            string  `json:"end"`
	Capacity       uint64  `json:"capacity"`
	ObservedLeases int     `json:"observed_leases"`
	Free           *uint64 `json:"free"`
	Reservations   int     `json:"reservations"`
}

func ipv4Number(value string) (uint32, bool) {
	ip := net.ParseIP(value).To4()
	if ip == nil {
		return 0, false
	}
	return binary.BigEndian.Uint32(ip), true
}

func hostRange(lan config.LANSettings) (uint32, uint32, bool) {
	_, subnet, err := net.ParseCIDR(lan.CIDR)
	if err != nil || subnet.IP.To4() == nil {
		return 0, 0, false
	}
	first := binary.BigEndian.Uint32(subnet.IP.To4())
	last := first | ^binary.BigEndian.Uint32(subnet.Mask)
	return first, last, last > first+1
}

func poolSummary(cfg config.SystemConfig, snapshot telemetry.DHCPLeaseSnapshot) lanPool {
	pool := lanPool{Start: cfg.DHCP.RangeStart, End: cfg.DHCP.RangeEnd, Reservations: len(cfg.DHCP.StaticLeases)}
	start, ok := ipv4Number(pool.Start)
	end, endOK := ipv4Number(pool.End)
	if !ok || !endOK || end < start {
		return pool
	}
	pool.Capacity = uint64(end) - uint64(start) + 1
	seen := map[string]bool{}
	for _, lease := range snapshot.Leases {
		ip, valid := ipv4Number(lease.IPAddress)
		if valid && ip >= start && ip <= end {
			seen[lease.IPAddress] = true
		}
	}
	pool.ObservedLeases = len(seen)
	if snapshot.Available && !snapshot.Truncated && snapshot.RejectedEntries == 0 {
		free := pool.Capacity - uint64(pool.ObservedLeases)
		pool.Free = &free
	}
	return pool
}

func (s *Server) handleLANStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.engine.GetCurrentConfig()
	leases := telemetry.CurrentDHCPLeaseSnapshot()
	warnings := []string{}
	if !leases.Available && cfg.DHCP.Enabled {
		warnings = append(warnings, "DHCP leases are unavailable; allocation usage is unknown.")
	}
	if leases.Truncated || leases.RejectedEntries > 0 {
		warnings = append(warnings, "DHCP observation is incomplete; some addresses may be missing.")
	}
	pool := poolSummary(cfg, leases)
	if cfg.DHCP.Enabled && pool.Free != nil && pool.Capacity > 0 && *pool.Free*10 <= pool.Capacity {
		warnings = append(warnings, "The dynamic DHCP pool is at least 90% allocated.")
	}
	var pending any
	if tx := s.engine.GetPendingTransaction(); tx != nil {
		pending = map[string]any{"id": tx.ID, "state": tx.CurrentState, "confirmation_deadline": tx.ConfirmationDeadline, "management_url": managementURL(tx.Config)}
	}
	writeGatewayJSON(w, http.StatusOK, map[string]any{
		"revision": cfg.Revision, "generated_at": time.Now().UTC(), "lan": cfg.LAN,
		"runtime_interface": cfg.RuntimeLANInterface(), "dhcp_enabled": cfg.DHCP.Enabled,
		"lease_time": cfg.DHCP.LeaseTime, "pool": pool, "lease_status": leases.DHCPLeaseStatus,
		"dns":                 map[string]any{"enabled": true, "encrypted_upstream_supported": false, "client_resolver": cfg.LAN.IPAddress, "upstreams": cfg.DHCP.DNSServers, "local_records": len(cfg.DNS.Records)},
		"pending_transaction": pending, "warnings": warnings,
		"limitations": []string{"A DHCP lease is not proof a device is online.", "Unallocated DHCP addresses may still be used by manually configured devices."},
	})
}

func managementURL(cfg config.SystemConfig) string {
	scheme, port := "http", "8080"
	if cfg.System.HTTPSEnabled {
		scheme, port = "https", strconv.Itoa(cfg.System.HTTPSPort)
	}
	return scheme + "://" + net.JoinHostPort(cfg.LAN.IPAddress, port) + "/#network"
}

func (s *Server) handleNetworkDevices(w http.ResponseWriter, r *http.Request) {
	cfg, now := s.engine.GetCurrentConfig(), time.Now().UTC()
	snapshot := telemetry.CurrentDHCPLeaseSnapshot()
	activity := []accounting.DeviceActivity{}
	available := false
	if store := s.configuredAccountingStore(); store != nil && cfg.Accounting.Enabled {
		var err error
		activity, err = store.DeviceActivity(now)
		available = err == nil
		if err != nil {
			activity = []accounting.DeviceActivity{}
		}
	}
	writeGatewayJSON(w, http.StatusOK, map[string]any{
		"revision": cfg.Revision, "updated_at": now, "lease_status": snapshot.DHCPLeaseStatus,
		"leases": snapshot.Leases, "reservations": cfg.DHCP.StaticLeases,
		"activity": map[string]any{"available": available, "enabled": cfg.Accounting.Enabled, "devices": activity},
	})
}

func (s *Server) handleDHCPLeases(w http.ResponseWriter, r *http.Request) {
	limit, offset := 100, 0
	for key, target := range map[string]*int{"limit": &limit, "offset": &offset} {
		if raw := r.URL.Query().Get(key); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 0 || (key == "limit" && (n < 1 || n > 500)) || (key == "offset" && n > 4096) {
				writeGatewayJSON(w, 400, map[string]string{"error": "Invalid pagination; limit is 1–500, offset is 0–4096."})
				return
			}
			*target = n
		}
	}
	order := r.URL.Query().Get("sort")
	if order != "" && order != "ip" && order != "expiry" && order != "hostname" {
		writeGatewayJSON(w, 400, map[string]string{"error": "sort must be ip, expiry or hostname"})
		return
	}
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("search")))
	if len(query) > 253 {
		writeGatewayJSON(w, 400, map[string]string{"error": "Search is too long"})
		return
	}
	snapshot := telemetry.CurrentDHCPLeaseSnapshot()
	filtered := []telemetry.DHCPLease{}
	for _, lease := range snapshot.Leases {
		if strings.Contains(strings.ToLower(lease.Hostname+" "+lease.MAC+" "+lease.IPAddress), query) {
			filtered = append(filtered, lease)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if order == "expiry" && filtered[i].ExpiresAt != filtered[j].ExpiresAt {
			return filtered[i].ExpiresAt < filtered[j].ExpiresAt
		}
		if order == "hostname" && filtered[i].Hostname != filtered[j].Hostname {
			return filtered[i].Hostname < filtered[j].Hostname
		}
		a, _ := ipv4Number(filtered[i].IPAddress)
		b, _ := ipv4Number(filtered[j].IPAddress)
		return a < b
	})
	total := len(filtered)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	writeGatewayJSON(w, 200, map[string]any{"lease_status": snapshot.DHCPLeaseStatus, "leases": filtered[offset:end], "total": total, "offset": offset, "limit": limit})
}

type addressAssessment struct {
	Revision         config.Revision           `json:"revision"`
	Address          string                    `json:"address"`
	State            string                    `json:"state"`
	Conflicts        []string                  `json:"conflicts"`
	SuggestedAddress string                    `json:"suggested_address,omitempty"`
	LeaseStatus      telemetry.DHCPLeaseStatus `json:"lease_status"`
	Limitation       string                    `json:"limitation"`
}

func assessLANAddress(cfg config.SystemConfig, snapshot telemetry.DHCPLeaseSnapshot, address, mac, excludeID string) addressAssessment {
	result := addressAssessment{Revision: cfg.Revision, Address: address, State: "unknown", Conflicts: []string{}, LeaseStatus: snapshot.DHCPLeaseStatus, Limitation: "Only configured and observed DHCP conflicts are checked. No active scan is performed; an unobserved static device may use this address."}
	first, last, valid := hostRange(cfg.LAN)
	ip, ipOK := ipv4Number(address)
	start, _ := ipv4Number(cfg.DHCP.RangeStart)
	end, _ := ipv4Number(cfg.DHCP.RangeEnd)
	if address != "" {
		if !valid || !ipOK || ip <= first || ip >= last {
			result.Conflicts = append(result.Conflicts, "Choose a usable host address in the LAN subnet.")
		}
		if address == cfg.LAN.IPAddress {
			result.Conflicts = append(result.Conflicts, "This is the router gateway address.")
		}
		if ipOK && ip >= start && ip <= end {
			result.Conflicts = append(result.Conflicts, "This address is inside the dynamic DHCP pool.")
		}
		for _, reservation := range cfg.DHCP.StaticLeases {
			if reservation.ID != excludeID && (reservation.IPAddress == address || (mac != "" && strings.EqualFold(mac, reservation.MAC))) {
				result.Conflicts = append(result.Conflicts, "Address or MAC is already reserved for "+reservation.Hostname+" ("+reservation.MAC+").")
			}
		}
		for _, lease := range snapshot.Leases {
			if lease.IPAddress == address && !strings.EqualFold(mac, lease.MAC) {
				result.Conflicts = append(result.Conflicts, "Address is currently leased to "+lease.MAC+".")
			}
		}
		if len(result.Conflicts) > 0 {
			result.State = "conflict"
		} else if snapshot.Available && !snapshot.Truncated && snapshot.RejectedEntries == 0 {
			result.State = "no_known_conflict"
		}
	}
	// Jump over the pool and inspect at most the number of occupied addresses
	// plus a handful of exclusions, even for large configured subnets.
	occupied := map[uint32]bool{}
	for _, reservation := range cfg.DHCP.StaticLeases {
		n, ok := ipv4Number(reservation.IPAddress)
		if ok {
			occupied[n] = true
		}
	}
	for _, lease := range snapshot.Leases {
		n, ok := ipv4Number(lease.IPAddress)
		if ok {
			occupied[n] = true
		}
	}
	gateway, _ := ipv4Number(cfg.LAN.IPAddress)
	occupied[gateway] = true
	for _, record := range cfg.DNS.Records {
		n, ok := ipv4Number(record.IP)
		if ok {
			occupied[n] = true
		}
	}
	if valid {
		for candidate, checks := uint64(first)+1, 0; candidate < uint64(last) && checks <= len(occupied)+2; candidate, checks = candidate+1, checks+1 {
			if candidate >= uint64(start) && candidate <= uint64(end) {
				candidate = uint64(end) + 1
				if candidate >= uint64(last) {
					break
				}
			}
			if !occupied[uint32(candidate)] {
				result.SuggestedAddress = fmt.Sprintf("%d.%d.%d.%d", candidate>>24, (candidate>>16)&255, (candidate>>8)&255, candidate&255)
				break
			}
		}
	}
	return result
}

func (s *Server) handleLANAddressCheck(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	address, mac, id := q.Get("ip"), q.Get("mac"), q.Get("exclude_reservation_id")
	if address != "" {
		if _, ok := ipv4Number(address); !ok {
			writeGatewayJSON(w, 400, map[string]string{"error": "ip must be IPv4"})
			return
		}
	}
	if mac != "" {
		parsed, err := net.ParseMAC(mac)
		if err != nil || len(parsed) != 6 {
			writeGatewayJSON(w, 400, map[string]string{"error": "mac must be a 48-bit address"})
			return
		}
		mac = parsed.String()
	}
	if len(id) > 128 {
		writeGatewayJSON(w, 400, map[string]string{"error": "Reservation ID is too long"})
		return
	}
	writeGatewayJSON(w, 200, assessLANAddress(s.engine.GetCurrentConfig(), telemetry.CurrentDHCPLeaseSnapshot(), address, mac, id))
}
