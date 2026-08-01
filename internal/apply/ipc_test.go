package apply

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultSocketPathMatchesOpenRCReadinessGate(t *testing.T) {
	initScript := filepath.Join("..", "..", "packaging", "alpine", "router-applyd.initd")
	data, err := os.ReadFile(initScript)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("[ -S "+DefaultSocketPath+" ]")) {
		t.Fatalf("OpenRC readiness gate does not use apply socket %q", DefaultSocketPath)
	}
	if !bytes.Contains(data, []byte("rm -f "+DefaultSocketPath)) {
		t.Fatalf("OpenRC startup does not clear stale apply socket %q", DefaultSocketPath)
	}
}

func TestAlpineModuleManifestIncludesPPPoE(t *testing.T) {
	manifest := filepath.Join("..", "..", "packaging", "alpine", "minimalrouter.modules")
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(append([]byte("\n"), data...), []byte("\npppoe\n")) {
		t.Fatal("Alpine kernel module manifest omits pppoe; pppd cannot create a PPPoE socket")
	}
}

func TestPPPoEServiceRaisesWANInterfaceBeforeStartingPPPD(t *testing.T) {
	initScript := filepath.Join("..", "..", "packaging", "alpine", "pppoe-wan.initd")
	data, err := os.ReadFile(initScript)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`$1 == "plugin" && $2 == "pppoe.so" { print $3; exit }`)) {
		t.Fatal("PPPoE service does not derive the WAN interface from the generated peer config")
	}
	if !bytes.Contains(data, []byte(`/sbin/ip link set dev "$wan_interface" up`)) {
		t.Fatal("PPPoE service starts pppd without first raising the WAN interface")
	}
}
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
