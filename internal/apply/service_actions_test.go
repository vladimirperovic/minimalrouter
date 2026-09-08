package apply

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func assertActionCode(t *testing.T, err error, code string) {
	t.Helper()
	var typed *ActionError
	if !errors.As(err, &typed) || typed.Code != code {
		t.Fatalf("error=%v, want %s", err, code)
	}
}

func TestServiceActionResponseSupports512Devices(t *testing.T) {
	pauses := make([]DevicePause, MaxDevicePauses)
	for i := range pauses {
		pauses[i] = DevicePause{IP: fmt.Sprintf("192.168.%d.%d", i/254, i%254+1), UntilUnix: math.MaxInt64}
	}
	data, err := json.Marshal(serviceActionResponse{Success: true, Pauses: pauses})
	if err != nil {
		t.Fatal(err)
	}
	if len(data) <= 16384 || len(data) >= MaxServiceActionResponseBytes {
		t.Fatalf("test response has unexpected size %d", len(data))
	}
	response, err := decodeServiceActionResponse(bytes.NewReader(data))
	if err != nil || len(response.Pauses) != MaxDevicePauses {
		t.Fatalf("512 response: %d, %v", len(response.Pauses), err)
	}
}

func TestServiceActionResponseRejectsBadFrames(t *testing.T) {
	tooMany := make([]DevicePause, MaxDevicePauses+1)
	data, _ := json.Marshal(serviceActionResponse{Success: true, Pauses: tooMany})
	for _, body := range []string{
		string(data), `{"success":true} {}`, `{"success":true,"error":"failed"}`, `{"success":true,"code":"recovery_required"}`, `{"success":true,"extra":1}`,
		`{"success":true,"pauses":[{"ip":"::1","until_unix":0}]}`,
		`{"success":true,"pauses":[{"ip":"192.168.1.10","until_unix":-1}]}`,
		`{"success":true,"pauses":[{"ip":"192.168.1.10","until_unix":0},{"ip":"192.168.1.10","until_unix":0}]}`,
		`{"success":true}` + strings.Repeat(" ", MaxServiceActionResponseBytes),
	} {
		if _, err := decodeServiceActionResponse(strings.NewReader(body)); err == nil {
			t.Fatalf("accepted invalid frame %.100q", body)
		}
	}
}

func TestServiceActionResponsePreservesRecoveryClassification(t *testing.T) {
	_, err := decodeServiceActionResponse(strings.NewReader(`{"success":false,"code":"recovery_required","error":"rollback failed"}`))
	assertActionCode(t, err, ActionRecoveryRequired)
	response, err := decodeServiceActionResponse(strings.NewReader(`{"success":true}`))
	if err != nil || response.Pauses == nil {
		t.Fatalf("empty pauses should be an array: %+v, %v", response, err)
	}
}

func TestDevicePauseEngineValidatesRequestsAndBlocksUnstableStatus(t *testing.T) {
	e := NewEngineWithClient(config.DefaultConfig(), nil, &testApplyClient{})
	for _, ip := range []string{"bad", "::1", "203.0.113.1", e.GetCurrentConfig().LAN.IPAddress} {
		_, err := e.PauseDeviceInternet(context.Background(), ip, 0)
		assertActionCode(t, err, ActionInvalid)
		_, err = e.ResumeDeviceInternet(context.Background(), ip)
		assertActionCode(t, err, ActionInvalid)
	}
	_, err := e.PauseDeviceInternet(context.Background(), "192.168.1.10", 1)
	assertActionCode(t, err, ActionInvalid)
	for _, state := range []string{"applying", "recovery", "pending"} {
		t.Run(state, func(t *testing.T) {
			e := NewEngineWithClient(config.DefaultConfig(), nil, &testApplyClient{})
			switch state {
			case "applying":
				e.applying = true
			case "recovery":
				e.recoveryRequired = true
			case "pending":
				e.pending = &pendingChange{}
			}
			_, err := e.DeviceInternetPauses(context.Background())
			assertActionCode(t, err, ActionConflict)
			_, err = e.PauseDeviceInternet(context.Background(), "192.168.1.10", 0)
			assertActionCode(t, err, ActionConflict)
			_, err = e.ResumeDeviceInternet(context.Background(), "192.168.1.10")
			assertActionCode(t, err, ActionConflict)
		})
	}
}

func TestDevicePauseEngineSerializesStatusAndValidationWithConfiguration(t *testing.T) {
	for _, operation := range []string{"status", "pause", "resume"} {
		t.Run(operation, func(t *testing.T) {
			e := NewEngineWithClient(config.DefaultConfig(), nil, &testApplyClient{})
			e.operationMu.Lock()
			done := make(chan error, 1)
			started := make(chan struct{})
			go func() {
				close(started)
				var err error
				switch operation {
				case "status":
					_, err = e.DeviceInternetPauses(context.Background())
				case "pause":
					_, err = e.PauseDeviceInternet(context.Background(), "10.0.0.2", 0)
				case "resume":
					_, err = e.ResumeDeviceInternet(context.Background(), "10.0.0.2")
				}
				done <- err
			}()
			<-started
			select {
			case err := <-done:
				e.operationMu.Unlock()
				t.Fatalf("operation escaped configuration lock: %v", err)
			case <-time.After(20 * time.Millisecond):
			}
			e.mu.Lock()
			e.currentConfig.LAN.CIDR = "10.0.0.1/24"
			e.currentConfig.LAN.IPAddress = "10.0.0.1"
			e.recoveryRequired = true
			e.mu.Unlock()
			e.operationMu.Unlock()
			select {
			case err := <-done:
				assertActionCode(t, err, ActionConflict)
			case <-time.After(time.Second):
				t.Fatal("operation did not complete")
			}
		})
	}
}

func TestServiceActionSocketCancellationInterruptsRead(t *testing.T) {
	dir, err := os.MkdirTemp("", "mr-action-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "actions.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	received := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.ReadAll(conn)
		close(received)
		<-release
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := callServiceActionSocket(ctx, path, serviceActionRequest{Action: DeviceActionStatus})
		done <- err
	}()
	select {
	case <-received:
	case <-time.After(time.Second):
		t.Fatal("request not half-closed")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not interrupt socket read")
	}
}
