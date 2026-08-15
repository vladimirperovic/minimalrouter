package apply

import (
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestPreviewTransitionClassifiesSafeAndProtectedChanges(t *testing.T) {
	current := config.DefaultConfig()
	current.WAN.Enabled = true
	current.WAN.Username = "user"
	current.WAN.Password = "secret"

	qos := current.DeepCopy()
	qos.QoS.Enabled = true
	preview, err := PreviewTransition(current, qos)
	if err != nil {
		t.Fatalf("PreviewTransition(qos): %v", err)
	}
	if preview.Risk != "medium" || preview.RequiresConfirmation {
		t.Fatalf("unexpected QoS preview: %+v", preview)
	}

	wgClient := current.DeepCopy()
	wgClient.WGClient.Endpoint = "203.0.113.10:51820"
	preview, err = PreviewTransition(current, wgClient)
	if err != nil {
		t.Fatalf("PreviewTransition(wg client): %v", err)
	}
	if preview.Risk != "high" || !preview.RequiresConfirmation || preview.RollbackSeconds != 90 {
		t.Fatalf("unexpected protected preview: %+v", preview)
	}
}

func TestPreviewTransitionRejectsSameUnsafeTransitionAsApply(t *testing.T) {
	current := config.DefaultConfig()
	candidate := current.DeepCopy()
	candidate.System.HTTPSPort = current.System.HTTPSPort + 1
	if _, err := PreviewTransition(current, candidate); err == nil {
		t.Fatal("expected live management port change to be rejected")
	}
}
