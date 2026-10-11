package network

import "net"

// IPv4Identity makes equivalent IPv4 spellings comparable without changing
// configuration or accepting invalid input. Validation remains the caller's job.
func IPv4Identity(value string) string {
	ip := net.ParseIP(value).To4()
	if ip == nil {
		return value
	}
	return ip.String()
}

// MACIdentity compares the colon, dash and dotted forms accepted by net.ParseMAC.
func MACIdentity(value string) string {
	mac, err := net.ParseMAC(value)
	if err != nil || len(mac) != 6 {
		return value
	}
	return mac.String()
}
