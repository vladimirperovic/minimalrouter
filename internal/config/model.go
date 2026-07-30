package config

import (
	"time"
)

// Revision represents an optimistic concurrency token for config changes.
type Revision uint64

// SystemConfig represents the complete canonical configuration model.
type SystemConfig struct {
	Revision   Revision           `json:"revision"`
	UpdatedAt  time.Time          `json:"updated_at"`
	System     SystemSettings     `json:"system"`
	WAN        WANSettings        `json:"wan"`
	LAN        LANSettings        `json:"lan"`
	DHCP       DHCPSettings       `json:"dhcp"`
	Firewall   FirewallConfig     `json:"firewall"`
	WireGuard  WireGuardConfig    `json:"wireguard"`
	Cloudflare CloudflareConfig   `json:"cloudflare"`
	SquidProxy SquidProxyConfig   `json:"squid_proxy"`
	AdGuard    AdGuardConfig      `json:"adguard"`
	QoS        QoSConfig          `json:"qos"`
	WiFi       WiFiConfig         `json:"wifi"`
	IoT        IoTConfig          `json:"iot"`
	Policies   DevicePolicyConfig `json:"device_policies"`
}

type WireGuardConfig struct {
	Enabled    bool            `json:"enabled"`
	Interface  string          `json:"interface"`
	PrivateKey string          `json:"private_key,omitempty"`
	ListenPort int             `json:"listen_port"`
	Address    string          `json:"address"`
	Peers      []WireGuardPeer `json:"peers"`
}

type WireGuardPeer struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	PublicKey    string   `json:"public_key"`
	PresharedKey string   `json:"preshared_key,omitempty"`
	AllowedIPs   []string `json:"allowed_ips"`
	Endpoint     string   `json:"endpoint,omitempty"`
	Enabled      bool     `json:"enabled"`
}

type CloudflareConfig struct {
	DDNSEnabled   bool   `json:"ddns_enabled"`
	APIToken      string `json:"api_token,omitempty"`
	ZoneID        string `json:"zone_id,omitempty"` // Legacy field retained for import compatibility
	ZoneName      string `json:"zone_name,omitempty"`
	Domain        string `json:"domain"`
	TunnelEnabled bool   `json:"tunnel_enabled"`
	TunnelToken   string `json:"tunnel_token,omitempty"`
}

// WiFiConfig holds hostapd Wi-Fi Access Point configuration settings.
type WiFiConfig struct {
	Enabled    bool   `json:"enabled"`
	Interface  string `json:"interface"`            // e.g. "wlan0"
	SSID       string `json:"ssid"`                 // e.g. "MinimalRouter-Home"
	Passphrase string `json:"passphrase,omitempty"` // WPA2/WPA3 WPA passphrase
	Band       string `json:"band"`                 // "2.4ghz" or "5ghz"
	Channel    int    `json:"channel"`              // e.g. 6 or 36
	HideSSID   bool   `json:"hide_ssid"`
}

const WiFiBridgeInterface = "br-lan"

// RuntimeLANInterface returns the interface that owns the LAN address and
// receives LAN firewall/DHCP policy. Wi-Fi clients join the same LAN through a
// bridge instead of being placed on an unprotected parallel subnet.
func (c SystemConfig) RuntimeLANInterface() string {
	if c.WiFi.Enabled {
		return WiFiBridgeInterface
	}
	return c.LAN.Interface
}

const IoTVLANInterface = "mr-iot"

// IoTConfig defines an optional isolated IPv4 zone. The zone may use a
// dedicated physical interface or one explicitly configured 802.1Q VLAN.
type IoTConfig struct {
	Enabled         bool            `json:"enabled"`
	Mode            string          `json:"mode"` // dedicated or vlan
	Interface       string          `json:"interface"`
	ParentInterface string          `json:"parent_interface,omitempty"`
	VLANID          int             `json:"vlan_id,omitempty"`
	IPAddress       string          `json:"ip_address"`
	Netmask         string          `json:"netmask"`
	CIDR            string          `json:"cidr"`
	DHCP            IoTDHCPSettings `json:"dhcp"`
}

