//go:build !linux

package gateway

import "context"

type LinkReader interface {
	Read(context.Context) LinkStatus
}

type unsupportedLinkReader struct{ interfaceName string }

func NewLinkReader(interfaceName string) LinkReader {
	if interfaceName == "" {
		interfaceName = "ppp0"
	}
	return unsupportedLinkReader{interfaceName: interfaceName}
}

func (r unsupportedLinkReader) Read(context.Context) LinkStatus {
	return LinkStatus{Interface: r.interfaceName}
}
