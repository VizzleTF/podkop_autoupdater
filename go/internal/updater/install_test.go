package updater

import "testing"

func TestInstallScriptURLForTag(t *testing.T) {
	cases := []struct {
		tag  string
		want string
	}{
		{"", InstallScriptURL},
		{"v0.7.0", "https://raw.githubusercontent.com/itdoginfo/podkop/refs/tags/v0.7.0/install.sh"},
		{"0.7.0", "https://raw.githubusercontent.com/itdoginfo/podkop/refs/tags/0.7.0/install.sh"},
	}
	for _, c := range cases {
		if got := installScriptURLForTag(c.tag); got != c.want {
			t.Errorf("tag %q: got %s want %s", c.tag, got, c.want)
		}
	}
}
