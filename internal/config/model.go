package config

import (
	"slices"
	"time"
)

// Revision represents an optimistic concurrency token for config changes.
type Revision uint64

// SystemConfig represents the complete canonical configuration model.
type SystemConfig struct {
	Revision  Revision        `json:"revision"`
	UpdatedAt time.Time       `json:"updated_at"`
	System    SystemSettings  `json:"system"`
	WAN       WANSettings     `json:"wan"`
	LAN       LANSettings     `json:"lan"`
	DHCP      DHCPSettings    `json:"dhcp"`
	DNS       DNSSettings     `json:"dns"`
	Firewall  FirewallConfig  `json:"firewall"`
	WireGuard WireGuardConfig `json:"wireguard"`
	// WGClient is the outbound WireGuard tunnel (client mode) used to reach
	// remote sites such as an office network. Unlike WireGuard (server, wg0)
	// the remote peer initiates nothing: nftables only accepts established
	// traffic from this interface, so the remote site can never open a
	// connection back into the home network.
	WGClient   WGClientConfig   `json:"wg_client"`
	Cloudflare CloudflareConfig `json:"cloudflare"`
	SquidProxy SquidProxyConfig `json:"squid_proxy"`
	// AdGuard is the retained JSON compatibility key. The user-facing feature
	// is named DNS Filter and does not embed or impersonate AdGuard Home.
	AdGuard DNSFilterConfig `json:"adguard"`
	QoS     QoSConfig       `json:"qos"`
	WiFi    WiFiConfig      `json:"wifi"`
	// Accounting is per-device byte counting in the forward chain. It is
	// opt-in because it adds two dynamic nftables sets and a periodic read.
	Accounting AccountingConfig `json:"accounting"`
	// TrustedNetworks restricts administrative Web UI/API access to clients
	// whose source address falls within one of the listed CIDR networks.
	// Localhost (127.0.0.1, ::1) is always trusted. It does not replace
	// authentication; both layers are enforced.
	TrustedNetworks []string `json:"trusted_networks"`
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

// WGClientConfig configures the outbound WireGuard tunnel (wg1). This router
// dials the remote endpoint; the remote peer is provisioned to accept this
// device only. AllowedIPs lists the remote networks reachable through the
// tunnel (for example the office LAN).
type WGClientConfig struct {
	Enabled             bool     `json:"enabled"`
	Interface           string   `json:"interface"` // "wg1"
	PrivateKey          string   `json:"private_key,omitempty"`
	Address             string   `json:"address"`    // local tunnel address, e.g. "10.7.0.2/32"
	PublicKey           string   `json:"public_key"` // remote peer public key
	PresharedKey        string   `json:"preshared_key,omitempty"`
	Endpoint            string   `json:"endpoint"` // remote endpoint host:port
	AllowedIPs          []string `json:"allowed_ips"`
	PersistentKeepalive int      `json:"persistent_keepalive"`
}

// CloudflareConfig retains its historical JSON key so existing backups remain
// compatible. DDNS and Cloudflare Tunnel deliberately keep separate hostname
// and secret fields so saving either integration cannot mutate the other.
type CloudflareConfig struct {
	DDNSEnabled    bool   `json:"ddns_enabled"`
	DDNSProvider   string `json:"ddns_provider,omitempty"` // noip or cloudflare; empty is legacy Cloudflare
	DDNSUser       string `json:"ddns_username,omitempty"` // No-IP DDNS Key username/email
	APIToken       string `json:"api_token,omitempty"`     // DDNS credential secret: No-IP key password or Cloudflare API token
	ZoneID         string `json:"zone_id,omitempty"`       // Legacy field retained for import compatibility
	ZoneName       string `json:"zone_name,omitempty"`     // Cloudflare zone name only
	Domain         string `json:"domain"`                  // Hostname updated by the selected DDNS provider
	TunnelEnabled  bool   `json:"tunnel_enabled"`
	TunnelHostname string `json:"tunnel_hostname,omitempty"` // Expected public hostname routed by the remotely-managed tunnel
	TunnelToken    string `json:"tunnel_token,omitempty"`
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

// QoSConfig holds traffic shaping and CAKE / FQ-CoDel bufferbloat prevention settings.
type QoSConfig struct {
	Enabled           bool   `json:"enabled"`
	Algorithm         string `json:"algorithm"`
	DownloadLimitMbps int    `json:"download_limit_mbps"`
	UploadLimitMbps   int    `json:"upload_limit_mbps"`
}

// AccessWindow is a local-router-time half-open access interval. End must be
// later than Start; midnight-spanning windows are represented as two windows.
type AccessWindow struct {
	Start string `json:"start"` // HH:MM
	End   string `json:"end"`   // HH:MM
}

// WeeklyAccessSchedule controls managed services independently for each day.
// The legacy weekday/weekend fields remain readable for old backups.
type WeeklyAccessSchedule struct {
	DayWindows     map[string][]AccessWindow `json:"day_windows,omitempty"`
	WeekdayWindows []AccessWindow            `json:"weekday_windows,omitempty"`
	WeekendMode    string                    `json:"weekend_mode,omitempty"`
	WeekendWindows []AccessWindow            `json:"weekend_windows,omitempty"`
}

// DeviceProfile applies a service schedule to one or more static LAN addresses.
// DNS-derived destination sets are enforced in nftables, before established
// connection acceptance, so a session does not remain open after a window ends.
type DeviceProfile struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	IPAddresses []string             `json:"ip_addresses"`
	Services    []string             `json:"services"`
	Schedule    WeeklyAccessSchedule `json:"schedule"`
	Enabled     bool                 `json:"enabled"`
}

// FilterDeviceRule is retained only so old backups can be decoded. New writes
// must use DeviceProfiles because legacy dnsmasq address rules were global.
type FilterDeviceRule struct {
	ID              string   `json:"id"`
	Hostname        string   `json:"hostname"`
	IPAddress       string   `json:"ip_address"`
	BlockedServices []string `json:"blocked_services"`
	Enabled         bool     `json:"enabled"`
}

// DNSFilterConfig holds the global DNS sinkhole and per-device access profiles.
type DNSFilterConfig struct {
	Enabled        bool               `json:"enabled"`
	BlocklistURL   string             `json:"blocklist_url"`
	LastUpdated    string             `json:"last_updated"`
	FilterDevices  []FilterDeviceRule `json:"filter_devices,omitempty"` // legacy import only
	DeviceProfiles []DeviceProfile    `json:"device_profiles"`
}

// AdGuardConfig remains a source-compatible alias for older callers.
type AdGuardConfig = DNSFilterConfig

// RestrictedIPItem represents a device IP in the Restricted Alias list with a toggle state.
type RestrictedIPItem struct {
	Hostname  string `json:"hostname"`
	IPAddress string `json:"ip_address"`
	Enabled   bool   `json:"enabled"`
}

// SquidProxyConfig holds non-caching HTTP/HTTPS proxy configuration.
type SquidProxyConfig struct {
	Enabled       bool               `json:"enabled"`
	Port          int                `json:"port"`
	Username      string             `json:"username"`
	Password      string             `json:"password,omitempty"`
	RestrictedIPs []RestrictedIPItem `json:"restricted_ips"`
}

// SystemSettings holds basic appliance metadata and management settings.
type SystemSettings struct {
	Hostname         string `json:"hostname"`
	Domain           string `json:"domain"`
	HTTPSEnabled     bool   `json:"https_enabled"`
	HTTPSPort        int    `json:"https_port"`
	ManagementAccess string `json:"management_access"`
}

// WANSettings holds PPPoE internet connection configuration.
type WANSettings struct {
	Interface string `json:"interface"`
	Enabled   bool   `json:"enabled"`
	Username  string `json:"username"`
	Password  string `json:"password,omitempty"`
	MTU       int    `json:"mtu"`
}

// LANSettings holds local network configuration.
type LANSettings struct {
	Interface string `json:"interface"`
	IPAddress string `json:"ip_address"`
	Netmask   string `json:"netmask"`
	CIDR      string `json:"cidr"`
}

// DHCPSettings holds dnsmasq DHCP server configuration and static leases.
type DHCPSettings struct {
	Enabled      bool          `json:"enabled"`
	DNSEnabled   bool          `json:"dns_enabled"`
	RangeStart   string        `json:"range_start"`
	RangeEnd     string        `json:"range_end"`
	LeaseTime    string        `json:"lease_time"`
	DNSServers   []string      `json:"dns_servers"`
	StaticLeases []StaticLease `json:"static_leases"`
}

// StaticLease assigns a static IP to a specific MAC address.
type StaticLease struct {
	ID        string `json:"id"`
	Hostname  string `json:"hostname"`
	MAC       string `json:"mac"`
	IPAddress string `json:"ip_address"`
}

// DNSSettings holds static DNS records served by the local resolver,
// independent of DHCP.
type DNSSettings struct {
	Records []DNSRecord `json:"records,omitempty"`
}

// DNSRecord maps a local hostname to a fixed IPv4 address without requiring
// the host to receive a DHCP lease from the router.
type DNSRecord struct {
	Name string `json:"name"`
	IP   string `json:"ip"`
}

// FirewallConfig holds packet filtering and NAT port forwarding rules.
type FirewallConfig struct {
	DefaultWANInputPolicy string            `json:"default_wan_input_policy"`
	WANIngressMode        string            `json:"wan_ingress_mode"`
	StatefulFirewall      bool              `json:"stateful_firewall"`
	PortForwards          []PortForwardRule `json:"port_forwards"`
	CustomRules           []FirewallRule    `json:"custom_rules"`
	ExtraLANs             []ExtraLANConfig  `json:"extra_lans,omitempty"`
}

// ExtraLANConfig defines an additional isolated LAN segment (e.g. a media
// network). Only hosts inside AllowFrom CIDRs may reach the single service
// DstIP:DstPort; the segment has no WAN/LAN egress and hosts no router
// services (no DHCP/DNS), so everything else is dropped by the default policy.
// RouterAddress is the router-side gateway address on the segment (for
// example "192.168.50.1/24"); it lets the router reconstruct the segment
// itself after a clean reboot instead of relying on manual interface state.
type ExtraLANConfig struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Interface     string   `json:"interface"`
	CIDR          string   `json:"cidr"`
	RouterAddress string   `json:"router_address,omitempty"`
	DstIP         string   `json:"dst_ip"`
	DstPort       int      `json:"dst_port"`
	Protocol      string   `json:"protocol,omitempty"` // tcp (default), udp
	AllowFrom     []string `json:"allow_from"`
	Enabled       bool     `json:"enabled"`
}

