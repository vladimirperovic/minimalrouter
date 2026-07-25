package config

import (
	"fmt"
	"net"
	"strings"
)

// ValidationError contains field-specific error messages.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// ValidationErrors is a list of ValidationErrors implementing error interface.
type ValidationErrors []ValidationError

func (ve ValidationErrors) Error() string {
	var msgs []string
	for _, err := range ve {
		msgs = append(msgs, err.Error())
	}
	return strings.Join(msgs, "; ")
}

// Validate checks the complete SystemConfig for syntax and cross-field invariant errors.
func (c *SystemConfig) Validate() error {
	var errs ValidationErrors

	// Interface boundary check
	if c.WAN.Interface != "" && c.WAN.Interface == c.LAN.Interface {
		errs = append(errs, ValidationError{
			Field:   "wan.interface",
			Message: "WAN interface cannot be the same as LAN interface",
		})
	}

	// LAN Validation
	lanIP := net.ParseIP(c.LAN.IPAddress)
	if lanIP == nil || lanIP.To4() == nil {
		errs = append(errs, ValidationError{
			Field:   "lan.ip_address",
			Message: "Must be a valid IPv4 address",
		})
	}

	if c.LAN.CIDR != "" {
		_, _, err := net.ParseCIDR(c.LAN.CIDR)
		if err != nil {
			errs = append(errs, ValidationError{
				Field:   "lan.cidr",
				Message: "Must be a valid CIDR notation (e.g. 192.168.1.1/24)",
			})
		}
	}

	// DHCP Validation
	if c.DHCP.Enabled {
		startIP := net.ParseIP(c.DHCP.RangeStart)
		if startIP == nil || startIP.To4() == nil {
			errs = append(errs, ValidationError{
				Field:   "dhcp.range_start",
				Message: "Must be a valid IPv4 address",
			})
		}

		endIP := net.ParseIP(c.DHCP.RangeEnd)
		if endIP == nil || endIP.To4() == nil {
			errs = append(errs, ValidationError{
				Field:   "dhcp.range_end",
				Message: "Must be a valid IPv4 address",
			})
		}

		if lanIP != nil && (c.DHCP.RangeStart == c.LAN.IPAddress || c.DHCP.RangeEnd == c.LAN.IPAddress) {
			errs = append(errs, ValidationError{
				Field:   "dhcp.range",
				Message: "DHCP range cannot contain the LAN gateway IP address",
			})
		}
	}

	// Port Forwards Validation
	for i, pf := range c.Firewall.PortForwards {
		if pf.ExternalPort < 1 || pf.ExternalPort > 65535 {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("firewall.port_forwards[%d].external_port", i),
				Message: "Port must be between 1 and 65535",
			})
		}
		if pf.InternalPort < 1 || pf.InternalPort > 65535 {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("firewall.port_forwards[%d].internal_port", i),
				Message: "Port must be between 1 and 65535",
			})
		}
		targetIP := net.ParseIP(pf.InternalIP)
		if targetIP == nil || targetIP.To4() == nil {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("firewall.port_forwards[%d].internal_ip", i),
				Message: "Must be a valid internal IPv4 address",
			})
		}
	}

	// QoS Validation
	if c.QoS.Enabled {
		if c.QoS.DownloadLimitMbps <= 0 {
			errs = append(errs, ValidationError{
				Field:   "qos.download_limit_mbps",
				Message: "Download speed limit must be greater than 0 Mbps when QoS is enabled",
			})
		}
		if c.QoS.UploadLimitMbps <= 0 {
			errs = append(errs, ValidationError{
				Field:   "qos.upload_limit_mbps",
				Message: "Upload speed limit must be greater than 0 Mbps when QoS is enabled",
			})
		}
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}
