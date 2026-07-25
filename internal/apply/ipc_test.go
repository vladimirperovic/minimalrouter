package apply

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUnixClientHalfClosesRequestBeforeReadingResponse(t *testing.T) {
	socketDir, err := os.MkdirTemp("", "mr-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socketPath := filepath.Join(socketDir, "applyd.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		decoder := json.NewDecoder(io.LimitReader(conn, MaxRequestBytes))
		var req ApplyRequest
		if err := decoder.Decode(&req); err != nil {
			serverErr <- err
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			serverErr <- err
			return
		}
		serverErr <- json.NewEncoder(conn).Encode(ApplyResponse{
			ID: req.ID, Success: true, Verified: true,
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	response, err := NewUnixClient(socketPath).Apply(ctx, ApplyRequest{ID: "tx-half-close"})
	if err != nil {
		t.Fatalf("IPC request deadlocked or failed: %v", err)
	}
	if !response.Success || response.ID != "tx-half-close" {
		t.Fatalf("unexpected IPC response: %+v", response)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("test helper failed: %v", err)
	}
}
