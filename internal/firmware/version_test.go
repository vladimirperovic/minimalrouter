package firmware

import "testing"

func TestCompareReleaseVersions(t *testing.T) {
	cases := []struct {
		left  string
		right string
		want  int
	}{
		{"0.1.3", "0.1.2", 1},
		{"v0.1.3", "0.1.3", 0},
		{"0.1.3-beta.2", "0.1.3-beta.1", 1},
		{"0.1.3", "0.1.3-beta.9", 1},
		{"0.1.3-beta.1", "0.1.3", -1},
		{"0.1.3+build.2", "0.1.3+build.1", 0},
		{"0.1.2", "0.1.10", -1},
		{"0.1.3-alpha", "0.1.3-alpha.1", -1},
	}
	for _, tc := range cases {
		got, err := compareReleaseVersions(tc.left, tc.right)
		if err != nil {
			t.Fatalf("compareReleaseVersions(%q, %q): %v", tc.left, tc.right, err)
		}
		if got != tc.want {
			t.Fatalf("compareReleaseVersions(%q, %q) = %d, want %d", tc.left, tc.right, got, tc.want)
		}
	}
}

func TestValidateForwardUpgrade(t *testing.T) {
	if err := validateForwardUpgrade("0.1.3", "0.1.2"); err != nil {
		t.Fatalf("forward upgrade rejected: %v", err)
	}
	if err := validateForwardUpgrade("0.1.2", "0.1.2"); err == nil {
		t.Fatal("same-version stage must be rejected")
	}
	if err := validateForwardUpgrade("0.1.1", "0.1.2"); err == nil {
		t.Fatal("downgrade stage must be rejected")
	}
	if err := validateForwardUpgrade("0.1.2", "0.0.0+bootstrap.abcdef"); err != nil {
		t.Fatalf("bootstrap baseline must permit first real release: %v", err)
	}
}
