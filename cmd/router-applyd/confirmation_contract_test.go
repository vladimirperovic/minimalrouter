package main

import (
	"context"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

type confirmationContractClient struct {
	previous config.SystemConfig
	requests []apply.ApplyRequest
}

func (c *confirmationContractClient) Apply(_ context.Context, req apply.ApplyRequest) (*apply.ApplyResponse, error) {
	c.requests = append(c.requests, req)
	if err := validatePrivilegedCandidate(req.Config, &c.previous); err != nil {
		return nil, err
	}
	if err := validateConfirmationRequest(req, &c.previous); err != nil {
		return nil, err
	}
	return &apply.ApplyResponse{ID: req.ID, Success: true, Verified: true}, nil
}

func TestConfirmationPreviewHelperContract(t *testing.T) {
	previous := config.DefaultConfig()
	previous.WiFi.Enabled = true
	previous.WiFi.Interface = "wlan0"
	previous.WiFi.SSID = "ContractAP"
	previous.WiFi.Passphrase = "old-passphrase-123"
	for _, test := range []struct {
		name         string
		edit         func(*config.SystemConfig)
		confirmation bool
	}{
		{"trusted networks", func(c *config.SystemConfig) { c.TrustedNetworks = append(c.TrustedNetworks, "10.255.255.0/24") }, true},
		{"WiFi password", func(c *config.SystemConfig) { c.WiFi.Passphrase = "new-passphrase-456" }, true},
		{"WiFi SSID", func(c *config.SystemConfig) { c.WiFi.SSID = "NextAP" }, true},
		{"WiFi channel", func(c *config.SystemConfig) { c.WiFi.Channel = 44 }, true},
		{"accounting", func(c *config.SystemConfig) { c.Accounting.Enabled = !c.Accounting.Enabled }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := previous.DeepCopy()
			test.edit(&candidate)
			preview, err := apply.PreviewTransition(previous, candidate)
			if err != nil {
				t.Fatal(err)
			}
			if preview.RequiresConfirmation != test.confirmation || confirmationModeAllowed(&previous, candidate) != test.confirmation {
				t.Fatalf("confirmation policy diverged: preview=%+v", preview)
			}
			client := &confirmationContractClient{previous: previous}
			engine := apply.NewEngineWithClient(previous, nil, client)
			tx, err := engine.ProcessTransaction("confirmation-contract", candidate)
			if err != nil {
				t.Fatalf("engine/helper contract failed: %v", err)
			}
			if len(client.requests) != 1 || client.requests[0].RequireConfirmation != test.confirmation || (tx.CurrentState == apply.StateAwaitingConfirmation) != test.confirmation {
				t.Fatal("engine request diverged from preview/helper policy")
			}
			if test.confirmation {
				if err := engine.Reconcile(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			req := apply.ApplyRequest{Op: apply.OpApplyAll, Config: candidate, RequireConfirmation: test.confirmation}
			if err := validateConfirmationRequest(req, &previous); err != nil {
				t.Fatal(err)
			}
			if test.confirmation {
				req.RequireConfirmation = false
				if err := validateConfirmationRequest(req, &previous); err == nil {
					t.Fatal("root accepted omitted required confirmation")
				}
			}
		})
	}
}

func TestConfirmationRejectsModesThatCannotRollback(t *testing.T) {
	previous := config.DefaultConfig()
	candidate := previous.DeepCopy()
	candidate.TrustedNetworks = append(candidate.TrustedNetworks, "10.255.255.0/24")
	for _, req := range []apply.ApplyRequest{
		{Op: apply.OpReconcile, Config: candidate, RequireConfirmation: true},
		{Op: apply.OpApplyAll, Config: candidate, RequireConfirmation: true, DeferLastGood: true},
	} {
		if err := validateConfirmationRequest(req, &previous); err == nil {
			t.Fatal("accepted invalid confirmation mode")
		}
	}
	candidate.LAN.Interface = "eth2"
	if confirmationModeAllowed(&previous, candidate) {
		t.Fatal("replaced rollback interface")
	}
}
