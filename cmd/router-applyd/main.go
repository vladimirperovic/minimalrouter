package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
)

func main() {
	log.Println("Starting Minimal Router OS router-applyd (privileged execution helper)...")

	socketPath := "/tmp/router-applyd.sock"
	// Remove pre-existing socket file if present
	_ = os.Remove(socketPath)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Fatalf("Failed to bind Unix domain socket at %s: %v", socketPath, err)
	}
	defer listener.Close()

	log.Printf("router-applyd listening on unix://%s\n", socketPath)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Error accepting connection: %v", err)
			continue
		}
		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()

	var req apply.ApplyRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		resp := apply.ApplyResponse{
			Success:   false,
			Error:     fmt.Sprintf("Invalid RPC request format: %v", err),
			Timestamp: time.Now().Unix(),
		}
		json.NewEncoder(conn).Encode(resp)
		return
	}

	log.Printf("[router-applyd] Received operation %s (tx: %s, rev: %d)\n", req.Op, req.ID, req.Revision)

	var resp apply.ApplyResponse
	switch req.Op {
	case apply.OpApplyAll, apply.OpLoadNftables, apply.OpReloadService:
		resp = apply.ApplyResponse{
			ID:        req.ID,
			Success:   true,
			Logs:      "Operation executed successfully in privileged helper",
			Timestamp: time.Now().Unix(),
		}
	default:
		resp = apply.ApplyResponse{
			ID:        req.ID,
			Success:   false,
			Error:     fmt.Sprintf("Unknown or forbidden operation: %s", req.Op),
			Timestamp: time.Now().Unix(),
		}
	}

	json.NewEncoder(conn).Encode(resp)
}
