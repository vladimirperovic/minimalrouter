package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// Server handles REST API requests for Minimal Router OS.
type Server struct {
	engine *apply.Engine
	mu     sync.RWMutex
}

// NewServer creates a new API server instance.
func NewServer(engine *apply.Engine) *Server {
	return &Server{
		engine: engine,
	}
}

// RegisterRoutes attaches /api/v1 endpoints to the provided HTTP mux.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/system", s.handleGetSystem)
	mux.HandleFunc("GET /api/v1/config", s.handleGetConfig)
	mux.HandleFunc("PUT /api/v1/config", s.handleUpdateConfig)
}

func (s *Server) handleGetSystem(w http.ResponseWriter, r *http.Request) {
	response := map[string]interface{}{
		"status":      "Connected",
		"version":     "v0.1-alpha",
		"uptime":      "18d 04h",
		"public_ip":   "185.33.42.117",
		"last_backup": "6 days ago",
		"last_snap":   "8 min ago",
		"update":      "Up to date",
		"timestamp":   time.Now().Unix(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfg := s.engine.GetCurrentConfig()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	var newCfg config.SystemConfig
	if err := json.NewDecoder(r.Body).Decode(&newCfg); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON body: %v", err), http.StatusBadRequest)
		return
	}

	txID := fmt.Sprintf("tx-%d", time.Now().UnixNano())
	tx, err := s.engine.ProcessTransaction(txID, newCfg)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": err.Error(),
			"tx":    tx,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(tx)
}