type PortForwardRule struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Protocol     string `json:"protocol"`
	ExternalPort int    `json:"external_port"`
	InternalIP   string `json:"internal_ip"`
	InternalPort int    `json:"internal_port"`
	Enabled      bool   `json:"enabled"`
}

type FirewallRule struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Action    string `json:"action"`
	Direction string `json:"direction"`
	Protocol  string `json:"protocol"`
	SrcIP     string `json:"src_ip,omitempty"`
	DstPort   int    `json:"dst_port,omitempty"`
	Enabled   bool   `json:"enabled"`
}

// AccountingConfig controls per-device traffic accounting. Only aggregate byte
// counts per LAN address are recorded: no ports, hostnames, destinations or
// payload are stored, so enabling it does not turn the router into a traffic
// logger for the household.
type AccountingConfig struct {
	Enabled bool `json:"enabled"`
	// RetentionMonths bounds how many monthly buckets are kept per device.
	RetentionMonths int `json:"retention_months"`
}

// DeepCopy returns a fully detached copy of the configuration. Every slice
// and map in the model is copied, so mutating the returned value can never
// mutate the source. Callers that read canonical state and then modify it
// (redaction, diagnostics, candidate editing) must use DeepCopy.
func (c SystemConfig) DeepCopy() SystemConfig {
	out := c
	out.TrustedNetworks = slices.Clone(c.TrustedNetworks)
	out.WireGuard.Peers = make([]WireGuardPeer, len(c.WireGuard.Peers))
	for i, peer := range c.WireGuard.Peers {
		out.WireGuard.Peers[i] = peer
		out.WireGuard.Peers[i].AllowedIPs = slices.Clone(peer.AllowedIPs)
	}
	out.WGClient.AllowedIPs = slices.Clone(c.WGClient.AllowedIPs)
	out.DHCP.DNSServers = slices.Clone(c.DHCP.DNSServers)
	out.DHCP.StaticLeases = slices.Clone(c.DHCP.StaticLeases)
	out.DNS.Records = slices.Clone(c.DNS.Records)
	out.Firewall.PortForwards = slices.Clone(c.Firewall.PortForwards)
	out.Firewall.CustomRules = slices.Clone(c.Firewall.CustomRules)
	out.Firewall.ExtraLANs = make([]ExtraLANConfig, len(c.Firewall.ExtraLANs))
	for i, lan := range c.Firewall.ExtraLANs {
		out.Firewall.ExtraLANs[i] = lan
		out.Firewall.ExtraLANs[i].AllowFrom = slices.Clone(lan.AllowFrom)
	}
	out.SquidProxy.RestrictedIPs = slices.Clone(c.SquidProxy.RestrictedIPs)
	out.AdGuard.FilterDevices = slices.Clone(c.AdGuard.FilterDevices)
	out.AdGuard.DeviceProfiles = deepCopyDeviceProfiles(c.AdGuard.DeviceProfiles)
	return out
}

