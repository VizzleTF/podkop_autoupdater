package updater

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
)

var apkVersionRE = regexp.MustCompile(`[0-9]+\.[0-9]+\.[0-9]+`)

// InstalledVersion reads the locally installed podkop version. Tries the
// podkop CLI first (package-manager-agnostic), then falls back to opkg and
// apk. Returns FallbackVersion if nothing reports it. The returned string
// is normalized (no "v" prefix, no "-N" suffix).
//
// For end-to-end testing of the update flow without downgrading the real
// podkop package, set PODKOP_FAKE_INSTALLED to a semver string (e.g.
// "0.7.0") in the daemon's environment. The override is normalized before
// being returned.
func InstalledVersion() string {
	if fake := os.Getenv("PODKOP_FAKE_INSTALLED"); fake != "" {
		return Normalize(fake)
	}
	if v := readPodkopCLI(); v != "" {
		return Normalize(v)
	}
	if v := readOpkg(); v != "" {
		return Normalize(v)
	}
	if v := readApk(); v != "" {
		return Normalize(v)
	}
	return Normalize(FallbackVersion)
}

func readPodkopCLI() string {
	out, err := exec.Command("podkop", "show_version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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
