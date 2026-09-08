package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/accounting"
	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/telemetry"
)

// accountingRegistry mirrors the pattern already used for the gateway monitor:
// the optional subsystem is attached without widening Server's core
// configuration/recovery responsibilities or its struct literal.
var accountingRegistry sync.Map

// ConfigureAccountingStore attaches the per-device traffic store. A nil store
// detaches it and makes the endpoint report the feature as unavailable.
func (s *Server) ConfigureAccountingStore(store *accounting.Store) {
	if store == nil {
		accountingRegistry.Delete(s)
		return
	}
	accountingRegistry.Store(s, store)
}

func (s *Server) configuredAccountingStore() *accounting.Store {
	store, _ := accountingRegistry.Load(s)
	result, _ := store.(*accounting.Store)
	return result
}

// RegisterAccountingRoutes exposes the read-only per-device usage summary.
func (s *Server) RegisterAccountingRoutes(mux *http.ServeMux) {
	sh := s.securityHeadersMiddleware
	mux.HandleFunc("GET /api/v1/firewall/activity", sh(s.trustedNetworksMiddleware(s.authMiddleware(s.handleFirewallActivity))))
	mux.HandleFunc("GET /api/v1/accounting/insights", sh(s.trustedNetworksMiddleware(s.authMiddleware(s.handleAccountingInsights))))
	mux.HandleFunc("GET /api/v1/accounting", sh(s.trustedNetworksMiddleware(s.authMiddleware(s.handleGetAccounting))))
}

func (s *Server) handleGetAccounting(w http.ResponseWriter, r *http.Request) {
	cfg := s.engine.GetCurrentConfig()
	snapshot := accounting.Snapshot{
		Available: false,
		Enabled:   cfg.Accounting.Enabled,
		UpdatedAt: time.Now().UTC(),
	}

	store := s.configuredAccountingStore()
	if store == nil {
		writeAccountingJSON(w, snapshot)
		return
	}
	snapshot.Available = true

	// Disabling accounting promises that per-device history is deleted. The
	// collector performs that deletion on its next tick, so between the change
	// and that tick rows may still exist on disk; serving them would contradict
	// the setting the operator is looking at.
	if !cfg.Accounting.Enabled {
		writeAccountingJSON(w, snapshot)
		return
	}

	months := 3
	if raw := r.URL.Query().Get("months"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 24 {
			http.Error(w, "months must be between 1 and 24", http.StatusBadRequest)
			return
		}
		months = parsed
	}

	usage, err := store.Months(months)
	if err != nil {
		http.Error(w, "Traffic accounting is unavailable", http.StatusServiceUnavailable)
		return
	}

	// Names come from the live DHCP lease table and the configured static
	// leases. Nothing about a device is persisted in the accounting database
	// beyond its address, so the label is resolved at read time and disappears
	// with the lease.
	labels := deviceLabels(cfg)
	for monthIndex := range usage {
		for deviceIndex := range usage[monthIndex].Devices {
			device := &usage[monthIndex].Devices[deviceIndex]
			if label, ok := labels[device.Address]; ok {
				device.Hostname = label.hostname
				device.MAC = label.mac
			}
		}
	}
	snapshot.Months = usage
	writeAccountingJSON(w, snapshot)
}

type deviceLabel struct {
	hostname string
	mac      string
}

func deviceLabels(cfg config.SystemConfig) map[string]deviceLabel {
	return deviceLabelsFromLeases(cfg, telemetry.CurrentDHCPLeases())
}

func deviceLabelsFromLeases(cfg config.SystemConfig, leases []telemetry.DHCPLease) map[string]deviceLabel {
	labels := map[string]deviceLabel{}
	for _, lease := range cfg.DHCP.StaticLeases {
		labels[lease.IPAddress] = deviceLabel{hostname: lease.Hostname, mac: lease.MAC}
	}
	for _, lease := range leases {
		existing, ok := labels[lease.IPAddress]
		if ok && existing.hostname != "" {
			continue
		}
		labels[lease.IPAddress] = deviceLabel{hostname: lease.Hostname, mac: lease.MAC}
	}
	return labels
}

func writeAccountingJSON(w http.ResponseWriter, snapshot accounting.Snapshot) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(snapshot)
}

func (s *Server) handleAccountingInsights(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "today"
	}
	now := time.Now().UTC()
	from, until, _, err := accounting.InsightsRange(now, period)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cfg := s.engine.GetCurrentConfig()
	store := s.configuredAccountingStore()
	result := accounting.Insights{Available: store != nil, Enabled: cfg.Accounting.Enabled, Period: period, From: from, Until: until, Points: []accounting.UsagePoint{}, Devices: []accounting.DeviceUsage{}}
	if store != nil && cfg.Accounting.Enabled {
		result, err = store.Insights(now, period)
		if err != nil {
			http.Error(w, "Traffic history is unavailable", http.StatusServiceUnavailable)
			return
		}
		labels := deviceLabels(cfg)
		for i := range result.Devices {
			if label, ok := labels[result.Devices[i].Address]; ok {
				result.Devices[i].Hostname = label.hostname
				result.Devices[i].MAC = label.mac
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(result)
}

func (s *Server) handleFirewallActivity(w http.ResponseWriter, r *http.Request) {
	result, err := s.configuredAccountingStore().FirewallActivity(time.Now().UTC())
	if err != nil {
		http.Error(w, "Firewall activity unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(result)
}