// IoTDHCPSettings contains the DHCP pool owned by the isolated IoT zone.
type IoTDHCPSettings struct {
	Enabled      bool          `json:"enabled"`
	RangeStart   string        `json:"range_start"`
	RangeEnd     string        `json:"range_end"`
	LeaseTime    string        `json:"lease_time"`
	StaticLeases []StaticLease `json:"static_leases"`
}

// DevicePolicyConfig applies time-bounded Internet policies to devices with
// stable DHCP reservations. Unassigned devices retain the normal zone policy.
type DevicePolicyConfig struct {
	Enabled     bool               `json:"enabled"`
	Profiles    []DeviceProfile    `json:"profiles"`
	Assignments []DeviceAssignment `json:"assignments"`
}

// DeviceProfile describes when a device may forward traffic and whether the
// permitted window allows all Internet traffic or only named service groups.
type DeviceProfile struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Enabled         bool           `json:"enabled"`
	AccessMode      string         `json:"access_mode"` // allow_all or allow_services
	AllowedServices []string       `json:"allowed_services"`
	Windows         []AccessWindow `json:"windows"`
}

// AccessWindow is evaluated using the configured appliance timezone. A full-day
// window ignores Start and End. Otherwise the interval must not cross midnight.
type AccessWindow struct {
	Days   []string `json:"days"`
	Start  string   `json:"start,omitempty"`
	End    string   `json:"end,omitempty"`
	AllDay bool     `json:"all_day"`
}

// DeviceAssignment binds a profile to a stable DHCP reservation. Zone must be
// lan or iot. Matching both IP and MAC prevents accidental policy drift.
type DeviceAssignment struct {
	ID        string `json:"id"`
	Hostname  string `json:"hostname"`
	MAC       string `json:"mac"`
	IPAddress string `json:"ip_address"`
	Zone      string `json:"zone"`
	ProfileID string `json:"profile_id"`
}

// RuntimeIoTInterface returns the interface owned by the IoT zone. VLAN mode
// uses one fixed project-owned name so generated firewall rules are deterministic.
func (c SystemConfig) RuntimeIoTInterface() string {
	if !c.IoT.Enabled {
		return ""
	}
	if c.IoT.Mode == "vlan" {
		return IoTVLANInterface
	}
	return c.IoT.Interface
}

// EffectiveTimezone preserves compatibility with configurations created before
// timezone-aware schedules were introduced.
func (c SystemConfig) EffectiveTimezone() string {
	if c.System.Timezone == "" {
		return "UTC"
	}
	return c.System.Timezone
}

// QoSConfig holds traffic shaping and CAKE / FQ-CoDel bufferbloat prevention settings.
type QoSConfig struct {
	Enabled           bool   `json:"enabled"`
	Algorithm         string `json:"algorithm"`           // "cake" or "fq_codel"
	DownloadLimitMbps int    `json:"download_limit_mbps"` // e.g. 100
	UploadLimitMbps   int    `json:"upload_limit_mbps"`   // e.g. 20
}

// FilterDeviceRule represents a device subject to per-device content/service blocking (YouTube, TikTok, etc.)
type FilterDeviceRule struct {
	ID              string   `json:"id"`
	Hostname        string   `json:"hostname"`
	IPAddress       string   `json:"ip_address"`
	BlockedServices []string `json:"blocked_services"` // e.g. ["youtube", "tiktok", "facebook", "adult", "gaming"]
	Enabled         bool     `json:"enabled"`
}

// AdGuardConfig holds AdBlock and per-device parental content filtering settings.
type AdGuardConfig struct {
	Enabled       bool               `json:"enabled"`
	BlocklistURL  string             `json:"blocklist_url"`
	LastUpdated   string             `json:"last_updated"`
	FilterDevices []FilterDeviceRule `json:"filter_devices"`
}

