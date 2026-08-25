package startup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func bootPath(t *testing.T, dir string, r *Recorder) string {
	t.Helper()
	return filepath.Join(dir, "startup", r.boot.ID+".json")
}

func readBoot(t *testing.T, path string) Boot {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var boot Boot
	if err := json.Unmarshal(data, &boot); err != nil {
		t.Fatal(err)
	}
	return boot
}

// Every persist serialises the whole boot document. Persisting each sample made
// the bytes written grow with the square of the sample count: a full ten-minute
// capture wrote roughly 20 MiB to produce a 71 KiB file, on an appliance that
// boots from flash.
func TestFullCaptureDoesNotRewriteTheDocumentPerSample(t *testing.T) {
	dir := t.TempDir()
	r, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := bootPath(t, dir, r)

	samples := int(Window / SampleInterval)
	written := int64(0)
	for i := 0; i < samples; i++ {
		before := int64(0)
		if info, statErr := os.Stat(path); statErr == nil {
			before = info.ModTime().UnixNano()
		}
		// The loop runs instantly, so advance the flush marker by one sample
		// interval per iteration to reproduce the once-a-second production
		// cadence over the whole capture window.
		r.mu.Lock()
		r.lastSampleP = r.lastSampleP.Add(-SampleInterval)
		r.mu.Unlock()
		r.sample()
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatal(statErr)
		}
		if info.ModTime().UnixNano() != before {
			written += info.Size()
		}
	}
	final, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Persisting every sample wrote the sum of all 600 intermediate sizes, about
	// 300x the final document. Flushing on an interval leaves one write per
	// samplePersistInterval, each averaging half the final size, so the expected
	// ratio is around 20x. The bound below has headroom for that while still
	// failing loudly if per-sample persistence returns.
	ratio := float64(written) / float64(final.Size())
	t.Logf("wrote %d bytes for a %d byte final document (%.0fx)", written, final.Size(), ratio)
	if ratio > 40 {
		t.Errorf("a full capture amplified writes %.0fx; the interval flush should keep this near 20x", ratio)
	}
}

// Buffering samples must not lose them: completion has to flush whatever the
// interval was still holding.
func TestCompletionFlushesBufferedSamples(t *testing.T) {
	dir := t.TempDir()
	r, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		r.sample()
	}
	r.complete()

	boot := readBoot(t, bootPath(t, dir, r))
	if len(boot.Samples) != 12 {
		t.Errorf("expected every buffered sample to reach disk, got %d of 12", len(boot.Samples))
	}
	if !boot.Completed {
		t.Error("completion was not persisted")
	}
}

// An event is rare and meaningful, so it still reaches disk immediately.
func TestEventPersistsImmediately(t *testing.T) {
	dir := t.TempDir()
	r, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	r.sample()
	r.Event("routerd", "something worth recording")

	boot := readBoot(t, bootPath(t, dir, r))
	var found bool
	for _, event := range boot.Events {
		if event.Message == "something worth recording" {
			found = true
		}
	}
	if !found {
		t.Error("an event must be durable as soon as it is recorded")
	}
}

// With no uplink configured there is nothing for a resolver or an HTTPS dial to
// reach. Requiring those milestones anyway kept every first boot -- before the
// wizard runs -- probing for the whole ten-minute window and never completing.
func TestBootWithoutAnUplinkCompletesOnceManagementIsReady(t *testing.T) {
	dir := t.TempDir()
	r, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	r.Ready("management")

	if !r.checkReadiness(context.Background(), false, "", false) {
		t.Fatal("a boot with no uplink must complete once management is ready")
	}
}

func TestBootWithoutAnUplinkStillWaitsForWireGuard(t *testing.T) {
	dir := t.TempDir()
	r, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	r.Ready("management")

	if r.checkReadiness(context.Background(), false, "wg-does-not-exist", true) {
		t.Fatal("an enabled tunnel is still a milestone this boot has to reach")
	}
}

// A configured uplink keeps every milestone: management, PPPoE, DNS and
// Internet all have to be reached before the capture is complete.
func TestBootWithAnUplinkStillRequiresEveryMilestone(t *testing.T) {
	dir := t.TempDir()
	r, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	r.Ready("management")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // no probe may succeed
	if r.checkReadiness(ctx, true, "", false) {
		t.Fatal("a configured uplink must not be reported ready before ppp0 exists")
	}
}

// Run must return promptly rather than holding the window open when the boot
// has nothing left to wait for.
func TestRunReturnsWhenEveryApplicableMilestoneIsReady(t *testing.T) {
	dir := t.TempDir()
	r, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	r.Ready("management")

	done := make(chan struct{})
	go func() {
		r.Run(context.Background(), false, "", false)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run held the capture window open with no milestone left to reach")
	}

	boot := readBoot(t, bootPath(t, dir, r))
	if !boot.Completed {
		t.Error("the capture should be marked complete")
	}
}

// Boot files are pruned to MaxBoots. Pruning moved out of persist, so prove it
// still happens when a boot starts.
func TestBootFilesArePrunedToTheRetainedCount(t *testing.T) {
	dir := t.TempDir()
	startupDir := filepath.Join(dir, "startup")
	if err := os.MkdirAll(startupDir, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxBoots+4; i++ {
		name := filepath.Join(startupDir, "old-"+string(rune('a'+i))+".json")
		if err := os.WriteFile(name, []byte(`{"id":"old"}`), 0600); err != nil {
			t.Fatal(err)
		}
		// Keep the modification times distinct so pruning is deterministic.
		stamp := time.Now().Add(time.Duration(-i-1) * time.Hour)
		if err := os.Chtimes(name, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := New(dir); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(startupDir)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".json" {
			count++
		}
	}
	if count > MaxBoots {
		t.Errorf("expected at most %d retained boots, found %d", MaxBoots, count)
	}
}

// A routerd restart inside the same kernel boot continues the same capture
// rather than inventing a new one.
func TestRestartContinuesTheSameBootCapture(t *testing.T) {
	if _, err := os.ReadFile("/proc/sys/kernel/random/boot_id"); err != nil {
		t.Skip("boot identity falls back to a timestamp without procfs; this is a Linux behaviour")
	}
	dir := t.TempDir()
	first, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	first.Event("routerd", "before restart")
	firstID := first.boot.ID

	second, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if second.boot.ID != firstID {
		t.Fatalf("restart started a new boot %q instead of continuing %q", second.boot.ID, firstID)
	}
	var carried bool
	for _, event := range second.boot.Events {
		if event.Message == "before restart" {
			carried = true
		}
	}
	if !carried {
		t.Error("the restart lost the events recorded before it")
	}
}
