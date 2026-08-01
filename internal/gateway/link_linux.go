//go:build linux

package gateway

import (
	"context"
	"net"
	"os/exec"
)

type LinkReader interface {
	Read(context.Context) LinkStatus
}

type LinuxLinkReader struct {
	Interface string
	IPPath    string
}

func NewLinkReader(interfaceName string) *LinuxLinkReader {
	if interfaceName == "" {
		interfaceName = "ppp0"
	}
	return &LinuxLinkReader{Interface: interfaceName, IPPath: findFixedBinary("ip")}
}

func (r *LinuxLinkReader) Read(ctx context.Context) LinkStatus {
	status := LinkStatus{Interface: r.Interface}
	iface, err := net.InterfaceByName(r.Interface)
	if err != nil || iface.Flags&net.FlagUp == 0 {
		return status
	}
	status.Connected = true
	if r.IPPath != "" {
		output, _ := exec.CommandContext(ctx, r.IPPath, "-4", "-o", "addr", "show", "dev", r.Interface).CombinedOutput()
		status.LocalIP, status.PeerIP = parsePeerOutput(output)
	}
	return status
}