// RestrictedIPItem represents a device IP in the Restricted Alias list with a toggle state.
type RestrictedIPItem struct {
	Hostname  string `json:"hostname"`
	IPAddress string `json:"ip_address"`
	Enabled   bool   `json:"enabled"`
}

// SquidProxyConfig holds non-caching HTTP/HTTPS proxy configuration.
type SquidProxyConfig struct {
	Enabled       bool               `json:"enabled"`
	Port          int                `json:"port"`               // e.g. 3128
	Username      string             `json:"username"`           // Proxy auth username
	Password      string             `json:"password,omitempty"` // Proxy auth password
	RestrictedIPs []RestrictedIPItem `json:"restricted_ips"`     // IP Alias list with enabled toggle
}

// SystemSettings holds basic appliance metadata and management settings.
type SystemSettings struct {
	Hostname         string `json:"hostname"`
	Domain           string `json:"domain"`
	Timezone         string `json:"timezone"`
	HTTPSEnabled     bool   `json:"https_enabled"`
	HTTPSPort        int    `json:"https_port"`
	ManagementAccess string `json:"management_access"` // lan_and_wireguard or wireguard_only
}

// WANSettings holds PPPoE internet connection configuration.
type WANSettings struct {
	Interface  string `json:"interface"` // e.g. "eth0"
	Enabled    bool   `json:"enabled"`
	Username   string `json:"username"`
	Password   string `json:"password,omitempty"` // Omitted in status, set on change
	MTU        int    `json:"mtu"`
	UsePeerDNS bool   `json:"use_peer_dns"`
}

// LANSettings holds local network configuration.
type LANSettings struct {
	Interface string `json:"interface"`  // e.g. "eth1" or "br0"
	IPAddress string `json:"ip_address"` // e.g. "192.168.1.1"
	Netmask   string `json:"netmask"`    // e.g. "255.255.255.0"
	CIDR      string `json:"cidr"`       // e.g. "192.168.1.1/24"
}

// DHCPSettings holds dnsmasq DHCP server configuration and static leases.
type DHCPSettings struct {
	Enabled      bool          `json:"enabled"`
	DNSEnabled   bool          `json:"dns_enabled"` // Enable DNS-over-HTTPS proxy
	RangeStart   string        `json:"range_start"` // e.g. "192.168.1.100"
	RangeEnd     string        `json:"range_end"`   // e.g. "192.168.1.200"
	LeaseTime    string        `json:"lease_time"`  // e.g. "12h"
	DNSServers   []string      `json:"dns_servers"` // e.g. ["1.1.1.1", "8.8.8.8"]
	StaticLeases []StaticLease `json:"static_leases"`
}

// StaticLease assigns a static IP to a specific MAC address.
type StaticLease struct {
	ID        string `json:"id"`
	Hostname  string `json:"hostname"`
	MAC       string `json:"mac"`
	IPAddress string `json:"ip_address"`
}

// FirewallConfig holds packet filtering and NAT port forwarding rules.
type FirewallConfig struct {
	DefaultWANInputPolicy string            `json:"default_wan_input_policy"` // "deny"
	WANIngressMode        string            `json:"wan_ingress_mode"`         // "wireguard_only"
	StatefulFirewall      bool              `json:"stateful_firewall"`
	PortForwards          []PortForwardRule `json:"port_forwards"`
	CustomRules           []FirewallRule    `json:"custom_rules"`
}

// PortForwardRule redirects an incoming WAN port to an internal LAN IP and port.
type PortForwardRule struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Protocol     string `json:"protocol"`      // "tcp", "udp", or "both"
	ExternalPort int    `json:"external_port"` // e.g. 8080
	InternalIP   string `json:"internal_ip"`   // e.g. "192.168.1.50"
	InternalPort int    `json:"internal_port"` // e.g. 80
	Enabled      bool   `json:"enabled"`
}

