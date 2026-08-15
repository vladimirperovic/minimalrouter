package buildinfo

import "testing"

func TestDisplayVersion(t *testing.T) {
	original := Version
	defer func() { Version = original }()

	for _, tc := range []struct {
		input string
		want  string
	}{
		{"", "dev"},
		{"dev", "dev"},
		{"0.1.3", "v0.1.3"},
		{"v0.1.3", "v0.1.3"},
	} {
		Version = tc.input
		if got := DisplayVersion(); got != tc.want {
			t.Fatalf("DisplayVersion(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
