package main

import (
	"context"
	"log"

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

	collector := accounting.NewCollector(store, accounting.CommandReader{}, func() (bool, int) {
		cfg := engine.GetCurrentConfig()
		return cfg.Accounting.Enabled, cfg.Accounting.RetentionMonths
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		collector.Run(ctx)
	}()
	log.Println("[ACCOUNTING] Per-device byte accounting collector started")

	return func() {
		cancel()
		<-done
		server.ConfigureAccountingStore(nil)
		if err := store.Close(); err != nil {
			log.Printf("[ACCOUNTING] Failed to close accounting store: %v", err)
		}
	}
}
