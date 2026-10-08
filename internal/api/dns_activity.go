package api

import (
	"encoding/json"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/accounting"
	"github.com/vladimirperovic/minimalrouter/internal/services"
)

// dnsActivityRegistry attaches the optional DNS activity collector without
// widening Server's struct, like the accounting store and gateway monitor.
var dnsActivityRegistry sync.Map

// ConfigureDNSActivity attaches the DNS activity collector. A nil collector
// detaches it and makes the endpoints report the feature as unavailable.
func (s *Server) ConfigureDNSActivity(collector *accounting.DNSCollector) {
	if collector == nil {
		dnsActivityRegistry.Delete(s)
		return
	}
	dnsActivityRegistry.Store(s, collector)
}

func (s *Server) configuredDNSActivity() *accounting.DNSCollector {
	value, _ := dnsActivityRegistry.Load(s)
	collector, _ := value.(*accounting.DNSCollector)
	return collector
}

// RegisterDNSActivityRoutes exposes DNS lookup statistics.
func (s *Server) RegisterDNSActivityRoutes(mux *http.ServeMux) {
	gate := func(next http.HandlerFunc) http.HandlerFunc {
		return s.securityHeadersMiddleware(s.trustedNetworksMiddleware(s.authMiddleware(next)))
	}
	mux.HandleFunc("GET /api/v1/dns-activity", gate(s.handleDNSActivity))
	mux.HandleFunc("GET /api/v1/dns-activity/settings", gate(s.handleGetDNSActivitySettings))
	mux.HandleFunc("PUT /api/v1/dns-activity/settings", gate(s.handlePutDNSActivitySettings))
	mux.HandleFunc("GET /api/v1/dns-activity/recent", gate(s.handleDNSActivityRecent))
	mux.HandleFunc("POST /api/v1/dns-activity/clear", gate(s.handleDNSActivityClear))
	s.registerDNSRiskRoutes(mux)
}

// extraCategoryDomains extends the device-profile service lists for
// highlighting only. They are kept here, not in internal/services, because
// that package is compiled into the byte-identical bootstrap tools and its
// lists also drive blocking.
var extraCategoryDomains = map[string][]string{
	"adult": {
		"phncdn.com", "xvideos-cdn.com", "xhamster.com", "xhcdn.com", "fansly.com", "redtube.com", "youporn.com",
		"tube8.com", "spankbang.com", "eporner.com", "beeg.com", "motherless.com", "chaturbate.com", "stripchat.com",
		"bongacams.com", "livejasmin.com", "cam4.com",
	},
}

// dnsCategories maps a site to the device-profile service it belongs to. The
// aggregate "gaming" service only repeats other services' domains.
var dnsCategories = func() map[string]string {
	lists := map[string][]string{}
	for name, domains := range services.ServiceDomains {
		if name != "gaming" {
			lists[name] = append(lists[name], domains...)
		}
	}
	for name, domains := range extraCategoryDomains {
		lists[name] = append(lists[name], domains...)
	}
	names := make([]string, 0, len(lists))
	for name := range lists {
		names = append(names, name)
	}
	sort.Strings(names)
	out := map[string]string{}
	for _, name := range names {
		for _, domain := range lists[name] {
			if _, taken := out[domain]; !taken {
				out[domain] = name
			}
		}
	}
	return out
}()

func dnsCategorySites() []string {
	sites := make([]string, 0, len(dnsCategories))
	for site := range dnsCategories {
		sites = append(sites, site)
	}
	sort.Strings(sites)
	return sites
}

// dnsActivitySettings reports whether the feature is available and the
// operator's setting. Without the accounting store there is nowhere to keep
// either the setting or the history.
func (s *Server) dnsActivitySettings() (*accounting.Store, *accounting.DNSCollector, accounting.DNSSettings, error) {
	store := s.configuredAccountingStore()
	collector := s.configuredDNSActivity()
	if store == nil || collector == nil {
		return nil, nil, accounting.DNSSettings{RetentionDays: accounting.DefaultDNSRetentionDays}, nil
	}
	settings, err := store.DNSSettings()
	return store, collector, settings, err
}

func (s *Server) handleGetDNSActivitySettings(w http.ResponseWriter, _ *http.Request) {
	store, _, settings, err := s.dnsActivitySettings()
	if err != nil {
		http.Error(w, "DNS activity settings are unavailable", http.StatusServiceUnavailable)
		return
	}
	writeDNSActivityJSON(w, map[string]any{"available": store != nil, "enabled": settings.Enabled, "retention_days": settings.RetentionDays})
}

func (s *Server) handlePutDNSActivitySettings(w http.ResponseWriter, r *http.Request) {
	store, collector, previous, err := s.dnsActivitySettings()
	if store == nil || err != nil {
		http.Error(w, "DNS activity is unavailable", http.StatusServiceUnavailable)
		return
	}
	var next accounting.DNSSettings
	if err := decodeJSON(w, r, &next); err != nil {
		http.Error(w, "Invalid DNS activity settings", http.StatusBadRequest)
		return
	}
	if err := store.SetDNSSettings(next); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if next != previous {
		s.appendAudit("dns_activity.settings_changed", auditActor(r.RemoteAddr), map[string]string{
			"enabled": strconv.FormatBool(next.Enabled), "retention_days": strconv.Itoa(next.RetentionDays),
		})
	}
	collector.Wake()
	writeDNSActivityJSON(w, map[string]any{"available": true, "enabled": next.Enabled, "retention_days": next.RetentionDays})
}