// FirewallRule represents a custom filtering rule.
type FirewallRule struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Action    string `json:"action"`    // "allow" or "deny"
	Direction string `json:"direction"` // "input" or "forward"
	Protocol  string `json:"protocol"`  // "tcp", "udp", "icmp", "any"
	SrcIP     string `json:"src_ip,omitempty"`
	DstPort   int    `json:"dst_port,omitempty"`
	Enabled   bool   `json:"enabled"`
}

// DefaultConfig returns a secure default Minimal Router configuration.
func DefaultConfig() SystemConfig {
	return SystemConfig{
		Revision:  1,
		UpdatedAt: time.Now(),
		System: SystemSettings{
			Hostname:         "minimalrouter",
			Domain:           "lan",
			Timezone:         "UTC",
			HTTPSEnabled:     true,
			HTTPSPort:        8443,
			ManagementAccess: "lan_and_wireguard",
		},
		WAN: WANSettings{
			Interface:  "eth0",
			Enabled:    false,
			Username:   "",
			Password:   "",
			MTU:        1492,
			UsePeerDNS: true,
		},
		LAN: LANSettings{
			Interface: "eth1",
			IPAddress: "192.168.1.1",
			Netmask:   "255.255.255.0",
			CIDR:      "192.168.1.1/24",
		},
		DHCP: DHCPSettings{
			Enabled:      true,
			RangeStart:   "192.168.1.100",
			RangeEnd:     "192.168.1.200",
			LeaseTime:    "12h",
			DNSServers:   []string{"1.1.1.1", "1.0.0.1"},
			StaticLeases: []StaticLease{},
		},
		Firewall: FirewallConfig{
			DefaultWANInputPolicy: "deny",
			WANIngressMode:        "wireguard_only",
			StatefulFirewall:      true,
			PortForwards:          []PortForwardRule{},
			CustomRules:           []FirewallRule{},
		},
		WireGuard: WireGuardConfig{
			Enabled:    false,
			Interface:  "wg0",
			PrivateKey: "",
			ListenPort: 51820,
			Address:    "10.8.0.1/24",
			Peers:      []WireGuardPeer{},
		},
		Cloudflare: CloudflareConfig{
			DDNSEnabled:   false,
			APIToken:      "",
			ZoneID:        "",
			ZoneName:      "",
			Domain:        "",
			TunnelEnabled: false,
			TunnelToken:   "",
		},
		SquidProxy: SquidProxyConfig{
			Enabled:       false,
			Port:          3128,
			Username:      "proxyadmin",
			Password:      "",
			RestrictedIPs: []RestrictedIPItem{},
		},
		AdGuard: AdGuardConfig{
			Enabled:       false,
			BlocklistURL:  "",
			LastUpdated:   "Never",
			FilterDevices: []FilterDeviceRule{},
		},
		QoS: QoSConfig{
			Enabled:           false,
			Algorithm:         "cake",
			DownloadLimitMbps: 100,
			UploadLimitMbps:   20,
		},
		WiFi: WiFiConfig{
			Enabled:    false,
			Interface:  "wlan0",
			SSID:       "MinimalRouter-Home",
			Passphrase: "",
			Band:       "5ghz",
			Channel:    36,
			HideSSID:   false,
		},
		IoT: IoTConfig{
			Enabled:   false,
			Mode:      "dedicated",
			Interface: "eth2",
			IPAddress: "192.168.30.1",
			Netmask:   "255.255.255.0",
			CIDR:      "192.168.30.1/24",
			DHCP: IoTDHCPSettings{
				Enabled:      true,
				RangeStart:   "192.168.30.100",
				RangeEnd:     "192.168.30.200",
				LeaseTime:    "12h",
				StaticLeases: []StaticLease{},
			},
		},
		Policies: DevicePolicyConfig{
			Enabled:     false,
			Profiles:    []DeviceProfile{},
			Assignments: []DeviceAssignment{},
		},
	}
}
