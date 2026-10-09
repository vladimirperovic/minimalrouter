package services

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// ProfileStatus explains configuration at the router clock. It deliberately
// does not claim that a destination set or an actual packet was inspected.
type ProfileStatus struct {
	ID                  string     `json:"id"`
	Name                string     `json:"name"`
	IPAddresses         []string   `json:"ip_addresses"`
	Services            []string   `json:"services"`
	State               string     `json:"state"`
	AllowedNow          *bool      `json:"allowed_now"`
	NextChangeAt        *time.Time `json:"next_change_at"`
	NextState           string     `json:"next_state,omitempty"`
	MissingReservations []string   `json:"missing_reservations"`
	Basis               string     `json:"basis"`
	EnforcementVerified bool       `json:"enforcement_verified"`
}

func scheduleMinute(value string) int {
	hour, _ := strconv.Atoi(strings.Split(value, ":")[0])
	minute, _ := strconv.Atoi(strings.Split(value, ":")[1])
	return hour*60 + minute
}

func scheduleAllowed(windows map[string][]config.AccessWindow, now time.Time) bool {
	minute := now.Hour()*60 + now.Minute()
	day := strings.ToLower(now.Weekday().String())
	for _, window := range windows[day] {
		end := scheduleMinute(window.End)
		if window.End == "23:59" {
			end = 1440
		}
		if minute >= scheduleMinute(window.Start) && minute < end {
			return true
		}
	}
	return false
}

func DeviceProfileStatuses(cfg config.SystemConfig, now time.Time) []ProfileStatus {
	result := []ProfileStatus{}
	reserved := map[string]bool{}
	for _, lease := range cfg.DHCP.StaticLeases {
		reserved[lease.IPAddress] = true
	}
	for _, profile := range cfg.AdGuard.DeviceProfiles {
		status := ProfileStatus{ID: profile.ID, Name: profile.Name, IPAddresses: append([]string{}, profile.IPAddresses...), Services: append([]string{}, profile.Services...), Basis: "configured_schedule", MissingReservations: []string{}}
		for _, ip := range profile.IPAddresses {
			if !reserved[ip] {
				status.MissingReservations = append(status.MissingReservations, ip)
			}
		}
		switch {
		case !profile.Enabled:
			status.State = "paused"
		case !cfg.AdGuard.Enabled:
			status.State = "filter_off"
		default:
			windows := effectiveDayWindows(profile.Schedule)
			allowed := scheduleAllowed(windows, now)
			status.AllowedNow = &allowed
			status.State = "blocked"
			if allowed {
				status.State = "allowed"
			}
			// Check only interval boundaries, including adjacent-day midnight,
			// instead of scanning every minute for every device on each poll.
			boundaries := []time.Time{}
			for offset := 0; offset <= 8; offset++ {
				day := time.Date(now.Year(), now.Month(), now.Day()+offset, 0, 0, 0, 0, now.Location())
				boundaries = append(boundaries, day)
				for _, window := range windows[strings.ToLower(day.Weekday().String())] {
					for _, minute := range []int{scheduleMinute(window.Start), scheduleMinute(window.End)} {
						if minute == 1439 && window.End == "23:59" {
							minute = 1440
						}
						boundaries = append(boundaries, time.Date(day.Year(), day.Month(), day.Day(), minute/60, minute%60, 0, 0, now.Location()))
					}
				}
			}
			sort.Slice(boundaries, func(i, j int) bool { return boundaries[i].Before(boundaries[j]) })
			for _, at := range boundaries {
				if at.After(now) && scheduleAllowed(windows, at) != allowed {
					status.NextChangeAt = &at
					status.NextState = "allowed"
					if allowed {
						status.NextState = "blocked"
					}
					break
				}
			}
		}
		result = append(result, status)
	}
	return result
}
