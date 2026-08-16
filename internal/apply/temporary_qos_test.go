package apply

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

type temporaryQoSClient struct {
	mu                    sync.Mutex
	requests              []ApplyRequest
	failRequest           int
	transportErrorRequest int
}

func (c *temporaryQoSClient) Apply(_ context.Context, req ApplyRequest) (*ApplyResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, req)
	if c.transportErrorRequest > 0 && len(c.requests) == c.transportErrorRequest {
		return nil, errors.New("injected transport failure")
	}
	if c.failRequest > 0 && len(c.requests) == c.failRequest {
		return &ApplyResponse{ID: req.ID, Success: false, Error: "injected failure"}, nil
	}
	return &ApplyResponse{ID: req.ID, Success: true, Verified: true}, nil
}

func (c *temporaryQoSClient) snapshot() []ApplyRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]ApplyRequest(nil), c.requests...)
}

func TestWithQoSBypassedRestoresCanonicalRuntime(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.QoS.Enabled = true
	client := &temporaryQoSClient{}
	engine := NewEngineWithClient(cfg, nil, client)

	called := false
	err := engine.WithQoSBypassed(context.Background(), func(context.Context) error {
		called = true
		requests := client.snapshot()
		if len(requests) != 1 {
			t.Fatalf("measurement started after %d privileged requests, want 1", len(requests))
		}
		if requests[0].Config.QoS.Enabled {
			t.Fatal("measurement runtime still has QoS enabled")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithQoSBypassed returned error: %v", err)
	}
	if !called {
		t.Fatal("measurement callback was not called")
	}

	requests := client.snapshot()
	if len(requests) != 2 {
		t.Fatalf("privileged request count = %d, want 2", len(requests))
	}
	if requests[0].Op != OpApplyAll || !requests[0].DeferLastGood || requests[0].Config.QoS.Enabled {
		t.Fatalf("unexpected bypass request: %#v", requests[0])
	}
	if requests[1].Op != OpReconcile || requests[1].DeferLastGood || !requests[1].Config.QoS.Enabled {
		t.Fatalf("unexpected restore request: %#v", requests[1])
	}
	if !engine.GetCurrentConfig().QoS.Enabled {
		t.Fatal("canonical engine configuration was mutated")
	}
	if engine.GetStatus().RecoveryRequired {
		t.Fatal("successful restoration marked recovery required")
	}
}

func TestWithQoSBypassedRestoresAfterMeasurementFailure(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.QoS.Enabled = true
	client := &temporaryQoSClient{}
	engine := NewEngineWithClient(cfg, nil, client)
	measurementErr := errors.New("measurement failed")

	err := engine.WithQoSBypassed(context.Background(), func(context.Context) error {
		return measurementErr
	})
	if !errors.Is(err, measurementErr) {
		t.Fatalf("error = %v, want measurement failure", err)
	}
	requests := client.snapshot()
	if len(requests) != 2 || requests[1].Op != OpReconcile {
		t.Fatalf("canonical restore was not attempted after measurement failure: %#v", requests)
	}
	if engine.GetStatus().RecoveryRequired {
		t.Fatal("successful restoration marked recovery required")
	}
}

func TestWithQoSBypassedReconcilesAfterAmbiguousBypassFailure(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.QoS.Enabled = true
	client := &temporaryQoSClient{transportErrorRequest: 1}
	engine := NewEngineWithClient(cfg, nil, client)
	called := false

	err := engine.WithQoSBypassed(context.Background(), func(context.Context) error {
		called = true
		return nil
	})
	if err == nil {
		t.Fatal("expected bypass transport failure")
	}
	if called {
		t.Fatal("measurement ran even though the temporary bypass was not verified")
	}
	requests := client.snapshot()
	if len(requests) != 2 {
		t.Fatalf("privileged request count = %d, want 2", len(requests))
	}
	if requests[1].Op != OpReconcile || !requests[1].Config.QoS.Enabled {
		t.Fatalf("canonical restore was not attempted after ambiguous bypass failure: %#v", requests)
	}
	if engine.GetStatus().RecoveryRequired {
		t.Fatal("successful canonical reconcile marked recovery required")
	}
}

func TestWithQoSBypassedMarksRecoveryWhenRestoreFails(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.QoS.Enabled = true
	client := &temporaryQoSClient{failRequest: 2}
	engine := NewEngineWithClient(cfg, nil, client)

	err := engine.WithQoSBypassed(context.Background(), func(context.Context) error { return nil })
	if err == nil {
		t.Fatal("expected restore failure")
	}
	status := engine.GetStatus()
	if !status.RecoveryRequired {
		t.Fatal("restore failure did not mark recovery required")
	}
	if status.RecoveryReason == "" {
		t.Fatal("restore failure did not record a recovery reason")
	}
}

func TestWithQoSBypassedSkipsPrivilegedApplyWhenQoSDisabled(t *testing.T) {
	cfg := config.DefaultConfig()
	client := &temporaryQoSClient{}
	engine := NewEngineWithClient(cfg, nil, client)
	called := false

	if err := engine.WithQoSBypassed(context.Background(), func(context.Context) error {
		called = true
		return nil
	}); err != nil {
		t.Fatalf("WithQoSBypassed returned error: %v", err)
	}
	if !called {
		t.Fatal("measurement callback was not called")
	}
	if requests := client.snapshot(); len(requests) != 0 {
		t.Fatalf("QoS-disabled path made privileged requests: %#v", requests)
	}
}
