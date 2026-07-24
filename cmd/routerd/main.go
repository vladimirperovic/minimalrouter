package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/vladimirperovic/minimalrouter/internal/api"
	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func main() {
	log.Println("Starting Minimal Router OS routerd (unprivileged management plane)...")

	dataDir := os.Getenv("MINIMALROUTER_DATA_DIR")
	if dataDir == "" {
		dataDir = "./data"
	}

	absDir, _ := filepath.Abs(dataDir)
	log.Printf("Initializing configuration store at %s\n", absDir)

	store, err := config.NewFileStore(absDir)
	if err != nil {
		log.Fatalf("Failed to initialize store: %v", err)
	}

	initialCfg, err := store.GetLatestConfig()
	if err != nil {
		log.Printf("Warning: Could not read store, fallback to default: %v", err)
		initialCfg = config.DefaultConfig()
	}

	engine := apply.NewEngine(initialCfg, store)

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
