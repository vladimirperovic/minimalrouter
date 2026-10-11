package accounting

import (
	"testing"
	"time"
)

func TestDeviceActivityUsesTwoCalendarMonthsAndCurrentMonthBytes(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.db.Exec(`INSERT INTO device_month(address,month,rx_bytes,tx_bytes,last_seen) VALUES
	('192.168.1.2','2026-09',20,5,2),('192.168.1.2','2026-10',30,10,3),
	('192.168.1.3','2026-10',8,2,4),('192.168.1.4','2026-07',50,50,1)`)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.DeviceActivity(time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Address != "192.168.1.3" || !rows[0].NewThisMonth || rows[1].NewThisMonth || rows[1].MonthBytes != 40 {
		t.Fatalf("%+v", rows)
	}
}
