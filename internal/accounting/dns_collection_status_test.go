package accounting

import (
	"context"
	"errors"
	"testing"
	"time"
)

type dnsSourceFunc func(context.Context, bool) (DNSDrain, error)

func (f dnsSourceFunc) Drain(ctx context.Context, enabled bool) (DNSDrain, error) {
	return f(ctx, enabled)
}

func TestDNSFailedCollectionDoesNotInventObservedZero(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	settings := DNSSettings{Enabled: true, RetentionDays: 30}
	now := time.Date(2026, 10, 8, 12, 30, 0, 0, time.UTC)
	fail := true
	source := dnsSourceFunc(func(context.Context, bool) (DNSDrain, error) {
		if fail {
			return DNSDrain{}, errors.New("helper unavailable")
		}
		return DNSDrain{Enabled: true}, nil
	})
	c := NewDNSCollector(store, source, func() DNSSettings { return settings }, nil)
	c.collect(context.Background(), now)
	c.flush(now, settings)
	activity, err := store.DNSActivity(now, DNSQuery{Period: "today"})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range activity.Points {
		if p.Observed {
			t.Fatal("failed drain presented as observed zero")
		}
	}
	if c.Status(now).State != "unavailable" {
		t.Fatal("failed collector presented as active")
	}
	fail = false
	c.collect(context.Background(), now.Add(time.Hour))
	c.flush(now.Add(time.Hour), settings)
	activity, err = store.DNSActivity(now.Add(time.Hour), DNSQuery{Period: "today"})
	if err != nil {
		t.Fatal(err)
	}
	if activity.Points[12].Observed || !activity.Points[13].Observed {
		t.Fatalf("collection gap lost: %+v", activity.Points)
	}
	if c.Status(now.Add(5*time.Hour)).State != "stale" {
		t.Fatal("stopped collector did not become stale")
	}
}

func TestDNSClearRejectsInflightDrainAndStorageFailureStaysVisible(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	settings := DNSSettings{Enabled: true, RetentionDays: 30}
	now := time.Now()
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	c := NewDNSCollector(store, dnsSourceFunc(func(context.Context, bool) (DNSDrain, error) {
		close(started)
		<-release
		return DNSDrain{Enabled: true, Lookups: []DNSLookup{{Name: "risk.example", Client: "192.0.2.1", Count: 1}}}, nil
	}), func() DNSSettings { return settings }, func() bool { return false })
	observed := 0
	cleared := 0
	c.SetRiskObserver(func([]DNSLookup) { observed++ }, func() error { cleared++; return nil })
	go func() { defer close(done); c.collect(context.Background(), now) }()
	<-started
	if err := c.Clear(); err != nil {
		t.Fatal(err)
	}
	close(release)
	<-done
	if observed != 0 || cleared != 1 || len(c.Recent("", 10)) != 0 {
		t.Fatal("in-flight drain survived clear")
	}
	c.source = &fakeDNSSource{}
	c.collect(context.Background(), now)
	c.flush(now, settings)
	c.collect(context.Background(), now.Add(time.Minute))
	if status := c.Status(now.Add(time.Minute)); status.State != "degraded" || status.Error == "" {
		t.Fatalf("write failure hidden by successful drain: %+v", status)
	}
}
