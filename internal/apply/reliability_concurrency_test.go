package apply

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

type blockingApplyClient struct {
	mu       sync.Mutex
	requests []ApplyRequest
	started  chan ApplyRequest
	release  chan struct{}
	fail     bool
}

func (c *blockingApplyClient) Apply(_ context.Context, req ApplyRequest) (*ApplyResponse, error) {
	c.mu.Lock()
	c.requests = append(c.requests, req)
	c.mu.Unlock()
	if c.started != nil {
		select {
		case c.started <- req:
		default:
		}
	}
	if c.release != nil {
		<-c.release
	}
	if c.fail {
		return nil, errors.New("injected helper failure")
	}
	return &ApplyResponse{ID: req.ID, Success: true, Verified: true}, nil
}

func TestStatusRemainsReadableDuringSlowApply(t *testing.T) {
	store := newScenarioStore(t)
	initial, err := store.GetLatestConfig()
	if err != nil {
		t.Fatal(err)
	}
	client := &blockingApplyClient{started: make(chan ApplyRequest, 1), release: make(chan struct{})}
	engine := NewEngineWithClient(initial, store, client)
	candidate := initial
	candidate.DHCP.LeaseTime = "24h"

	done := make(chan error, 1)
	go func() {
		_, err := engine.ProcessTransaction("slow-apply", candidate)
		done <- err
	}()
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("apply did not start")
	}

	readDone := make(chan struct{})
	go func() {
		_ = engine.GetCurrentConfig()
		status := engine.GetStatus()
		if !status.Applying || status.ActiveTransactionID != "slow-apply" {
			t.Errorf("unexpected status during apply: %+v", status)
		}
		close(readDone)
	}()
	select {
	case <-readDone:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("read-only status blocked behind privileged apply")
	}
	close(client.release)
	if err := <-done; err != nil {
		t.Fatalf("apply failed: %v", err)
	}
}

func TestConfirmAndRollbackCannotRunConcurrently(t *testing.T) {
	store := newScenarioStore(t)
	initial, err := store.GetLatestConfig()
	if err != nil {
		t.Fatal(err)
	}
	client := &blockingApplyClient{}
	engine := NewEngineWithClient(initial, store, client)
	candidate := candidateWithNewLAN(initial)
	pending, err := engine.ProcessTransaction("confirm-race", candidate)
	if err != nil {
		t.Fatal(err)
	}

	client.started = make(chan ApplyRequest, 1)
	client.release = make(chan struct{})
	confirmDone := make(chan error, 1)
	go func() {
		_, err := engine.ConfirmTransaction(pending.ID)
		confirmDone <- err
	}()
	select {
	case req := <-client.started:
		if req.Op != OpConfirm {
			t.Fatalf("first blocked operation=%s, want confirm", req.Op)
		}
	case <-time.After(time.Second):
		t.Fatal("confirmation did not start")
	}

	rollbackDone := make(chan struct{})
	go func() {
		engine.rollbackExpired(pending.ID)
		close(rollbackDone)
	}()
	select {
	case <-rollbackDone:
		t.Fatal("rollback ran concurrently with confirmation")
	case <-time.After(100 * time.Millisecond):
	}
	// Confirm performs two helper operations; release both sequentially.
	client.release <- struct{}{}
	select {
	case req := <-client.started:
		if req.Op != OpCommitConfirmed {
			t.Fatalf("second operation=%s, want commit-confirmed", req.Op)
		}
	case <-time.After(time.Second):
		t.Fatal("confirmed commit did not start")
	}
	client.release <- struct{}{}
	if err := <-confirmDone; err != nil {
		t.Fatalf("confirmation failed: %v", err)
	}
	select {
	case <-rollbackDone:
	case <-time.After(time.Second):
		t.Fatal("rollback waiter did not finish after confirmation")
	}
}

func TestInitialSetupCommitFailureRollsBackRuntime(t *testing.T) {
	store := newScenarioStore(t)
	initial, err := store.GetLatestConfig()
	if err != nil {
		t.Fatal(err)
	}
	client := &testApplyClient{response: &ApplyResponse{Success: true, Verified: true}}
	engine := NewEngineWithClient(initial, store, client)
	candidate := initial
	candidate.WAN.Interface = "ens18"
	candidate.LAN.Interface = "ens19"

	tx, err := engine.ProcessInitialSetup("setup-failure", candidate, func(config.SystemConfig) error {
		return errors.New("injected SQLite commit failure")
	})
	if err == nil {
		t.Fatal("setup commit failure was accepted")
	}
	if tx.CurrentState != StateRolledBack {
		t.Fatalf("state=%s, want rolled back (%s)", tx.CurrentState, tx.Error)
	}
	if engine.GetCurrentConfig().LAN.Interface != initial.LAN.Interface {
		t.Fatal("failed setup changed canonical in-memory configuration")
	}
	if len(client.requests) != 2 || client.requests[1].ID != "setup-failure-rollback" {
		t.Fatalf("expected apply plus verified rollback, got %+v", client.requests)
	}
}