// parseDNSDevice accepts an empty value or one IP address in canonical form.
func parseDNSDevice(raw string) (string, bool) {
	if raw == "" {
		return "", true
	}
	address, err := netip.ParseAddr(raw)
	if err != nil || address.Zone() != "" {
		return "", false
	}
	return address.Unmap().String(), true
}

// parseDNSSearch accepts a short lowercase fragment of a site name.
func parseDNSSearch(raw string) (string, bool) {
	search := strings.ToLower(strings.TrimSpace(raw))
	if len(search) > 64 {
		return "", false
	}
	for i := 0; i < len(search); i++ {
		c := search[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '_' && c != '.' {
			return "", false
		}
	}
	return search, true
}

func (s *Server) handleDNSActivity(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	period := query.Get("period")
	if period == "" {
		period = "today"
	}
	device, ok := parseDNSDevice(query.Get("device"))
	if !ok {
		http.Error(w, "device must be an IP address", http.StatusBadRequest)
		return
	}
	search, ok := parseDNSSearch(query.Get("q"))
	if !ok {
		http.Error(w, "search must be up to 64 letters, digits, dots, hyphens or underscores", http.StatusBadRequest)
		return
	}
	now := time.Now().UTC()
	from, until, _, err := accounting.InsightsRange(now, period)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	store, collector, settings, err := s.dnsActivitySettings()
	if err != nil {
		http.Error(w, "DNS activity is unavailable", http.StatusServiceUnavailable)
		return
	}
	result := accounting.DNSActivity{
		Available: store != nil && collector != nil, Enabled: settings.Enabled,
		RetentionDays: settings.RetentionDays, Period: period, From: from, Until: until, Device: device, Search: search,
		Points: []accounting.DNSPoint{}, Devices: []accounting.DNSDeviceUsage{}, Sites: []accounting.DNSSiteUsage{}, Flagged: []accounting.DNSDeviceSite{},
	}
	// Disabling promises deletion; the collector carries it out on its next
	// round, and rows that still exist until then must not be served.
	if result.Available && settings.Enabled {
		activity, err := store.DNSActivity(now, accounting.DNSQuery{Period: period, Device: device, Search: search, FlaggedSites: dnsCategorySites()})
		if err != nil {
			http.Error(w, "DNS activity is unavailable", http.StatusServiceUnavailable)
			return
		}
		activity.Enabled = true
		activity.RetentionDays = settings.RetentionDays
		labels := deviceLabels(s.engine.GetCurrentConfig())
		for i := range activity.Devices {
			if label, ok := labels[activity.Devices[i].Address]; ok {
				activity.Devices[i].Hostname = label.hostname
				activity.Devices[i].MAC = label.mac
			}
		}
		for i := range activity.Sites {
			activity.Sites[i].Category = dnsCategories[activity.Sites[i].Site]
		}
		for i := range activity.Flagged {
			activity.Flagged[i].Category = dnsCategories[activity.Flagged[i].Site]
			if label, ok := labels[activity.Flagged[i].Address]; ok {
				activity.Flagged[i].Hostname = label.hostname
			}
		}
		result = activity
	}
	result.Collection = collector.Status(now)
	if !settings.Enabled {
		result.Collection.State = "disabled"
	}
	writeDNSActivityJSON(w, result)
}

func (s *Server) handleDNSActivityRecent(w http.ResponseWriter, r *http.Request) {
	device, ok := parseDNSDevice(r.URL.Query().Get("device"))
	if !ok {
		http.Error(w, "device must be an IP address", http.StatusBadRequest)
		return
	}
	limit := 200
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 2000 {
			http.Error(w, "limit must be between 1 and 2000", http.StatusBadRequest)
			return
		}
		limit = parsed
	}
	_, collector, settings, err := s.dnsActivitySettings()
	if err != nil {
		http.Error(w, "DNS activity is unavailable", http.StatusServiceUnavailable)
		return
	}
	entries := []accounting.DNSRecentLookup{}
	if collector != nil && settings.Enabled {
		entries = collector.Recent(device, limit)
		labels := deviceLabels(s.engine.GetCurrentConfig())
		for i := range entries {
			entries[i].Category = dnsCategories[entries[i].Site]
			if label, ok := labels[entries[i].Address]; ok {
				entries[i].Hostname = label.hostname
			}
		}
	}
	writeDNSActivityJSON(w, map[string]any{"available": collector != nil, "enabled": settings.Enabled, "entries": entries})
}

func (s *Server) handleDNSActivityClear(w http.ResponseWriter, r *http.Request) {
	collector := s.configuredDNSActivity()
	if collector == nil {
		http.Error(w, "DNS activity is unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := collector.Clear(); err != nil {
		http.Error(w, "DNS activity history could not be deleted", http.StatusInternalServerError)
		return
	}
	s.appendAudit("dns_activity.history_cleared", auditActor(r.RemoteAddr), nil)
	writeDNSActivityJSON(w, map[string]bool{"cleared": true})
}

func writeDNSActivityJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(value)
}
