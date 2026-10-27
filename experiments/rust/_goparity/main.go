// Command goparity writes differential test cases for the Rust port.
//
// It lives in an underscore-prefixed directory so `go test ./...` and
// `go vet ./...` never pick it up; run it explicitly:
//
//	go run ./experiments/rust/_goparity -out experiments/rust/target/parity -n 4000
//
// Every case is a configuration (a valid base with random mutations) together
// with what the Go implementation returns for it. The Rust parity tests load
// the same inputs and must produce identical results.
package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/services"
)

type validationOutcome struct {
	Validate []config.ValidationError `json:"validate"`
	Scenario []config.ValidationError `json:"scenario"`
}

type configCase struct {
	Name        string                   `json:"name"`
	Input       string                   `json:"input"`
	UnmarshalOK bool                     `json:"unmarshal_ok"`
	Marshal     string                   `json:"marshal,omitempty"`
	Outcome     validationOutcome        `json:"outcome"`
	Changes     []config.ValidationError `json:"changes_from_base"`
	Confirm     bool                     `json:"requires_confirmation"`
	Migrated    string                   `json:"migrated,omitempty"`
	Trusted     map[string]bool          `json:"trusted,omitempty"`
}

func errorsOf(err error) []config.ValidationError {
	if err == nil {
		return nil
	}
	if list, ok := err.(config.ValidationErrors); ok {
		return []config.ValidationError(list)
	}
	return []config.ValidationError{{Field: "<error>", Message: err.Error()}}
}

