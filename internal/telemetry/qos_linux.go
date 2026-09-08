//go:build linux

package telemetry

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"time"
)

type qosOutputBuffer struct {
	bytes.Buffer
	overflow bool
}

func (b *qosOutputBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := maxQoSOutputBytes - b.Len()
	if len(p) > remaining {
		p = p[:remaining]
		b.overflow = true
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

func readQoSStatus() QoSStatus {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Reading qdiscs is unprivileged netlink; no new helper/doas capability.
	command := exec.CommandContext(ctx, "/sbin/tc", "-j", "qdisc", "show")
	var output qosOutputBuffer
	command.Stdout = &output
	command.Stderr = io.Discard
	if command.Run() != nil || output.overflow {
		return QoSStatus{Devices: []QoSDevice{}}
	}
	return parseQoSStatus(output.Bytes())
}