func deepCopyDeviceProfiles(in []DeviceProfile) []DeviceProfile {
	out := make([]DeviceProfile, len(in))
	for i, profile := range in {
		out[i] = profile
		out[i].IPAddresses = slices.Clone(profile.IPAddresses)
		out[i].Services = slices.Clone(profile.Services)
		out[i].Schedule.DayWindows = make(map[string][]AccessWindow, len(profile.Schedule.DayWindows))
		for day, windows := range profile.Schedule.DayWindows {
			out[i].Schedule.DayWindows[day] = slices.Clone(windows)
		}
		out[i].Schedule.WeekdayWindows = slices.Clone(profile.Schedule.WeekdayWindows)
		out[i].Schedule.WeekendWindows = slices.Clone(profile.Schedule.WeekendWindows)
	}
	return out
}

// DefaultConfig returns a secure default Minimal Router configuration.
func DefaultConfig() SystemConfig {
	return SystemConfig{
		Revision:  1,
		UpdatedAt: time.Now(),
		System: SystemSettings{
			Hostname:         "minimalrouter",
			Domain:           "lan",
			HTTPSEnabled:     true,
			HTTPSPort:        8443,
			ManagementAccess: "lan_and_wireguard",
		},
		WAN: WANSettings{Interface: "eth0", Enabled: false, MTU: 1492},
		LAN: LANSettings{Interface: "eth1", IPAddress: "192.168.1.1", Netmask: "255.255.255.0", CIDR: "192.168.1.1/24"},
		DHCP: DHCPSettings{
			Enabled: true, RangeStart: "192.168.1.100", RangeEnd: "192.168.1.200", LeaseTime: "12h",
			DNSServers: []string{"1.1.1.1", "1.0.0.1"}, StaticLeases: []StaticLease{},
		},
		Firewall: FirewallConfig{
			DefaultWANInputPolicy: "deny", WANIngressMode: "wireguard_only", StatefulFirewall: true,
			PortForwards: []PortForwardRule{}, CustomRules: []FirewallRule{},
		},
		WireGuard:  WireGuardConfig{Enabled: false, Interface: "wg0", ListenPort: 51820, Address: "10.8.0.1/24", Peers: []WireGuardPeer{}},
		WGClient:   WGClientConfig{Enabled: false, Interface: "wg1", PersistentKeepalive: 25, AllowedIPs: []string{}},
		Cloudflare: CloudflareConfig{DDNSProvider: "noip"},
		SquidProxy: SquidProxyConfig{Enabled: false, Port: 3128, Username: "proxyadmin", RestrictedIPs: []RestrictedIPItem{}},
		AdGuard: DNSFilterConfig{
			Enabled: false, LastUpdated: "Never", FilterDevices: []FilterDeviceRule{}, DeviceProfiles: []DeviceProfile{},
		},
		QoS:        QoSConfig{Enabled: false, Algorithm: "cake", DownloadLimitMbps: 100, UploadLimitMbps: 20},
		Accounting: AccountingConfig{Enabled: false, RetentionMonths: 13},
		WiFi:       WiFiConfig{Enabled: false, Interface: "wlan0", SSID: "MinimalRouter-Home", Band: "5ghz", Channel: 36},
		// Default management trust boundary is the LAN. A future Proxmox
		// recovery virtual NIC can extend this list (e.g. 10.255.255.0/24).
		TrustedNetworks: []string{"192.168.1.0/24"},
	}
}