func key(seed byte) string {
	b := make([]byte, 32)
	for i := range b {
		b[i] = seed + byte(i)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// fullConfig enables every feature so mutations reach every validator.
func fullConfig() config.SystemConfig {
	cfg := config.DefaultConfig()
	cfg.Revision = 7
	cfg.WAN.Enabled = true
	cfg.WAN.Username = "pppoe-user@isp"
	cfg.WAN.Password = "pppoe-secret"
	cfg.WireGuard.Enabled = true
	cfg.WireGuard.PrivateKey = key(1)
	cfg.WireGuard.Peers = []config.WireGuardPeer{
		{ID: "peer-1", Name: "Phone", PublicKey: key(2), PresharedKey: key(3), AllowedIPs: []string{"10.8.0.2/32"}, Enabled: true},
		{ID: "peer-2", Name: "Laptop", PublicKey: key(4), AllowedIPs: []string{"10.8.0.3/32"}, Endpoint: "203.0.113.10:51820", Enabled: true},
	}
	cfg.WGClient = config.WGClientConfig{Enabled: true, Interface: "wg1", PrivateKey: key(5), Address: "10.7.0.2/32", PublicKey: key(6),
		Endpoint: "vpn.example.com:51820", AllowedIPs: []string{"10.20.0.0/16", "192.168.50.0/24"}, PersistentKeepalive: 25}
	cfg.DHCP.StaticLeases = []config.StaticLease{
		{ID: "lease-1", Hostname: "nas", MAC: "aa:bb:cc:dd:ee:01", IPAddress: "192.168.1.10"},
		{ID: "lease-2", Hostname: "printer", MAC: "AA-BB-CC-DD-EE-02", IPAddress: "192.168.1.11"},
	}
	cfg.DNS.Records = []config.DNSRecord{{Name: "nas.home.arpa", IP: "192.168.1.10"}, {Name: "media.lan", IP: "192.168.1.12"}}
	cfg.Firewall.PortForwards = []config.PortForwardRule{
		{ID: "pf-1", Name: "Game server", Protocol: "both", ExternalPort: 27015, InternalIP: "192.168.1.20", InternalPort: 27015, Enabled: true},
	}
	cfg.Firewall.CustomRules = []config.FirewallRule{
		{ID: "r-1", Name: "Block telnet", Action: "deny", Direction: "forward", Protocol: "tcp", DstPort: 23, Enabled: true},
		{ID: "r-2", Name: "Allow ssh", Action: "allow", Direction: "input", Protocol: "tcp", SrcIP: "192.168.1.0/25", DstPort: 22, Enabled: true},
		{ID: "r-3", Name: "Ping", Action: "allow", Direction: "forward", Protocol: "icmp", Enabled: true, Scope: "wan", PresetID: "icmp-out"},
	}
	cfg.Firewall.ExtraLANs = []config.ExtraLANConfig{
		{ID: "media", Name: "Media network", Interface: "eth2", CIDR: "192.168.60.0/24", RouterAddress: "192.168.60.1/24",
			DstIP: "192.168.60.10", DstPort: 8096, Protocol: "tcp", AllowFrom: []string{"192.168.1.0/24", "10.8.0.2/32"}, Enabled: true},
		{ID: "legacy", Name: "Legacy", Interface: "eth3", CIDR: "192.168.70.0/24", DstIP: "192.168.70.5", DstPort: 80, AllowFrom: []string{"192.168.1.0/24"}, Enabled: false},
	}
	cfg.Cloudflare = config.CloudflareConfig{DDNSEnabled: true, DDNSProvider: "noip", DDNSUser: "user@example.com", APIToken: "ddns-secret", Domain: "home.example.com"}
	cfg.SquidProxy = config.SquidProxyConfig{Enabled: true, Port: 3128, Username: "proxyadmin", Password: "proxy-password",
		RestrictedIPs: []config.RestrictedIPItem{{Hostname: "kid", IPAddress: "192.168.1.30", Enabled: true}}}
	cfg.AdGuard.Enabled = true
	cfg.AdGuard.DeviceProfiles = []config.DeviceProfile{
		{ID: "kids", Name: "Kids", IPAddresses: []string{"192.168.1.30", "192.168.1.31"}, Services: []string{"youtube", "tiktok"},
			Schedule: config.WeeklyAccessSchedule{DayWindows: map[string][]config.AccessWindow{
				"monday": {{Start: "07:00", End: "08:00"}, {Start: "16:00", End: "20:00"}}, "saturday": {{Start: "9:00", End: "21:30"}}}}, Enabled: true},
		{ID: "legacy-schedule", Name: "Guest (old)", IPAddresses: []string{"192.168.1.40"}, Services: []string{"gaming"},
			Schedule: config.WeeklyAccessSchedule{WeekdayWindows: []config.AccessWindow{{Start: "18:00", End: "22:00"}}, WeekendMode: "custom",
				WeekendWindows: []config.AccessWindow{{Start: "10:00", End: "23:00"}}}, Enabled: true},
	}
	cfg.QoS.Enabled = true
	cfg.WiFi = config.WiFiConfig{Enabled: true, Interface: "wlan0", SSID: "Home", Passphrase: "a-long-passphrase", Band: "2.4ghz", Channel: 6}
	cfg.Accounting.Enabled = true
	cfg.TrustedNetworks = []string{"192.168.1.0/24", "10.8.0.0/24", "fd00::/8"}
	return cfg
}

var stringPool = []string{
	"", " ", "a", "eth0", "eth1", "eth2", "wg0", "wg1", "br-lan", "ppp0", "wlan0", "lo", "averyveryverylongname",
	"192.168.1.1", "192.168.1.0", "192.168.1.255", "192.168.1.50", "192.168.1.150", "192.168.2.1", "10.8.0.1", "10.8.0.5/32",
	"10.8.0.1/24", "10.8.0.1/16", "192.168.1.1/24", "192.168.1.1/25", "192.168.1.0/24", "192.168.60.0/24", "192.168.60.1/24",
	"0.0.0.0/0", "::/0", "fe80::1", "::ffff:192.168.1.1", "::ffff:192.168.1.1/120", "01.2.3.4", "1.1.1.1", "8.8.8.8", "127.0.0.1",
	"224.0.0.1", "255.255.255.255", "255.255.255.0", "255.255.0.0", "aa:bb:cc:dd:ee:ff", "aa:bb:cc:dd:ee", "aabb.ccdd.eeff",
	"1h", "1m", "30s", "169h", "12", "1h30m", "09:00", "9:00", "24:00", "23:59", "12:5", "tcp", "udp", "both", "icmp", "any",
	"allow", "deny", "input", "forward", "all", "wan", "router", "noip", "cloudflare", "cake", "fq_codel", "2.4ghz", "5ghz",
	"lan_and_wireguard", "wireguard_only", "custom", "all_day", "blocked", "same_as_weekdays", "youtube", "YouTube", "unknown",
	"monday", "funday", "home.example.com", "a..b", "-bad", "bad-", "x.local", "nas.home.arpa", "NAS.home.arpa", "example.com",
	"[REDACTED]", "short", "a-long-passphrase", "Kids & Teens", "Ünïcödé name", "tab\there", "bell\u0007", "emoji 🙂",
	"1.2.3.4:51820", "[2001:db8::1]:51820", "host:99999", "vpn..example.com:51820", "nohostport", key(9), key(1), "bm90LWEta2V5",
	"cloudflare_token_abcdefghijklmnop", "preset-ok", "Bad_Preset", strings.Repeat("n", 70), "192.168.1.0/23", "10.0.0.0/7", "172.16.5.0/24",
}

var numberPool = []float64{0, 1, -1, 6, 11, 12, 22, 23, 25, 36, 37, 48, 53, 80, 443, 1023, 1024, 1280, 1279, 1492, 1500, 1501,
	3128, 8096, 8443, 27015, 51820, 65535, 65536, 100000, 100001}

type mutator struct {
	rng *rand.Rand
}

type leaf struct {
	path   []any
	parent any
}

// collect lists every mutable location in a decoded JSON tree.
func collect(node any, path []any, out *[]leaf) {
	switch v := node.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			p := append(append([]any{}, path...), k)
			*out = append(*out, leaf{path: p, parent: v})
			collect(v[k], p, out)
		}
	case []any:
		for i := range v {
			p := append(append([]any{}, path...), i)
			*out = append(*out, leaf{path: p, parent: v})
			collect(v[i], p, out)
		}
	}
}

