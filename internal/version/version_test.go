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
		{"v1.3.1", "v1.4.0-rc.1", false},
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

func TestNewerAny(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v1.4.0-rc.1", "v1.3.1", true},
		{"v1.4.0-rc.2", "v1.4.0-rc.1", true},
		{"v1.4.0-rc.10", "v1.4.0-rc.9", true},
		{"v1.4.0-rc.1", "v1.4.0-beta.3", true},
		{"v1.4.0-beta", "v1.4.0-alpha.2", true},
		{"v1.4.0", "v1.4.0-rc.3", true},
		{"v1.4.0-rc.3", "v1.4.0", false},
		{"v1.3.1", "v1.4.0-rc.1", false},
		{"v1.4.0-preview.1", "v1.3.1", false},
		{"v1.4.0-rc.1", "dev", false},
	}
	for _, c := range cases {
		if got := NewerAny(c.latest, c.current); got != c.want {
			t.Errorf("NewerAny(%q, %q) = %v", c.latest, c.current, got)
		}
	}
	if !IsPre("v1.4.0-rc.1") || IsPre("v1.4.0") || IsPre("dev") || !Valid("v1.4.0-beta") || Valid("v1.4.0-12-gabc") {
		t.Fatal("pre-release detection")
	}
}
