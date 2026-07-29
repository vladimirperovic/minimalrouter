package config

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"sort"
	"strings"
	"time"
)

const maxPfSenseXMLBytes = 8 << 20

// PfSenseConfig represents the XML structure of an exported pfSense config.xml file.
type PfSenseConfig struct {
	XMLName    xml.Name          `xml:"pfsense"`
	Version    string            `xml:"version"`
	System     PfSenseSystem     `xml:"system"`
	Interfaces PfSenseInterfaces `xml:"interfaces"`
	DHCPD      PfSenseDHCPD      `xml:"dhcpd"`
	NAT        PfSenseNAT        `xml:"nat"`
	PPPs       PfSensePPPs       `xml:"ppps"`
	Filter     PfSenseFilter     `xml:"filter"`
	Aliases    PfSenseAliases    `xml:"aliases"`
	VLANs      PfSenseVLANs      `xml:"vlans"`
}

type PfSenseSystem struct {
	Hostname string `xml:"hostname"`
	Domain   string `xml:"domain"`
}

type PfSenseInterfaces struct {
	WAN PfSenseInterface `xml:"wan"`
	LAN PfSenseInterface `xml:"lan"`
}

type PfSenseInterface struct {
	If     string `xml:"if"`
	IPAddr string `xml:"ipaddr"`
	Subnet string `xml:"subnet"`
}

type PfSensePPPs struct {
	PPP []PfSensePPP `xml:"ppp"`
}

type PfSensePPP struct {
	Username string `xml:"username"`
	Password string `xml:"password"`
	If       string `xml:"if"`
}

type PfSenseDHCPD struct {
	LAN PfSenseDHCPInterface `xml:"lan"`
}

type PfSenseDHCPInterface struct {
	Enable    string             `xml:"enable"`
	Range     PfSenseDHCPRange   `xml:"range"`
	StaticMap []PfSenseStaticMap `xml:"staticmap"`
}

type PfSenseDHCPRange struct {
	From string `xml:"from"`
	To   string `xml:"to"`
}

type PfSenseStaticMap struct {
	MAC      string `xml:"mac"`
	IPAddr   string `xml:"ipaddr"`
	Hostname string `xml:"hostname"`
}

type PfSenseNAT struct {
	Rules []PfSenseNATRule `xml:"rule"`
}

type PfSenseNATRule struct {
	Descr        string             `xml:"descr"`
	Interface    string             `xml:"interface"`
	Protocol     string             `xml:"protocol"`
	ExternalPort string             `xml:"external-port"`
	Destination  PfSenseNATEndpoint `xml:"destination"`
	Target       string             `xml:"target"`
	LocalPort    string             `xml:"local-port"`
	Disabled     string             `xml:"disabled"`
}

type PfSenseNATEndpoint struct {
	Port string `xml:"port"`
}

type PfSenseFilter struct {
	Rules []struct{} `xml:"rule"`
}

type PfSenseAliases struct {
	Aliases []struct{} `xml:"alias"`
}

type PfSenseVLANs struct {
	VLANs []struct{} `xml:"vlan"`
}

// PfSenseInterfaceMapping is mandatory because pfSense/FreeBSD names such as
// em0 or igb1 are not stable Linux interface identifiers on the target.
type PfSenseInterfaceMapping struct {
	WAN string `json:"wan"`
	LAN string `json:"lan"`
}

type PfSenseImportReport struct {
	Config              SystemConfig   `json:"config"`
	SourceVersion       string         `json:"source_version,omitempty"`
	Warnings            []string       `json:"warnings"`
	UnsupportedSections []string       `json:"unsupported_sections"`
	Imported            map[string]int `json:"imported"`
}