func get(root any, path []any) any {
	node := root
	for _, step := range path {
		switch s := step.(type) {
		case string:
			node = node.(map[string]any)[s]
		case int:
			node = node.([]any)[s]
		}
	}
	return node
}

func set(root any, path []any, value any) any {
	if len(path) == 0 {
		return value
	}
	parent := get(root, path[:len(path)-1])
	switch s := path[len(path)-1].(type) {
	case string:
		parent.(map[string]any)[s] = value
	case int:
		parent.([]any)[s] = value
	}
	return root
}

func (m *mutator) replacement(current any) any {
	// Mostly type-preserving so validation, not decoding, is exercised.
	if m.rng.Intn(40) == 0 {
		switch m.rng.Intn(4) {
		case 0:
			return nil
		case 1:
			return "wrong-type"
		case 2:
			return 3.5
		default:
			return []any{}
		}
	}
	switch v := current.(type) {
	case string:
		return stringPool[m.rng.Intn(len(stringPool))]
	case float64:
		return numberPool[m.rng.Intn(len(numberPool))]
	case bool:
		return !v
	case []any:
		switch m.rng.Intn(5) {
		case 0:
			return []any{}
		case 1:
			return nil
		case 2:
			if len(v) > 0 {
				return append(append([]any{}, v...), v[m.rng.Intn(len(v))])
			}
			return []any{stringPool[m.rng.Intn(len(stringPool))]}
		case 3:
			if len(v) > 1 {
				i := m.rng.Intn(len(v))
				return append(append([]any{}, v[:i]...), v[i+1:]...)
			}
			return v
		default:
			if len(v) > 0 {
				if _, isString := v[0].(string); isString {
					return append(append([]any{}, v...), stringPool[m.rng.Intn(len(stringPool))])
				}
			}
			return v
		}
	case nil:
		return stringPool[m.rng.Intn(len(stringPool))]
	default:
		return current
	}
}

func (m *mutator) mutate(input []byte, count int) []byte {
	var root any
	if err := json.Unmarshal(input, &root); err != nil {
		panic(err)
	}
	for i := 0; i < count; i++ {
		var leaves []leaf
		collect(root, nil, &leaves)
		target := leaves[m.rng.Intn(len(leaves))]
		root = set(root, target.path, m.replacement(get(root, target.path)))
	}
	out, err := json.Marshal(root)
	if err != nil {
		panic(err)
	}
	return out
}

