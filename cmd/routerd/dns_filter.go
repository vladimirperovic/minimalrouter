package main

import (
	"github.com/vladimirperovic/minimalrouter/internal/api"
	"github.com/vladimirperovic/minimalrouter/internal/dnsfilter"
	"github.com/vladimirperovic/minimalrouter/internal/storage"
	"log"
)

func configureDNSFilter(server *api.Server, dataDir string) func() {
	service, err := dnsfilter.Open(dataDir, dnsfilter.NewClient(), func() bool { return storage.AllowNonessentialWrite(dataDir) })
	if err != nil {
		log.Printf("[DNS FILTER] unavailable: %v", err)
		return func() {}
	}
	server.ConfigureDNSFilter(service)
	return service.Close
}
