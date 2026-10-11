package accounting

import "time"

// DeviceActivity is a bounded inventory projection, not a second copy of the
// monthly accounting report. It contains no destinations or DNS history.
type DeviceActivity struct {
	Address       string `json:"address"`
	LastSeenEpoch int64  `json:"last_seen_epoch"`
	MonthBytes    uint64 `json:"month_bytes"`
	NewThisMonth  bool   `json:"new_this_month"`
}

func (s *Store) DeviceActivity(now time.Time) ([]DeviceActivity, error) {
	result := []DeviceActivity{}
	month := now.UTC().Format("2006-01")
	previous := time.Date(now.UTC().Year(), now.UTC().Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0).Format("2006-01")
	rows, err := s.db.Query(`SELECT address, MAX(last_seen),
		SUM(CASE WHEN month = ? THEN rx_bytes + tx_bytes ELSE 0 END),
		MAX(CASE WHEN month = ? THEN 1 ELSE 0 END)
		FROM device_month WHERE month >= ? AND month <= ?
		GROUP BY address ORDER BY MAX(last_seen) DESC, address LIMIT ?`, month, previous, previous, month, maxTrackedDevices)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item DeviceActivity
		var seenPreviously int
		if err := rows.Scan(&item.Address, &item.LastSeenEpoch, &item.MonthBytes, &seenPreviously); err != nil {
			return nil, err
		}
		item.NewThisMonth = seenPreviously == 0
		result = append(result, item)
	}
	return result, rows.Err()
}
