package main

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/vladimirperovic/minimalrouter/internal/services"
)

func isIFBInterface(output string) bool {
	var links []struct {
		Name string `json:"ifname"`
		Info struct {
			Kind string `json:"info_kind"`
		} `json:"linkinfo"`
	}
	if json.Unmarshal([]byte(output), &links) != nil || len(links) != 1 {
		return false
	}
	return links[0].Name == services.QoSInterfaceName && links[0].Info.Kind == "ifb"
}

func ensureIFBInterface(run func(string, ...string) (string, error)) error {
	inspect := func() (string, error) {
		return run("/sbin/ip", "-j", "-d", "link", "show", "dev", services.QoSInterfaceName)
	}
	if out, err := inspect(); err == nil {
		if !isIFBInterface(out) {
			return errors.New("ifb0 exists but is not an IFB device")
		}
		return nil
	}
	// Loading the IFB driver can create its default ifb0 before RTM_NEWLINK
	// finishes. EEXIST in that case is success only after verifying the device.
	_, createErr := run("/sbin/ip", "link", "add", services.QoSInterfaceName, "type", "ifb")
	out, inspectErr := inspect()
	if inspectErr == nil && isIFBInterface(out) {
		return nil
	}
	if createErr != nil {
		return fmt.Errorf("create %s: %w", services.QoSInterfaceName, createErr)
	}
	return errors.New("created IFB device could not be verified")
}
