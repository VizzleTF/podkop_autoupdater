package updater

import "testing"

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"v0.4.7":   "0.4.7",
		"0.4.7-1":  "0.4.7",
		"v1.2.3-9": "1.2.3",
		"1.2.3":    "1.2.3",
		"":         "",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidSemver(t *testing.T) {
	for _, ok := range []string{"0.0.1", "10.20.30"} {
		if !ValidSemver(ok) {
			t.Errorf("ValidSemver(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"v1.0.0", "1.0", "1.0.0-rc1", "abc"} {
		if ValidSemver(bad) {
			t.Errorf("ValidSemver(%q) = true, want false", bad)
		}
	}
}

func TestIsNewer(t *testing.T) {
	cases := []struct {
		installed, latest string
		want              bool
	}{
		{"0.4.6", "0.4.7", true},
		{"0.4.7", "0.4.7", false},
		{"0.4.7", "0.4.6", false},
		{"0.4.7-1", "0.4.7", false},
		{"v0.4.6", "v0.4.7", true},
		{"0.10.0", "0.9.99", false},
		{"1.0.0", "0.99.99", false},
	}
	for _, c := range cases {
		if got := IsNewer(c.installed, c.latest); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", c.installed, c.latest, got, c.want)
		}
	}
}