// ImportPfSenseXML parses a pfSense config.xml using an explicit target
// interface mapping. Unsupported settings are reported rather than silently
// pretending that the migration is complete.
func ImportPfSenseXMLWithMapping(xmlContent []byte, mapping PfSenseInterfaceMapping) (PfSenseImportReport, error) {
	if len(xmlContent) == 0 || len(xmlContent) > maxPfSenseXMLBytes {
		return PfSenseImportReport{}, fmt.Errorf("pfSense XML must be between 1 byte and %d bytes", maxPfSenseXMLBytes)
	}
	if !validInterfaceName(mapping.WAN) || !validInterfaceName(mapping.LAN) || mapping.WAN == mapping.LAN {
		return PfSenseImportReport{}, fmt.Errorf("valid, distinct target WAN and LAN interfaces are required")
	}

	var pf PfSenseConfig
	decoder := xml.NewDecoder(io.LimitReader(bytes.NewReader(xmlContent), maxPfSenseXMLBytes+1))
	decoder.Strict = true
	if err := decoder.Decode(&pf); err != nil {
		return PfSenseImportReport{}, fmt.Errorf("failed to parse pfSense XML: %w", err)
	}
	if pf.XMLName.Local != "pfsense" {
		return PfSenseImportReport{}, fmt.Errorf("document root must be <pfsense>")
	}

	cfg := DefaultConfig()
	cfg.UpdatedAt = time.Now()
	cfg.WAN.Interface = mapping.WAN
	cfg.LAN.Interface = mapping.LAN
	report := PfSenseImportReport{
		SourceVersion:       pf.Version,
		Warnings:            []string{},
		UnsupportedSections: []string{},
		Imported:            map[string]int{},
	}

	// 1. System Metadata
	if pf.System.Hostname != "" {
		cfg.System.Hostname = pf.System.Hostname
	}
	if pf.System.Domain != "" {
		cfg.System.Domain = pf.System.Domain
	}

	// 2. Interfaces. Source identifiers are informational only; explicit target
	// mappings above prevent a FreeBSD device name from being applied to Linux.
	if pf.Interfaces.LAN.IPAddr != "" {
		cfg.LAN.IPAddress = pf.Interfaces.LAN.IPAddr
		subnet := pf.Interfaces.LAN.Subnet
		if subnet == "" {
			subnet = "24"
		}
		cfg.LAN.CIDR = fmt.Sprintf("%s/%s", pf.Interfaces.LAN.IPAddr, subnet)
		var prefix int
		if _, err := fmt.Sscanf(subnet, "%d", &prefix); err == nil && prefix >= 0 && prefix <= 32 {
			cfg.LAN.Netmask = net.IP(net.CIDRMask(prefix, 32)).String()
		}
	}

	// 3. PPPoE Credentials
	if len(pf.PPPs.PPP) > 0 {
		cfg.WAN.Enabled = true
		cfg.WAN.Username = pf.PPPs.PPP[0].Username
		cfg.WAN.Password = pf.PPPs.PPP[0].Password
		report.Imported["pppoe_accounts"] = 1
		if len(pf.PPPs.PPP) > 1 {
			report.Warnings = append(report.Warnings, "Only the first pfSense PPP connection was imported.")
			report.UnsupportedSections = append(report.UnsupportedSections, "additional PPP connections")
		}
	}

	// 4. DHCP Server & Static Leases
	if pf.DHCPD.LAN.Enable != "" || pf.DHCPD.LAN.Range.From != "" {
		cfg.DHCP.Enabled = true
		cfg.DHCP.RangeStart = pf.DHCPD.LAN.Range.From
		cfg.DHCP.RangeEnd = pf.DHCPD.LAN.Range.To

		var staticLeases []StaticLease
		for i, sm := range pf.DHCPD.LAN.StaticMap {
			staticLeases = append(staticLeases, StaticLease{
				ID:        fmt.Sprintf("pf-static-%d", i+1),
				Hostname:  sm.Hostname,
				MAC:       sm.MAC,
				IPAddress: sm.IPAddr,
			})
		}
		cfg.DHCP.StaticLeases = staticLeases
		report.Imported["dhcp_static_leases"] = len(staticLeases)
	}

	// 5. Port Forwarding Rules
	var portForwards []PortForwardRule
	for i, r := range pf.NAT.Rules {
		if r.Disabled != "" {
			continue
		}
		var extPort, intPort int
		externalPort := r.ExternalPort
		if externalPort == "" {
			externalPort = r.Destination.Port
		}
		fmt.Sscanf(externalPort, "%d", &extPort)
		fmt.Sscanf(r.LocalPort, "%d", &intPort)

		if r.Interface != "" && r.Interface != "wan" {
			report.Warnings = append(report.Warnings, fmt.Sprintf("Skipped NAT rule %q because only WAN port forwards are supported.", r.Descr))
			continue
		}
		if strings.Contains(externalPort, "-") || strings.Contains(r.LocalPort, "-") {
			report.Warnings = append(report.Warnings, fmt.Sprintf("Skipped NAT rule %q because port ranges are not supported.", r.Descr))
			continue
		}
		if extPort > 0 && intPort == 0 {
			intPort = extPort
		}
		if extPort > 0 && intPort > 0 && r.Target != "" {
			proto := strings.ToLower(r.Protocol)
			if proto == "" {
				proto = "tcp"
			}
			name := r.Descr
			if name == "" {
				name = fmt.Sprintf("Rule %d", i+1)
			}
			portForwards = append(portForwards, PortForwardRule{
				ID:           fmt.Sprintf("pf-nat-%d", i+1),
				Name:         name,
				Protocol:     proto,
				ExternalPort: extPort,
				InternalIP:   r.Target,
				InternalPort: intPort,
				Enabled:      false,
			})
			report.Warnings = append(report.Warnings, fmt.Sprintf("Imported NAT rule %q as disabled because WireGuard is the only permitted WAN entry point.", name))
		} else {
			report.Warnings = append(report.Warnings, fmt.Sprintf("Skipped NAT rule %q because its target or ports could not be represented.", r.Descr))
		}
	}
	cfg.Firewall.PortForwards = portForwards
	report.Imported["port_forwards"] = len(portForwards)

	if len(pf.Filter.Rules) > 0 {
		report.UnsupportedSections = append(report.UnsupportedSections, "firewall filter rules")
		report.Warnings = append(report.Warnings, fmt.Sprintf("%d pfSense firewall rules require manual review and were not imported.", len(pf.Filter.Rules)))
	}
	if len(pf.Aliases.Aliases) > 0 {
		report.UnsupportedSections = append(report.UnsupportedSections, "aliases")
		report.Warnings = append(report.Warnings, fmt.Sprintf("%d pfSense aliases require manual review and were not imported.", len(pf.Aliases.Aliases)))
	}
	if len(pf.VLANs.VLANs) > 0 {
		report.UnsupportedSections = append(report.UnsupportedSections, "VLANs")
		report.Warnings = append(report.Warnings, fmt.Sprintf("%d pfSense VLANs require manual interface design and were not imported.", len(pf.VLANs.VLANs)))
	}
	sort.Strings(report.UnsupportedSections)

	// Validate imported configuration
	if err := cfg.Validate(); err != nil {
		report.Config = cfg
		return report, fmt.Errorf("imported configuration is not safe to apply: %w", err)
	}

	report.Config = cfg
	return report, nil
}

// ImportPfSenseXML is retained for library callers, but intentionally uses the
// safe Linux defaults instead of copying pfSense interface names.
func ImportPfSenseXML(xmlContent []byte) (SystemConfig, error) {
	report, err := ImportPfSenseXMLWithMapping(xmlContent, PfSenseInterfaceMapping{WAN: "eth0", LAN: "eth1"})
	return report.Config, err
}
