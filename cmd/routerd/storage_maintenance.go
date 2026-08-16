package main

import (
	"log"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func startStorageMaintenance(store *config.SQLiteStore) func() {
	stop := make(chan struct{})
	run := func() {
		if err := store.MaintainStorage(); err != nil {
			log.Printf("[STORAGE] maintenance failed: %v", err)
		}
	}
	// Bound/prune once during management-plane startup, then keep the steady
	// state intentionally quiet. The underlying stores already prune on writes;
	// this hourly pass is only a passive safety net and does not belong on a
	// high-frequency appliance timer.
	run()
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				run()
			case <-stop:
				return
			}
		}
	}()
	return func() { close(stop) }
}
