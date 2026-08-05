package apply

import (
	"fmt"
	"net"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func cidrContainedByAny(candidate string, zones []string) bool {
	_, candidateNet, err := net.ParseCIDR(candidate)
	if err != nil || candidateNet.IP.To4() == nil {
		return false
	}
	candidateOnes, candidateBits := candidateNet.Mask.Size()
	if candidateBits != 32 {
		return false
	}
	for _, zoneText := range zones {
		_, zone, parseErr := net.ParseCIDR(zoneText)
		if parseErr != nil || zone.IP.To4() == nil {
			continue
		}
		zoneOnes, zoneBits := zone.Mask.Size()
		if zoneBits == 32 && candidateOnes >= zoneOnes && zone.Contains(candidateNet.IP) {
			return true
		}
	}
	return false
}

// validateTransitionSafety checks invariants that depend on both the currently
// canonical state and the candidate. In particular, moving the LAN to another
// subnet is a two-step operation by design: the new subnet must first be added
// to trusted_networks and committed while the old management path is still
// available. The candidate must retain that trust as well. This prevents the
// provisional firewall/API split where a client moved to the new subnet can
// reach the candidate address but is rejected by the still-canonical trusted
// network gate, or becomes locked out immediately after confirmation.
func validateTransitionSafety(current, candidate config.SystemConfig) error {
	if current.LAN.CIDR == candidate.LAN.CIDR {
		return nil
	}
	_, oldNet, oldErr := net.ParseCIDR(current.LAN.CIDR)
	_, newNet, newErr := net.ParseCIDR(candidate.LAN.CIDR)
	if oldErr != nil || newErr != nil || oldNet.IP.To4() == nil || newNet.IP.To4() == nil {
		return nil // ordinary config validation reports malformed CIDRs
	}
	if oldNet.String() == newNet.String() {
		return nil // same subnet, gateway address change only
	}
	newNetwork := newNet.String()
	if !cidrContainedByAny(newNetwork, current.TrustedNetworks) {
		return fmt.Errorf("LAN subnet migration requires the new subnet %s to be added to trusted_networks in a separate confirmed transaction first", newNetwork)
	}
	if !cidrContainedByAny(newNetwork, candidate.TrustedNetworks) {
		return fmt.Errorf("LAN subnet migration candidate must retain trusted management access for the new subnet %s", newNetwork)
	}
	return nil
}
