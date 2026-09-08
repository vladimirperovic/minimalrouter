package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/accounting"
	"github.com/vladimirperovic/minimalrouter/internal/api"
	"github.com/vladimirperovic/minimalrouter/internal/apply"
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

	return func() {
		cancel()
		<-done
		<-firewallDone
		server.ConfigureAccountingStore(nil)
		if err := store.Close(); err != nil {
			log.Printf("[ACCOUNTING] Failed to close accounting store: %v", err)
		}
	}
}
