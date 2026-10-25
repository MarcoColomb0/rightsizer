package version

import "testing"

func TestNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v1.2.0", "v1.1.9", true},
		{"v1.10.0", "v1.9.0", true},
		{"v2.0.0", "1.9.9", true},
		{"v1.2.0", "v1.2.0", false},
		{"v1.1.0", "v1.2.0", false},
		{"v1.2.0", "dev", false},
		{"v1.3.0-rc1", "v1.2.0", false},
		{"garbage", "v1.2.0", false},
		// A pre-release installed upgrades to its final release and later
		// ones, never to another pre-release.
		{"v1.4.0", "v1.4.0-rc.1", true},
		{"v1.3.0", "v1.4.0-rc.1", false},
		{"v1.4.1", "v1.4.0-rc.2", true},
		{"v1.4.0-rc.2", "v1.4.0-rc.1", false},
		{"v1.4.0", "v1.3.0-12-gabcdef-dirty", false},
	}
	for _, c := range cases {
		if got := Newer(c.latest, c.current); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.latest, c.current, got)
		}
	}
}

func TestIsPre(t *testing.T) {
	if !IsPre("v1.4.0-rc.1") || IsPre("v1.4.0") || IsPre("dev") {
		t.Fatal("pre-release detection")
	}
	if !Diagnostics("v1.4.0-rc.1") || Diagnostics("v1.4.0") || Diagnostics("dev") || Diagnostics("v1.3.0-4-gabcdef") {
		t.Fatal("diagnostic bundles are for pre-releases only")
	}
}
