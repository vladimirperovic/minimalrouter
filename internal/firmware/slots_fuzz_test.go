package firmware

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func FuzzOperationJournalParsing(f *testing.F) {
	if runtime.GOOS == "windows" {
		f.Skip("skipping on Windows due to symlink permission model differences")
	}
	root, err := os.MkdirTemp("", "slot-journal-fuzz-*")
	if err != nil {
		f.Fatal(err)
	}
	defer os.RemoveAll(root)

	for _, version := range []string{"v1.0.0", "v1.1.0"} {
		if err := os.MkdirAll(filepath.Join(root, "slots", version), 0o755); err != nil {
			f.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join("slots", "v1.0.0"), filepath.Join(root, "current")); err != nil {
		f.Fatal(err)
	}
	manager := SlotManager{Root: root}

	for _, seed := range [][]byte{
		{},
		[]byte(`{}`),
		[]byte(`{"version":1,"kind":"activate","old":{"current":"v1.0.0","previous":"","pending":"v1.1.0"},"next":{"current":"v1.1.0","previous":"v1.0.0","pending":""}}`),
		[]byte(`{"version":1,"kind":"activate","old":{"current":"../../etc"}}`),
		[]byte(`{"version":999999999999999999999999}`),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64*1024 {
			t.Skip()
		}
		if err := os.WriteFile(filepath.Join(root, operationJournalName), data, 0o600); err != nil {
			t.Fatal(err)
		}
		_, _ = manager.State()

		target, err := os.Readlink(filepath.Join(root, "current"))
		if err != nil {
			t.Fatal(err)
		}
		if target != filepath.Join("slots", "v1.0.0") {
			t.Fatalf("read-only status mutated current pointer to %q", target)
		}
	})
}
