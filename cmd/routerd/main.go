package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/vladimirperovic/minimalrouter/internal/api"
	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func main() {
	log.Println("Starting Minimal Router OS routerd (unprivileged management plane)...")

	// Initialize default canonical config & apply engine
	initialCfg := config.DefaultConfig()
	engine := apply.NewEngine(initialCfg)

	// Setup API server and HTTP routes
	server := api.NewServer(engine)
	mux := http.ServeMux{}
	server.RegisterRoutes(&mux)

	port := 8080
	log.Printf("routerd listening on http://127.0.0.1:%d/api/v1/\n", port)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", port), &mux); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
