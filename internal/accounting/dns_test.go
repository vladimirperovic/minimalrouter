package accounting

import (
	"context"
	"testing"
	"time"
)

func TestSiteOfGroupsRegistrableDomains(t *testing.T) {
	cases := map[string]string{
		"rr3---sn-abc.googlevideo.com": "googlevideo.com",
		"www.pornhub.com":              "pornhub.com",
		"example.com":                  "example.com",
		"news.bbc.co.uk":               "bbc.co.uk",
		"www.b92.co.rs":                "b92.co.rs",
		"www.rts.rs":                   "rts.rs",
		"a.b.c.example.org":            "example.org",
		"printer.lan":                  "printer.lan",
		"foo.com.de":                   "foo.com.de",
	}
	for name, want := range cases {
		if got := SiteOf(name); got != want {
			t.Errorf("SiteOf(%q) = %q, want %q", name, got, want)
		}
	}
}

type fakeDNSSource struct {
	pages []DNSDrain
	calls int
}

func (f *fakeDNSSource) Drain(_ context.Context, enabled bool) (DNSDrain, error) {
	if !enabled {
		return DNSDrain{}, nil
	}
	if f.calls >= len(f.pages) {
		f.calls++
		return DNSDrain{Enabled: true}, nil
	}
	page := f.pages[f.calls]
	f.calls++
	return page, nil
}

