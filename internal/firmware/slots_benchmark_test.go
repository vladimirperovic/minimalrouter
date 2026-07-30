package firmware

import (
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkSlotManagerState(b *testing.B) {
	root := b.TempDir()
	manager := SlotManager{Root: root}
	for _, version := range []string{"v1.0.0", "v1.1.0"} {
		if err := os.MkdirAll(filepath.Join(root, "slots", version), 0o755); err != nil {
			b.Fatal(err)
		}
	}
	state := SlotState{Current: "v1.1.0", Previous: "v1.0.0"}
	if err := manager.saveState(state); err != nil {
		b.Fatal(err)
	}
	if err := manager.swapLink("current", state.Current); err != nil {
		b.Fatal(err)
	}
	if err := manager.swapLink("previous", state.Previous); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := manager.State(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSlotManagerStateWithJournal(b *testing.B) {
	root := b.TempDir()
	manager := SlotManager{Root: root}
	for _, version := range []string{"v1.0.0", "v1.1.0"} {
		if err := os.MkdirAll(filepath.Join(root, "slots", version), 0o755); err != nil {
			b.Fatal(err)
		}
	}
	old := SlotState{Current: "v1.0.0", Pending: "v1.1.0"}
	next := SlotState{Current: "v1.1.0", Previous: "v1.0.0"}
	if err := manager.saveState(old); err != nil {
		b.Fatal(err)
	}
	if err := manager.swapLink("current", next.Current); err != nil {
		b.Fatal(err)
	}
	if err := manager.swapLink("previous", next.Previous); err != nil {
		b.Fatal(err)
	}
	if err := manager.beginOperation(slotOperation{Version: operationJournalVersion, Kind: "activate", Old: old, Next: next}); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := manager.State(); err != nil {
			b.Fatal(err)
		}
	}
}
