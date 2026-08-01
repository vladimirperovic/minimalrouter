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
	run()
	go func() {
		ticker := time.NewTicker(15 * time.Minute)
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
