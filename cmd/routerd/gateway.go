package main

import (
	"context"
	"log"

	"github.com/vladimirperovic/minimalrouter/internal/api"
	"github.com/vladimirperovic/minimalrouter/internal/gateway"
)

func configureGatewayMonitoring(server *api.Server, dataDir string) func() {
	store, err := gateway.OpenStore(dataDir)
	if err != nil {
		log.Printf("[GATEWAY] Monitoring unavailable: %v", err)
		return func() {}
	}
	monitor := gateway.NewMonitor(store, gateway.NewCommandProber(), gateway.NewLinkReader("ppp0"))
	server.ConfigureGatewayMonitor(monitor)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		monitor.Run(ctx)
	}()
	log.Println("[GATEWAY] Read-only PPPoE and WAN quality monitoring enabled")
	return func() {
		cancel()
		<-done
		server.ConfigureGatewayMonitor(nil)
		if err := store.Close(); err != nil {
			log.Printf("[GATEWAY] Failed to close monitoring store: %v", err)
		}
	}
}
