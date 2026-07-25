package config

import (
	"testing"
)

func TestImportPfSenseXML(t *testing.T) {
	sampleXML := `<?xml version="1.0"?>
<pfsense>
	<system>
		<hostname>router-pfsense</hostname>
		<domain>home.arpa</domain>
	</system>
	<interfaces>
		<wan>
			<if>em0</if>
			<ipaddr>pppoe</ipaddr>
		</wan>
		<lan>
			<if>em1</if>
			<ipaddr>192.168.10.1</ipaddr>
			<subnet>24</subnet>
		</lan>
	</interfaces>
	<ppps>
		<ppp>
			<username>user@myisp.net</username>
			<password>secret1234567890</password>
			<if>em0</if>
		</ppp>
	</ppps>
	<dhcpd>
		<lan>
			<enable></enable>
			<range>
				<from>192.168.10.100</from>
				<to>192.168.10.200</to>
			</range>
			<staticmap>
				<mac>00:11:22:33:44:55</mac>
				<ipaddr>192.168.10.50</ipaddr>
				<hostname>NAS</hostname>
			</staticmap>
		</lan>
	</dhcpd>
	<nat>
		<rule>
			<descr>Web Server</descr>
			<protocol>tcp</protocol>
			<external-port>8080</external-port>
			<target>192.168.10.50</target>
			<local-port>80</local-port>
		</rule>
	</nat>
</pfsense>`

	cfg, err := ImportPfSenseXML([]byte(sampleXML))
	if err != nil {
		t.Fatalf("ImportPfSenseXML failed: %v", err)
	}

	if cfg.System.Hostname != "router-pfsense" {
		t.Errorf("Expected hostname router-pfsense, got: %s", cfg.System.Hostname)
	}
	if cfg.WAN.Username != "user@myisp.net" {
		t.Errorf("Expected PPPoE username user@myisp.net, got: %s", cfg.WAN.Username)
	}
	if cfg.LAN.IPAddress != "192.168.10.1" {
		t.Errorf("Expected LAN IP 192.168.10.1, got: %s", cfg.LAN.IPAddress)
	}
	if len(cfg.DHCP.StaticLeases) != 1 || cfg.DHCP.StaticLeases[0].Hostname != "NAS" {
		t.Errorf("Expected static lease NAS")
	}
	if len(cfg.Firewall.PortForwards) != 1 || cfg.Firewall.PortForwards[0].ExternalPort != 8080 {
		t.Errorf("Expected port forward rule 8080")
	}
}

func TestImportPfSenseRequiresExplicitSafeMappingAndReportsUnsupported(t *testing.T) {
	xmlData := []byte(`<pfsense>
		<version>24.11</version>
		<interfaces><wan><if>igb0</if></wan><lan><if>igb1</if><ipaddr>10.20.0.1</ipaddr><subnet>24</subnet></lan></interfaces>
		<ppps><ppp><username>isp-user</username><password>isp-password</password></ppp></ppps>
		<dhcpd><lan><enable/><range><from>10.20.0.20</from><to>10.20.0.200</to></range></lan></dhcpd>
		<nat><rule><descr>HTTPS service</descr><interface>wan</interface><protocol>tcp</protocol>
			<destination><port>443</port></destination><target>10.20.0.50</target><local-port>8443</local-port>
		</rule></nat>
		<filter><rule/></filter><aliases><alias/></aliases><vlans><vlan/></vlans>
	</pfsense>`)

	report, err := ImportPfSenseXMLWithMapping(xmlData, PfSenseInterfaceMapping{WAN: "enp1s0", LAN: "enp2s0"})
	if err != nil {
		t.Fatalf("ImportPfSenseXMLWithMapping failed: %v", err)
	}
	if report.Config.WAN.Interface != "enp1s0" || report.Config.LAN.Interface != "enp2s0" {
		t.Fatal("source FreeBSD interface names were not replaced by explicit Linux mappings")
	}
	if len(report.Config.Firewall.PortForwards) != 1 || report.Config.Firewall.PortForwards[0].ExternalPort != 443 {
		t.Fatal("nested pfSense destination port was not imported")
	}
	if len(report.UnsupportedSections) != 3 {
		t.Fatalf("expected explicit warnings for filters, aliases, and VLANs; got %v", report.UnsupportedSections)
	}

	if _, err := ImportPfSenseXMLWithMapping(xmlData, PfSenseInterfaceMapping{WAN: "eth0", LAN: "eth0"}); err == nil {
		t.Fatal("duplicate target interface mapping was accepted")
	}
}
