package gateway

import "testing"

func TestSettingsValidation(t *testing.T) {
	valid := DefaultSettings()
	if err := valid.Validate(); err != nil {
		t.Fatalf("default settings invalid: %v", err)
	}
	cases := []Settings{
		{Enabled: true, Targets: []string{"1.1.1.1"}, IntervalSeconds: 30},
		{Enabled: true, Targets: []string{"1.1.1.1", "1.1.1.1"}, IntervalSeconds: 30},
		{Enabled: true, Targets: []string{"192.168.1.1", "8.8.8.8"}, IntervalSeconds: 30},
		{Enabled: true, Targets: []string{"100.64.0.1", "8.8.8.8"}, IntervalSeconds: 30},
		{Enabled: true, Targets: []string{"203.0.113.1", "8.8.8.8"}, IntervalSeconds: 30},
		{Enabled: true, Targets: []string{"1.1.1.1", "8.8.8.8"}, IntervalSeconds: 5},
	}
	for _, settings := range cases {
		if err := settings.Validate(); err == nil {
			t.Fatalf("expected invalid settings: %+v", settings)
		}
	}
}
