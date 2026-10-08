package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/accounting"
	"github.com/vladimirperovic/minimalrouter/internal/api"
	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/dnsactivity"
	"github.com/vladimirperovic/minimalrouter/internal/dnsrisk"
	"github.com/vladimirperovic/minimalrouter/internal/storage"
)

// configureAccounting starts the per-device traffic collector. Like gateway
// monitoring it is optional: if the store cannot be opened the dashboard simply
// reports accounting as unavailable rather than failing the management plane.
func configureAccounting(server *api.Server, engine *apply.Engine, dataDir string) func() {
	store, err := accounting.OpenStore(dataDir)
	if err != nil {
		log.Printf("[ACCOUNTING] Per-device traffic accounting unavailable: %v", err)
		return func() {}
	}
	server.ConfigureAccountingStore(store)

	collector := accounting.NewCollector(store, accounting.CommandReader{}, func() accounting.Settings {
		cfg := engine.GetCurrentConfig()
		return accounting.Settings{
			Enabled:         cfg.Accounting.Enabled,
			RetentionMonths: cfg.Accounting.RetentionMonths,
			// Every apply recreates the nftables table and with it the counter
			// sets, and every apply advances the revision, so the revision is
			// exactly the counter generation.
			Generation: uint64(cfg.Revision),
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	firewallDone := make(chan struct{})
	go func() {
		defer close(firewallDone)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		client := apply.NewUnixClient("")
		for {
			queryCtx, stop := context.WithTimeout(ctx, 10*time.Second)
			response, err := client.Apply(queryCtx, apply.ApplyRequest{ID: fmt.Sprintf("fwstats-%d", time.Now().UnixNano()), Op: apply.OpFirewallCounters})
			stop()
			if err == nil && response.Success && response.FirewallCounters != nil {
				c := response.FirewallCounters
				if err := store.RecordFirewall(time.Now().UTC(), c.Generation, c.Seen, c.Accepted); err != nil {
					log.Printf("[FIREWALL] Activity storage failed: %v", err)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	done := make(chan struct{})
	go func() {
		defer close(done)
		collector.Run(ctx)
	}()
	log.Println("[ACCOUNTING] Per-device byte accounting collector started")

	// A failed settings read keeps the last known setting: treating it as
	// "disabled" would delete the history on a transient database error.
	var lastDNSSettings accounting.DNSSettings
	dnsCollector := accounting.NewDNSCollector(store, dnsActivitySource{client: dnsactivity.NewClient()}, func() accounting.DNSSettings {
		settings, err := store.DNSSettings()
		if err != nil {
			log.Printf("[DNS] could not read DNS activity setting: %v", err)
			return lastDNSSettings
		}
		lastDNSSettings = settings
		return settings
	}, func() bool { return storage.AllowNonessentialWrite(dataDir) })
	server.ConfigureDNSActivity(dnsCollector)
	riskDone := make(chan struct{})
	riskService, riskErr := dnsrisk.Open(dataDir, func() (bool, int, error) {
		settings, err := store.DNSSettings()
		return settings.Enabled, settings.RetentionDays, err
	}, func() bool { return storage.AllowNonessentialWrite(dataDir) })
	if riskErr != nil {
		log.Printf("[DNS] Risk monitor unavailable: %v", riskErr)
		close(riskDone)
	} else {
		server.ConfigureDNSRisk(riskService)
		dnsCollector.SetRiskObserver(func(lookups []accounting.DNSLookup) {
			batch := make([]dnsrisk.Lookup, 0, len(lookups))
			for _, l := range lookups {
				batch = append(batch, dnsrisk.Lookup{Name: l.Name, Address: l.Client, Count: l.Count, At: l.LastSeen})
			}
			riskService.Submit(batch)
		}, riskService.Clear)
		go func() { defer close(riskDone); riskService.Run(ctx) }()
	}
	dnsDone := make(chan struct{})
	go func() {
		defer close(dnsDone)
		dnsCollector.Run(ctx)
	}()

	return func() {
		cancel()
		<-done
		<-firewallDone
		<-dnsDone
		<-riskDone
		server.ConfigureDNSRisk(nil)
		if riskService != nil {
			if err := riskService.Close(); err != nil {
				log.Printf("[DNS] Could not close risk monitor: %v", err)
			}
		}
		server.ConfigureDNSActivity(nil)
		server.ConfigureAccountingStore(nil)
		if err := store.Close(); err != nil {
			log.Printf("[ACCOUNTING] Failed to close accounting store: %v", err)
		}
	}
}

// dnsActivitySource converges router-applyd on the setting and drains DNS
// lookups over the dedicated socket. Only validated client addresses, names
// and counts cross the privilege boundary.
type dnsActivitySource struct {
	client *dnsactivity.Client
}

func (s dnsActivitySource) Drain(ctx context.Context, enabled bool) (accounting.DNSDrain, error) {
	response, err := s.client.Drain(ctx, enabled)
	if err != nil {
		return accounting.DNSDrain{}, err
	}
	drain := accounting.DNSDrain{Enabled: response.Enabled, Dropped: response.Dropped, More: response.More, Lookups: make([]accounting.DNSLookup, 0, len(response.Entries))}
	for _, entry := range response.Entries {
		drain.Lookups = append(drain.Lookups, accounting.DNSLookup{Client: entry.Client, Name: entry.Name, Count: entry.Count, LastSeen: entry.LastSeen})
	}
	return drain, nil
}
