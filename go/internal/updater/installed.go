package updater

import (
	"os/exec"
	"regexp"
	"strings"
)

var apkVersionRE = regexp.MustCompile(`[0-9]+\.[0-9]+\.[0-9]+`)

// InstalledVersion reads the locally installed podkop version via opkg or
// apk. Returns FallbackVersion if neither package manager reports it.
// The returned string is normalized (no "v" prefix, no "-N" suffix).
func InstalledVersion() string {
	if v := readOpkg(); v != "" {
		return Normalize(v)
	}
	if v := readApk(); v != "" {
		return Normalize(v)
	}
	return Normalize(FallbackVersion)
}

func readOpkg() string {
	out, err := exec.Command("opkg", "info", "podkop").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "Version:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Version:"))
		}
	}
	return ""
}

func readApk() string {
	out, err := exec.Command("apk", "info", "podkop").Output()
	if err != nil {
		return ""
	}
	return apkVersionRE.FindString(string(out))
}

// Update fetches and runs the upstream install.sh. Implementation deferred
// to phase 3.
func Update() error {
	panic("not implemented (phase 3)")
}
