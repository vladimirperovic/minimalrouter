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
