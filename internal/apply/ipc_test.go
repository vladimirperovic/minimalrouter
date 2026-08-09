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

func TestAlpineSupervisorsExposePIDFilesToHealthChecks(t *testing.T) {
	for _, service := range []string{"routerd", "router-applyd"} {
		initScript := filepath.Join("..", "..", "packaging", "alpine", service+".initd")
		data, err := os.ReadFile(initScript)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(data, []byte(`chgrp routerd "$pidfile"`)) ||
			!bytes.Contains(data, []byte(`chmod 0640 "$pidfile"`)) {
			t.Errorf("%s does not expose its supervisor PID file to routerd health checks", service)
		}
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

func TestWireGuardTelemetryRequestIsPinnedToCanonicalInterfaces(t *testing.T) {
	canonical := ApplyRequest{Version: ProtocolVersion, ID: "wg-status", Op: OpWGTunnelStatus, TunnelInterface: "wg1"}
	canonical.Config.WireGuard.Interface = "wg0"
	canonical.Config.WGClient.Interface = "wg1"
	valid := []ApplyRequest{
		{Version: ProtocolVersion, ID: "wg-status", Op: OpWGTunnelStatus, TunnelInterface: "wg0"},
		canonical,
		{Version: ProtocolVersion, ID: "wg-status", Op: OpWGTunnelStatus},
	}
	for _, candidate := range valid {
		payload, err := json.Marshal(candidate)
		if err != nil {
			t.Fatal(err)
		}
		var req ApplyRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			t.Fatalf("valid telemetry request rejected: %v", err)
		}
	}

	badServer := ApplyRequest{Version: ProtocolVersion, ID: "wg-status", Op: OpWGTunnelStatus}
	badServer.Config.WireGuard.Interface = "wg9"
	badClient := ApplyRequest{Version: ProtocolVersion, ID: "wg-status", Op: OpWGTunnelStatus}
	badClient.Config.WGClient.Interface = "office0"
	invalid := []ApplyRequest{
		{Version: ProtocolVersion, ID: "wg-status", Op: OpWGTunnelStatus, TunnelInterface: "wg9"},
		badServer,
		badClient,
	}
	for _, candidate := range invalid {
		payload, err := json.Marshal(candidate)
		if err != nil {
			t.Fatal(err)
		}
		var req ApplyRequest
		if err := json.Unmarshal(payload, &req); err == nil {
			t.Fatalf("widened telemetry request accepted: %s", payload)
		}
	}
}

func TestApplyRequestCustomDecoderKeepsUnknownFieldRejection(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"version":    ProtocolVersion,
		"id":         "tx",
		"op":         OpReconcile,
		"config":     map[string]any{},
		"unexpected": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var req ApplyRequest
	if err := json.Unmarshal(payload, &req); err == nil {
		t.Fatal("ApplyRequest accepted an unknown IPC field")
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
