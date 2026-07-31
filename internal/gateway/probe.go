package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	pingTimePattern = regexp.MustCompile(`time[=<]([0-9]+(?:\.[0-9]+)?)\s*ms`)
	packetPattern   = regexp.MustCompile(`([0-9]+)\s+packets transmitted,\s+([0-9]+)\s+(?:packets )?received`)
	lossPattern     = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)%\s+packet loss`)
)

// Prober performs one bounded ICMP sample. Implementations must be read-only.
type Prober interface {
	Probe(context.Context, string) TargetResult
}

type CommandProber struct {
	PingPath string
	Count    int
	Timeout  time.Duration
}

func NewCommandProber() *CommandProber {
	return &CommandProber{PingPath: findFixedBinary("ping"), Count: 4, Timeout: 6 * time.Second}
}

func findFixedBinary(name string) string {
	for _, path := range []string{"/bin/" + name, "/usr/bin/" + name, "/sbin/" + name, "/usr/sbin/" + name} {
		if _, err := exec.LookPath(path); err == nil {
			return path
		}
	}
	return ""
}

func (p *CommandProber) Probe(parent context.Context, target string) TargetResult {
	target = strings.TrimSpace(target)
	result := TargetResult{Target: target, PacketLossPercent: 100}
	if net.ParseIP(target) == nil || net.ParseIP(target).To4() == nil || strings.Contains(target, ":") {
		result.Error = "target is not a dotted-quad IPv4 address"
		return result
	}
	if p == nil || p.PingPath == "" {
		result.Error = "ping utility unavailable"
		return result
	}
	count := p.Count
	if count < 1 || count > 10 {
		count = 4
	}
	timeout := p.Timeout
	if timeout <= 0 || timeout > 15*time.Second {
		timeout = 6 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	// Arguments and destination are fully bounded; targets are literal IPv4
	// addresses validated by the gateway settings model.
	cmd := exec.CommandContext(ctx, p.PingPath, "-n", "-c", strconv.Itoa(count), "-W", "1", target)
	output, err := cmd.CombinedOutput()
	parsed, parseErr := parsePingOutput(target, output)
	if parseErr == nil {
		result = parsed
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		result.Error = "probe timed out"
	} else if err != nil && result.PacketsReceived == 0 {
		result.Error = "target did not reply"
	}
	return result
}

func parsePingOutput(target string, output []byte) (TargetResult, error) {
	result := TargetResult{Target: target, PacketLossPercent: 100}
	packetMatch := packetPattern.FindSubmatch(output)
	if len(packetMatch) != 3 {
		return result, fmt.Errorf("unrecognized ping packet summary")
	}
	result.PacketsSent, _ = strconv.Atoi(string(packetMatch[1]))
	result.PacketsReceived, _ = strconv.Atoi(string(packetMatch[2]))
	if result.PacketsSent <= 0 {
		return result, fmt.Errorf("invalid transmitted packet count")
	}
	if lossMatch := lossPattern.FindSubmatch(output); len(lossMatch) == 2 {
		result.PacketLossPercent, _ = strconv.ParseFloat(string(lossMatch[1]), 64)
	} else {
		result.PacketLossPercent = float64(result.PacketsSent-result.PacketsReceived) / float64(result.PacketsSent) * 100
	}
	result.Reachable = result.PacketsReceived > 0

	matches := pingTimePattern.FindAllSubmatch(output, -1)
	values := make([]float64, 0, len(matches))
	for _, match := range matches {
		value, err := strconv.ParseFloat(string(match[1]), 64)
		if err == nil {
			values = append(values, value)
		}
	}
	if len(values) > 0 {
		for _, value := range values {
			result.LatencyMS += value
		}
		result.LatencyMS /= float64(len(values))
		if len(values) > 1 {
			var variation float64
			for i := 1; i < len(values); i++ {
				variation += math.Abs(values[i] - values[i-1])
			}
			result.JitterMS = variation / float64(len(values)-1)
		}
	}
	return result, nil
}

// parsePeerOutput accepts iproute2's one-line PPP address representation:
// "7: ppp0 ... inet 203.0.113.10 peer 198.51.100.1/32 ...".
func parsePeerOutput(output []byte) (localIP, peerIP string) {
	fields := strings.Fields(string(bytes.TrimSpace(output)))
	for i, field := range fields {
		switch field {
		case "inet":
			if i+1 < len(fields) {
				localIP = strings.Split(fields[i+1], "/")[0]
			}
		case "peer":
			if i+1 < len(fields) {
				peerIP = strings.Split(fields[i+1], "/")[0]
			}
		}
	}
	return localIP, peerIP
}
