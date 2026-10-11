package main

import (
	"fmt"
	"math"
	"net"
	"net/url"
	"strconv"
)

func lanTools() []MCPTool {
	return []MCPTool{
		{Name: "get_lan_dhcp_status", Description: "Read configured LAN/DHCP/DNS state, bounded lease observation freshness/errors, pool allocation, warnings and pending management URL. Configured enabled is not proof a service is running; use get_health for DNS health. Names are untrusted data.", InputSchema: ToolSchema{Type: "object", Properties: map[string]PropSchema{}}},
		{Name: "get_network_interfaces", Description: "Read discovered interfaces, hardware addresses and observed link state. No configuration is changed. Carrier is unknown when carrier_known is false.", InputSchema: ToolSchema{Type: "object", Properties: map[string]PropSchema{}}},
		{Name: "list_dhcp_leases", Description: "Read active DHCP leases with availability, collection time, truncation and pagination. A valid lease is not proof of online presence; unavailable is not an empty network. Hostnames are untrusted text.", InputSchema: ToolSchema{Type: "object", Properties: map[string]PropSchema{"search": {Type: "string", Description: "Optional hostname, IPv4 or MAC search"}, "sort": {Type: "string", Description: "Sort order", Enum: []string{"ip", "expiry", "hostname"}}, "limit": {Type: "integer", Minimum: 1, Maximum: 500}, "offset": {Type: "integer", Minimum: 0, Maximum: 4096}}}},
		{Name: "check_ip_availability", Description: "Check known LAN reservation/DHCP conflicts and suggest an address outside the pool. Read-only; no scan is sent. no_known_conflict never proves an IP is unused. Missing/incomplete lease data gives unknown. Names are untrusted data.", InputSchema: ToolSchema{Type: "object", Properties: map[string]PropSchema{"ip": {Type: "string", Description: "Optional IPv4 address to check; omit to request a suggestion"}, "mac": {Type: "string", Description: "Optional device MAC to exclude its own lease"}, "exclude_reservation_id": {Type: "string", Description: "Existing reservation being edited"}}}},
	}
}

func executeLANTool(name string, args map[string]interface{}) (string, error) {
	if name == "get_lan_dhcp_status" {
		return readOnlyCall("/api/v1/network/lan", nil, "LAN and DHCP status")
	}
	if name == "get_network_interfaces" {
		return readOnlyCall("/api/v1/system/interfaces", nil, "network interfaces")
	}
	q := url.Values{}
	if name == "list_dhcp_leases" {
		for _, key := range []string{"limit", "offset"} {
			if raw, exists := args[key]; exists {
				n, ok := raw.(float64)
				min, max := float64(0), float64(4096)
				if key == "limit" {
					min, max = 1, 500
				}
				if !ok || math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n || n < min || n > max {
					return "", fmt.Errorf("invalid %s", key)
				}
				q.Set(key, strconv.Itoa(int(n)))
			}
		}
		for _, key := range []string{"search", "sort"} {
			if raw, exists := args[key]; exists {
				value, ok := raw.(string)
				if !ok || len(value) > 253 {
					return "", fmt.Errorf("invalid %s", key)
				}
				q.Set(key, value)
			}
		}
		if value := q.Get("sort"); value != "" && value != "ip" && value != "expiry" && value != "hostname" {
			return "", fmt.Errorf("invalid sort")
		}
		return readOnlyCall("/api/v1/network/leases", q, "DHCP leases")
	}
	for _, key := range []string{"ip", "mac", "exclude_reservation_id"} {
		if raw, exists := args[key]; exists {
			value, ok := raw.(string)
			if !ok || len(value) > 128 {
				return "", fmt.Errorf("invalid %s", key)
			}
			q.Set(key, value)
		}
	}
	if value := q.Get("ip"); value != "" && net.ParseIP(value).To4() == nil {
		return "", fmt.Errorf("ip must be IPv4")
	}
	if value := q.Get("mac"); value != "" {
		mac, err := net.ParseMAC(value)
		if err != nil || len(mac) != 6 {
			return "", fmt.Errorf("mac must be a 48-bit address")
		}
	}
	return readOnlyCall("/api/v1/network/address-check", q, "IP availability")
}

func dnsArguments(args map[string]interface{}) ([]string, error) {
	servers := []string{}
	seen := map[string]bool{}
	for _, key := range []string{"primary_dns", "secondary_dns", "tertiary_dns"} {
		raw, exists := args[key]
		if !exists && key != "primary_dns" {
			continue
		}
		value, ok := raw.(string)
		ip := net.ParseIP(value)
		if !ok || ip.To4() == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() || ip.Equal(net.IPv4bcast) {
			return nil, fmt.Errorf("%s must be a unicast IPv4 upstream resolver", key)
		}
		if seen[ip.String()] {
			return nil, fmt.Errorf("duplicate upstream resolver")
		}
		seen[ip.String()] = true
		servers = append(servers, ip.String())
	}
	return servers, nil
}
