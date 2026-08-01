package storage

import "testing"

func TestEvaluatePressureThresholds(t *testing.T) {
	tests := []struct {
		name       string
		available  uint64
		wantLevel  PressureLevel
		wantWrites bool
	}{
		{"normal", 21, PressureNormal, true},
		{"warning", 20, PressureWarning, true},
		{"critical", 10, PressureCritical, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := Evaluate(100, tt.available)
			if status.Level != tt.wantLevel {
				t.Fatalf("level = %q, want %q", status.Level, tt.wantLevel)
			}
			if status.DurableWritesAllowed != tt.wantWrites {
				t.Fatalf("durable writes = %v, want %v", status.DurableWritesAllowed, tt.wantWrites)
			}
			if tt.wantLevel == PressureCritical && status.NonessentialWritesAllowed {
				t.Fatal("critical pressure must disable nonessential writes")
			}
		})
	}
}

func TestEvaluateUnknownFilesystemDoesNotInventPressure(t *testing.T) {
	status := Evaluate(0, 0)
	if status.Available {
		t.Fatal("zero-sized filesystem must be unavailable")
	}
	if status.Level != PressureUnknown {
		t.Fatalf("level = %q, want unknown", status.Level)
	}
	if !status.DurableWritesAllowed || !status.NonessentialWritesAllowed {
		t.Fatal("unavailable telemetry must not silently block development writes")
	}
}
