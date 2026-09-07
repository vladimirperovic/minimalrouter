package telemetry

import "testing"

func TestQoSTelemetryDistinguishesMissingQueuesFromUnavailable(t *testing.T) {
	for _, tc := range []struct {
		data      string
		available bool
		count     int
	}{
		{`[]`, true, 0},
		{`[{"dev":"ppp0","kind":"cake","root":true},{"dev":"ppp0","kind":"ingress","parent":"ffff:fff1"},{"dev":"ifb0","kind":"cake","root":true}]`, true, 3},
		{`null`, false, 0}, {`not json`, false, 0}, {`[{"dev":"ppp0"}]`, false, 0},
	} {
		status := parseQoSStatus([]byte(tc.data))
		if status.Available != tc.available || len(status.Devices) != tc.count {
			t.Fatalf("incorrect QoS evidence for %s: %+v", tc.data, status)
		}
	}
	if parseQoSStatus(make([]byte, maxQoSOutputBytes+1)).Available {
		t.Fatal("oversized qdisc output accepted")
	}
}
