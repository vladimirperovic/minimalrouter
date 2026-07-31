package gateway

import (
	"math"
	"testing"
)

func TestParseIPUtilsPingOutput(t *testing.T) {
	output := []byte(`PING 1.1.1.1 (1.1.1.1) 56(84) bytes of data.
64 bytes from 1.1.1.1: icmp_seq=1 ttl=57 time=10.0 ms
64 bytes from 1.1.1.1: icmp_seq=2 ttl=57 time=14.0 ms
64 bytes from 1.1.1.1: icmp_seq=3 ttl=57 time=12.0 ms
64 bytes from 1.1.1.1: icmp_seq=4 ttl=57 time=16.0 ms

--- 1.1.1.1 ping statistics ---
4 packets transmitted, 4 received, 0% packet loss, time 3004ms
`)
	result, err := parsePingOutput("1.1.1.1", output)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Reachable || result.PacketsReceived != 4 || result.PacketLossPercent != 0 {
		t.Fatalf("unexpected packet result: %+v", result)
	}
	if math.Abs(result.LatencyMS-13) > 0.001 || math.Abs(result.JitterMS-(10.0/3.0)) > 0.001 {
		t.Fatalf("unexpected latency/jitter: %+v", result)
	}
}

func TestParseTotalPacketLoss(t *testing.T) {
	output := []byte(`PING 203.0.113.1 (203.0.113.1): 56 data bytes

--- 203.0.113.1 ping statistics ---
4 packets transmitted, 0 packets received, 100% packet loss
`)
	result, err := parsePingOutput("203.0.113.1", output)
	if err != nil {
		t.Fatal(err)
	}
	if result.Reachable || result.PacketLossPercent != 100 || result.PacketsReceived != 0 {
		t.Fatalf("100%% loss was not preserved: %+v", result)
	}
}

func TestParsePeerOutput(t *testing.T) {
	local, peer := parsePeerOutput([]byte("7: ppp0    inet 203.0.113.10 peer 198.51.100.1/32 scope global ppp0"))
	if local != "203.0.113.10" || peer != "198.51.100.1" {
		t.Fatalf("unexpected local=%q peer=%q", local, peer)
	}
}
