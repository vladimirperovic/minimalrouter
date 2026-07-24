package config

import (
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

// PfSenseConfig represents the XML structure of an exported pfSense config.xml file.
type PfSenseConfig struct {
	XMLName    xml.Name          `xml:"pfsense"`
	System     PfSenseSystem     `xml:"system"`
	Interfaces PfSenseInterfaces `xml:"interfaces"`
	DHCPD      PfSenseDHCPD      `xml:"dhcpd"`
	NAT        PfSenseNAT        `xml:"nat"`
	PPPs       PfSensePPPs       `xml:"ppps"`
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
	Descr        string `xml:"descr"`
	Protocol     string `xml:"protocol"`
	ExternalPort string `xml:"external-port"`
	Target       string `xml:"target"`
	LocalPort    string `xml:"local-port"`
	Disabled     string `xml:"disabled"`
}

// ImportPfSenseXML parses raw pfSense XML content and converts it into a Minimal Router SystemConfig.
func ImportPfSenseXML(xmlContent []byte) (SystemConfig, error) {
	var pf PfSenseConfig
	if err := xml.Unmarshal(xmlContent, &pf); err != nil {
		return SystemConfig{}, fmt.Errorf("failed to parse pfSense XML: %w", err)
	}

	cfg := DefaultConfig()
	cfg.UpdatedAt = time.Now()

	// 1. System Metadata
	if pf.System.Hostname != "" {
		cfg.System.Hostname = pf.System.Hostname
	}
	if pf.System.Domain != "" {
		cfg.System.Domain = pf.System.Domain
	}

	// 2. Interfaces
	if pf.Interfaces.WAN.If != "" {
		cfg.WAN.Interface = pf.Interfaces.WAN.If
	}
	if pf.Interfaces.LAN.If != "" {
		cfg.LAN.Interface = pf.Interfaces.LAN.If
	}
	if pf.Interfaces.LAN.IPAddr != "" {
		cfg.LAN.IPAddress = pf.Interfaces.LAN.IPAddr
		subnet := pf.Interfaces.LAN.Subnet
		if subnet == "" {
			subnet = "24"
		}
		cfg.LAN.CIDR = fmt.Sprintf("%s/%s", pf.Interfaces.LAN.IPAddr, subnet)
	}

	// 3. PPPoE Credentials
	if len(pf.PPPs.PPP) > 0 {
		cfg.WAN.Username = pf.PPPs.PPP[0].Username
		cfg.WAN.Password = pf.PPPs.PPP[0].Password
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
	}

	// 5. Port Forwarding Rules
	var portForwards []PortForwardRule
	for i, r := range pf.NAT.Rules {
		if r.Disabled != "" {
			continue
		}
		var extPort, intPort int
		fmt.Sscanf(r.ExternalPort, "%d", &extPort)
		fmt.Sscanf(r.LocalPort, "%d", &intPort)

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
				Enabled:      true,
			})
		}
	}
	cfg.Firewall.PortForwards = portForwards

	// Validate imported configuration
	if err := cfg.Validate(); err != nil {
		return cfg, fmt.Errorf("imported configuration validation warning: %w", err)
	}

	return cfg, nil
}