func TestDNSCollectorAggregatesFlushesAndQueries(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 10, 6, 12, 30, 0, 0, time.UTC)
	source := &fakeDNSSource{pages: []DNSDrain{
		{Enabled: true, More: true, Lookups: []DNSLookup{
			{Client: "192.168.1.50", Name: "www.pornhub.com", Count: 3, LastSeen: now.Unix() - 120},
			{Client: "192.168.1.50", Name: "ss.phncdn.com", Count: 7, LastSeen: now.Unix() - 60},
		}},
		{Enabled: true, Dropped: 4, Lookups: []DNSLookup{
			{Client: "192.168.1.60", Name: "pornhub.com", Count: 1, LastSeen: now.Unix()},
			{Client: "192.168.1.60", Name: "rr1---sn-x.googlevideo.com", Count: 5, LastSeen: now.Unix()},
		}},
	}}
	settings := DNSSettings{Enabled: true, RetentionDays: 30}
	collector := NewDNSCollector(store, source, func() DNSSettings { return settings }, nil)
	collector.collect(context.Background(), now)
	if source.calls != 2 {
		t.Fatalf("collector drained %d pages, want 2", source.calls)
	}
	recent := collector.Recent("192.168.1.50", 10)
	if len(recent) != 2 || recent[0].Name != "ss.phncdn.com" || recent[0].Site != "phncdn.com" {
		t.Fatalf("recent = %+v", recent)
	}
	collector.flush(now, settings)

	activity, err := store.DNSActivity(now, DNSQuery{Period: "today", FlaggedSites: []string{"pornhub.com", "phncdn.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if activity.TotalLookups != 20 || activity.UnitemizedLookups != 4 || activity.SiteCount != 3 {
		t.Fatalf("totals = %d/%d/%d, want 20/4/3", activity.TotalLookups, activity.UnitemizedLookups, activity.SiteCount)
	}
	if len(activity.Sites) != 3 || activity.Sites[0].Site != "phncdn.com" || activity.Sites[0].Lookups != 7 {
		t.Fatalf("sites = %+v", activity.Sites)
	}
	var pornhub DNSSiteUsage
	for _, site := range activity.Sites {
		if site.Site == "pornhub.com" {
			pornhub = site
		}
	}
	if pornhub.Lookups != 4 || pornhub.Devices != 2 {
		t.Fatalf("pornhub.com = %+v, want 4 lookups from 2 devices", pornhub)
	}
	if len(activity.Flagged) != 3 {
		t.Fatalf("flagged = %+v, want three device/site pairs", activity.Flagged)
	}
	if len(activity.Devices) != 2 || activity.Devices[0].Address != "192.168.1.50" || activity.Devices[0].Lookups != 10 {
		t.Fatalf("devices = %+v", activity.Devices)
	}
	var charted uint64
	for _, point := range activity.Points {
		charted += point.Lookups
	}
	if len(activity.Points) != 13 || charted != 16 {
		t.Fatalf("points=%d charted=%d, want 13 hourly buckets holding 16 itemized lookups", len(activity.Points), charted)
	}
	if !activity.Points[12].Observed || activity.Points[0].Observed {
		t.Fatal("observed flags do not follow the collection clock")
	}

	filtered, err := store.DNSActivity(now, DNSQuery{Period: "today", Device: "192.168.1.60", Search: "video"})
	if err != nil {
		t.Fatal(err)
	}
	if filtered.TotalLookups != 6 || len(filtered.Sites) != 1 || filtered.Sites[0].Site != "googlevideo.com" {
		t.Fatalf("filtered = %+v", filtered)
	}
	if escaped, err := store.DNSActivity(now, DNSQuery{Period: "today", Search: "_"}); err != nil || len(escaped.Sites) != 0 {
		t.Fatalf("LIKE wildcard was not escaped: %+v %v", escaped.Sites, err)
	}

	yesterday, err := store.DNSActivity(now, DNSQuery{Period: "yesterday"})
	if err != nil || yesterday.TotalLookups != 0 {
		t.Fatalf("today's lookups leaked into yesterday: %+v %v", yesterday.TotalLookups, err)
	}
}

func TestDNSCollectorDisableDeletesHistory(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	settings := DNSSettings{Enabled: true, RetentionDays: 30}
	source := &fakeDNSSource{pages: []DNSDrain{{Enabled: true, Lookups: []DNSLookup{{Client: "192.168.1.5", Name: "a.example.com", Count: 1, LastSeen: now.Unix()}}}}}
	collector := NewDNSCollector(store, source, func() DNSSettings { return settings }, nil)
	collector.collect(context.Background(), now)
	collector.flush(now, settings)
	if has, err := store.HasDNSHistory(); err != nil || !has {
		t.Fatalf("history was not stored: %v %v", has, err)
	}
	collector.disable()
	if has, err := store.HasDNSHistory(); err != nil || has {
		t.Fatalf("history survived disable: %v %v", has, err)
	}
	if len(collector.Recent("", 0)) != 0 {
		t.Fatal("recent lookups survived disable")
	}
}

func TestDNSCollectorSkipsWritesUnderStoragePressure(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	settings := DNSSettings{Enabled: true, RetentionDays: 30}
	source := &fakeDNSSource{pages: []DNSDrain{{Enabled: true, Lookups: []DNSLookup{{Client: "192.168.1.5", Name: "a.example.com", Count: 1, LastSeen: now.Unix()}}}}}
	collector := NewDNSCollector(store, source, func() DNSSettings { return settings }, func() bool { return false })
	collector.collect(context.Background(), now)
	collector.flush(now, settings)
	if has, _ := store.HasDNSHistory(); has {
		t.Fatal("DNS activity was written under critical storage pressure")
	}
}

func TestRecordDNSBoundsRowsPerDayAndPrunes(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	today := utcDay(now)
	flush := DNSFlush{Daily: map[DNSDailyKey]DNSDailyCount{}}
	for i := 0; i < maxDNSDailyRows+10; i++ {
		flush.Daily[DNSDailyKey{Day: today, Address: "192.168.1.5", Site: "site" + itoa(i) + ".com"}] = DNSDailyCount{Lookups: 1, FirstSeen: now.Unix(), LastSeen: now.Unix()}
	}
	old := today - 40*86400
	flush.Daily[DNSDailyKey{Day: old, Address: "192.168.1.5", Site: "old.com"}] = DNSDailyCount{Lookups: 9, FirstSeen: old, LastSeen: old}
	if err := store.RecordDNS(now, flush); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM dns_daily WHERE day = ?`, today).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != maxDNSDailyRows {
		t.Fatalf("today has %d rows, want the %d bound", rows, maxDNSDailyRows)
	}
	activity, err := store.DNSActivity(now, DNSQuery{Period: "today"})
	if err != nil {
		t.Fatal(err)
	}
	if activity.UnitemizedLookups != 10 || activity.TotalLookups != uint64(maxDNSDailyRows+10) {
		t.Fatalf("overflow was not kept as unitemized lookups: %+v", activity.UnitemizedLookups)
	}
	if err := store.PruneDNS(now, 30); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM dns_daily WHERE day = ?`, old).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("rows older than retention survived: %d %v", rows, err)
	}
}

func itoa(value int) string {
	digits := []byte{}
	for {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
		if value == 0 {
			return string(digits)
		}
	}
}

func TestDNSSettingsDefaultAndValidation(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	settings, err := store.DNSSettings()
	if err != nil || settings.Enabled || settings.RetentionDays != DefaultDNSRetentionDays {
		t.Fatalf("default settings = %+v, %v", settings, err)
	}
	for _, days := range []int{0, -1, MaxDNSRetentionDays + 1} {
		if err := store.SetDNSSettings(DNSSettings{Enabled: true, RetentionDays: days}); err == nil {
			t.Fatalf("retention %d accepted", days)
		}
	}
	if err := store.SetDNSSettings(DNSSettings{Enabled: true, RetentionDays: 14}); err != nil {
		t.Fatal(err)
	}
	if settings, err = store.DNSSettings(); err != nil || !settings.Enabled || settings.RetentionDays != 14 {
		t.Fatalf("stored settings = %+v, %v", settings, err)
	}
}
