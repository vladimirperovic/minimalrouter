//go:build darwin

package main

import (
	"context"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
)

// previewApplyClient lets macOS exercise the complete unprivileged control
// plane without pretending that nftables, pppd, dnsmasq, or WireGuard were
// applied to the host. Production Linux builds cannot construct this client.
type previewApplyClient struct{}

func (previewApplyClient) Apply(_ context.Context, req apply.ApplyRequest) (*apply.ApplyResponse, error) {
	return &apply.ApplyResponse{
		ID:        req.ID,
		Success:   true,
		Verified:  true,
		Logs:      "macOS preview: validated and simulated only",
		Timestamp: time.Now().Unix(),
	}, nil
}

func newPreviewApplyClient() apply.Client {
	return previewApplyClient{}
}