var trustedProbes = []string{"192.168.1.20:443", "10.8.0.2:1234", "[::1]:80", "127.0.0.1", "[::ffff:192.168.1.5]:1", "203.0.113.1:9", "garbage", "[fd00::1]:5"}

func evaluate(name string, input []byte, base config.SystemConfig) configCase {
	out := configCase{Name: name, Input: string(input)}
	var cfg config.SystemConfig
	if err := json.Unmarshal(input, &cfg); err != nil {
		return out
	}
	out.UnmarshalOK = true
	marshal, _ := json.Marshal(cfg)
	out.Marshal = string(marshal)
	out.Outcome.Validate = errorsOf(cfg.Validate())
	out.Outcome.Scenario = errorsOf(cfg.ValidateScenarioSafety())
	out.Changes = errorsOf(cfg.ValidateChangesFrom(&base))
	out.Confirm = config.RequiresConfirmation(base, cfg)
	migrated := cfg.DeepCopy()
	migrated.MigrateLegacyFields()
	data, _ := json.Marshal(migrated)
	out.Migrated = string(data)
	out.Trusted = map[string]bool{}
	for _, probe := range trustedProbes {
		out.Trusted[probe] = cfg.IsTrustedClientAddress(probe)
	}
	return out
}

type textResult struct {
	Text  string `json:"text,omitempty"`
	Error string `json:"error,omitempty"`
}

func result(text string, err error) textResult {
	if err != nil {
		return textResult{Error: err.Error()}
	}
	return textResult{Text: text}
}

type explainResult struct {
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
	RuleID  string `json:"rule_id"`
	Source  string `json:"source_zone"`
	Dest    string `json:"destination_zone"`
	Error   string `json:"error,omitempty"`
}

type profileResult struct {
	ID         string   `json:"id"`
	State      string   `json:"state"`
	AllowedNow *bool    `json:"allowed_now"`
	NextChange string   `json:"next_change_at"`
	NextState  string   `json:"next_state"`
	Missing    []string `json:"missing_reservations"`
}

type servicesCase struct {
	Name             string                     `json:"name"`
	Input            string                     `json:"input"`
	Nftables         textResult                 `json:"nftables"`
	Dnsmasq          textResult                 `json:"dnsmasq"`
	PPPoE            []string                   `json:"pppoe"`
	Hostapd          textResult                 `json:"hostapd"`
	QoS              textResult                 `json:"qos"`
	Squid            textResult                 `json:"squid"`
	DDNS             textResult                 `json:"ddns"`
	Tunnel           textResult                 `json:"tunnel"`
	DoH              textResult                 `json:"doh"`
	WireGuard        textResult                 `json:"wireguard"`
	WireGuardRuntime textResult                 `json:"wireguard_runtime"`
	WGClientRuntime  textResult                 `json:"wg_client_runtime"`
	AdBlock          textResult                 `json:"adblock"`
	Counters         []string                   `json:"counters"`
	Warnings         []string                   `json:"warnings"`
	FirewallChanges  []string                   `json:"firewall_changes"`
	NetworkChanges   []string                   `json:"network_changes"`
	Explain          map[string]explainResult   `json:"explain"`
	Profiles         map[string][]profileResult `json:"profiles"`
}

