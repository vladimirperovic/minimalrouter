package recovery

import "testing"

// Older releases stored API snapshots with a local offset while recovery used
// UTC. The newest snapshot must be chosen by instant, not by text.
func TestSnapshotNewerComparesInstants(t *testing.T) {
	older := "2026-09-27T13:00:00+02:00" // 11:00 UTC
	newer := "2026-09-27T11:30:00Z"
	if !snapshotNewer(newer, older) || snapshotNewer(older, newer) {
		t.Fatal("snapshot recency was decided by text instead of time")
	}
	if !snapshotNewer("b", "a") {
		t.Fatal("unparsable timestamps must keep the text fallback")
	}
}
