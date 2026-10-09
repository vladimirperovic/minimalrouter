package services

import (
	"github.com/vladimirperovic/minimalrouter/internal/config"
	"testing"
	"time"
)

func TestProfileStatusMinuteBoundariesLegacyAndReservationContext(t *testing.T) {
	cfg := config.SystemConfig{}
	cfg.AdGuard.Enabled = true
	cfg.DHCP.StaticLeases = []config.StaticLease{{IPAddress: "192.0.2.5"}}
	cfg.AdGuard.DeviceProfiles = []config.DeviceProfile{{ID: "kids", Enabled: true, IPAddresses: []string{"192.0.2.5", "192.0.2.6"}, Schedule: config.WeeklyAccessSchedule{WeekdayWindows: []config.AccessWindow{{Start: "19:30", End: "22:30"}}, WeekendMode: "all_day"}}}
	for _, tc := range []struct{ now, state, next string }{
		{"2026-10-09T19:29:59+02:00", "blocked", "2026-10-09T19:30:00+02:00"},
		{"2026-10-09T19:30:00+02:00", "allowed", "2026-10-09T22:30:00+02:00"},
		{"2026-10-09T22:30:00+02:00", "blocked", "2026-10-10T00:00:00+02:00"},
		{"2026-10-10T23:59:45+02:00", "allowed", "2026-10-12T00:00:00+02:00"},
	} {
		now, _ := time.Parse(time.RFC3339, tc.now)
		got := DeviceProfileStatuses(cfg, now)[0]
		if got.State != tc.state || got.NextChangeAt == nil || got.NextChangeAt.Format(time.RFC3339) != tc.next || got.EnforcementVerified || got.Basis != "configured_schedule" {
			t.Fatalf("%s: %+v", tc.now, got)
		}
		if len(got.MissingReservations) != 1 || got.MissingReservations[0] != "192.0.2.6" {
			t.Fatalf("reservations: %+v", got)
		}
	}
	cfg.AdGuard.Enabled = false
	if got := DeviceProfileStatuses(cfg, time.Now())[0]; got.State != "filter_off" || got.AllowedNow != nil || got.NextChangeAt != nil {
		t.Fatalf("disabled: %+v", got)
	}
	cfg.AdGuard.DeviceProfiles[0].Enabled = false
	if got := DeviceProfileStatuses(cfg, time.Now())[0]; got.State != "paused" {
		t.Fatalf("paused: %+v", got)
	}
}