var flowProbes = []services.FirewallFlow{
	{SourceIP: "192.168.1.30", DestinationIP: "8.8.8.8", Protocol: "tcp", Port: 443},
	{SourceIP: "192.168.1.30", DestinationIP: "8.8.8.8", Protocol: "udp", Port: 53},
	{SourceIP: "192.168.1.50", DestinationIP: "8.8.8.8", Protocol: "tcp", Port: 23},
	{SourceIP: "192.168.1.50", DestinationIP: "192.168.1.1", Protocol: "tcp", Port: 22},
	{SourceIP: "192.168.1.50", DestinationIP: "192.168.1.1", Protocol: "tcp", Port: 8443},
	{SourceIP: "192.168.1.50", DestinationIP: "192.168.1.1", Protocol: "udp", Port: 67},
	{SourceIP: "192.168.1.50", DestinationIP: "192.168.1.1", Protocol: "tcp", Port: 3128},
	{SourceIP: "10.8.0.2", DestinationIP: "10.8.0.1", Protocol: "tcp", Port: 27015},
	{SourceIP: "10.8.0.2", DestinationIP: "192.168.1.20", Protocol: "udp", Port: 27015},
	{SourceIP: "10.8.0.2", DestinationIP: "192.168.60.10", Protocol: "tcp", Port: 8096},
	{SourceIP: "192.168.1.5", DestinationIP: "192.168.60.10", Protocol: "tcp", Port: 8096},
	{SourceIP: "192.168.60.10", DestinationIP: "192.168.60.1", Protocol: "icmp", Port: 0},
	{SourceIP: "192.168.60.10", DestinationIP: "8.8.8.8", Protocol: "tcp", Port: 80},
	{SourceIP: "10.20.1.1", DestinationIP: "192.168.1.5", Protocol: "tcp", Port: 22},
	{SourceIP: "192.168.1.5", DestinationIP: "10.20.1.1", Protocol: "tcp", Port: 22},
	{SourceIP: "203.0.113.5", DestinationIP: "192.168.1.5", Protocol: "tcp", Port: 80},
	{SourceIP: "192.168.1.5", DestinationIP: "192.168.1.6", Protocol: "icmp", Port: 0},
	{SourceIP: "192.168.1.5", DestinationIP: "8.8.8.8", Protocol: "icmp", Port: 3},
	{SourceIP: "1.1.1.1", DestinationIP: "fe80::1", Protocol: "tcp", Port: 1},
	{SourceIP: "192.168.1.5", DestinationIP: "8.8.8.8", Protocol: "gre", Port: 0},
}

var profileTimes = []string{"2026-10-26T12:34:00+01:00", "2026-10-31T23:59:30+01:00", "2026-11-01T06:59:00Z", "2026-11-02T20:00:00+01:00"}

const adblockHosts = "0.0.0.0 ads.example.com\n127.0.0.1 Tracker.Example.NET.\n# comment\n0.0.0.0 localhost\n::1 bad_domain\n0.0.0.0 ads.example.com\n"

// profileStatuses records a Go panic as an outcome instead of aborting: the
// Rust port returns a value for every input.
func profileStatuses(cfg config.SystemConfig, now time.Time) (statuses []services.ProfileStatus, panicked bool) {
	defer func() {
		if recover() != nil {
			statuses, panicked = nil, true
		}
	}()
	return services.DeviceProfileStatuses(cfg, now), false
}

func evaluateServices(name string, input []byte, base config.SystemConfig) (servicesCase, bool) {
	out := servicesCase{Name: name, Input: string(input)}
	var cfg config.SystemConfig
	if err := json.Unmarshal(input, &cfg); err != nil {
		return out, false
	}
	out.Nftables = result(services.GenerateNftables(&cfg))
	out.Dnsmasq = result(services.GenerateDnsmasq(&cfg))
	bundle, _ := services.GeneratePPPoE(&cfg)
	out.PPPoE = []string{bundle.PeerConfig, bundle.ChapSecrets, bundle.PapSecrets}
	out.Hostapd = result(services.GenerateHostapd(&cfg))
	out.QoS = result(services.GenerateQoS(&cfg))
	out.Squid = result(services.GenerateSquidConfig(&cfg))
	out.DDNS = result(services.GenerateDynamicDNS(&cfg))
	out.Tunnel = result(services.GenerateCloudflareTunnel(&cfg))
	out.DoH = result(services.GenerateDoHProxy(&cfg))
	out.WireGuard = result(services.GenerateWireGuard(&cfg.WireGuard))
	out.WireGuardRuntime = result(services.GenerateWireGuardRuntime(&cfg.WireGuard))
	out.WGClientRuntime = result(services.GenerateWireGuardClientRuntime(&cfg.WGClient))
	out.AdBlock = result(services.GenerateAdBlockConf(&cfg, []byte(adblockHosts)))
	for _, rule := range cfg.Firewall.CustomRules {
		out.Counters = append(out.Counters, services.FirewallRuleCounter(rule))
	}
	out.Warnings = services.FirewallWarnings(cfg)
	out.FirewallChanges = services.DescribeFirewallChanges(base.Firewall, cfg.Firewall)
	out.NetworkChanges = services.DescribeNetworkChanges(base, cfg)
	out.Explain = map[string]explainResult{}
	for i, flow := range flowProbes {
		explanation, err := services.ExplainFirewallFlow(cfg, flow)
		r := explainResult{Verdict: explanation.Verdict, Reason: explanation.Reason, RuleID: explanation.RuleID, Source: explanation.SourceZone, Dest: explanation.DestinationZone}
		if err != nil {
			r.Error = err.Error()
		}
		out.Explain[strconv.Itoa(i)] = r
	}
	out.Profiles = map[string][]profileResult{}
	for _, at := range profileTimes {
		now, _ := time.Parse(time.RFC3339, at)
		statuses, panicked := profileStatuses(cfg, now)
		if panicked {
			out.Profiles[at] = []profileResult{{ID: "<go panic>"}}
			continue
		}
		for _, status := range statuses {
			next := ""
			if status.NextChangeAt != nil {
				next = status.NextChangeAt.Format(time.RFC3339)
			}
			out.Profiles[at] = append(out.Profiles[at], profileResult{ID: status.ID, State: status.State, AllowedNow: status.AllowedNow, NextChange: next, NextState: status.NextState, Missing: status.MissingReservations})
		}
	}
	return out, true
}

