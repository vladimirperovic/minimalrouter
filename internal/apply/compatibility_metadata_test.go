package apply

import (
	"encoding/json"
	"os"
	"testing"
)

func TestReleaseCompatibilityRuntimeProtocolMatchesRPC(t *testing.T) {
	data, err := os.ReadFile("../../packaging/alpine/compatibility.json")
	if err != nil {
		t.Fatalf("read release compatibility metadata: %v", err)
	}
	var metadata struct {
		RuntimeProtocol int `json:"runtime_protocol"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		t.Fatalf("decode release compatibility metadata: %v", err)
	}
	if metadata.RuntimeProtocol != ProtocolVersion {
		t.Fatalf("compatibility runtime_protocol=%d but helper ProtocolVersion=%d; bump/update compatibility metadata with any RPC ABI change", metadata.RuntimeProtocol, ProtocolVersion)
	}
}