func main() {
	outDir := flag.String("out", "experiments/rust/target/parity", "output directory")
	n := flag.Int("n", 4000, "number of mutated cases")
	ns := flag.Int("services", 1500, "number of mutated generator cases")
	seed := flag.Int64("seed", 20261026, "random seed")
	nf := flag.Int("firmware", 500, "number of signed manifest cases")
	flag.Parse()
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		panic(err)
	}
	file, err := os.Create(filepath.Join(*outDir, "config_cases.jsonl"))
	if err != nil {
		panic(err)
	}
	defer file.Close()
	w := bufio.NewWriter(file)
	defer w.Flush()

	bases := map[string]config.SystemConfig{"default": config.DefaultConfig(), "full": fullConfig()}
	baseNames := []string{"default", "full"}
	m := &mutator{rng: rand.New(rand.NewSource(*seed))}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, name := range baseNames {
		data, _ := json.Marshal(bases[name])
		if err := enc.Encode(evaluate(name, data, bases[name])); err != nil {
			panic(err)
		}
	}
	for i := 0; i < *n; i++ {
		name := baseNames[m.rng.Intn(len(baseNames))]
		base := bases[name]
		data, _ := json.Marshal(base)
		mutated := m.mutate(data, 1+m.rng.Intn(3))
		if err := enc.Encode(evaluate(name+"-"+strconv.Itoa(i), mutated, base)); err != nil {
			panic(err)
		}
	}
	fmt.Fprintf(os.Stderr, "wrote %d config cases to %s\n", *n+len(baseNames), *outDir)

	sfile, err := os.Create(filepath.Join(*outDir, "services_cases.jsonl"))
	if err != nil {
		panic(err)
	}
	defer sfile.Close()
	sw := bufio.NewWriter(sfile)
	defer sw.Flush()
	senc := json.NewEncoder(sw)
	senc.SetEscapeHTML(false)
	written := 0
	for _, name := range baseNames {
		data, _ := json.Marshal(bases[name])
		c, _ := evaluateServices(name, data, bases[name])
		if err := senc.Encode(c); err != nil {
			panic(err)
		}
		written++
	}
	for i := 0; i < *ns; i++ {
		name := baseNames[m.rng.Intn(len(baseNames))]
		base := bases[name]
		data, _ := json.Marshal(base)
		mutated := m.mutate(data, 1+m.rng.Intn(3))
		c, ok := evaluateServices(name+"-"+strconv.Itoa(i), mutated, base)
		if !ok {
			continue
		}
		if err := senc.Encode(c); err != nil {
			panic(err)
		}
		written++
	}
	fmt.Fprintf(os.Stderr, "wrote %d services cases to %s\n", written, *outDir)
	writeFirmwareCases(*outDir, m.rng, *nf)
}
