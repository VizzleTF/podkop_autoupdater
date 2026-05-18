package updater

import (
	"regexp"
	"strconv"
	"strings"
)

var versionRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

const FallbackVersion = "0.0.0-1"

// Normalize strips a leading "v" and trims trailing "-N" packaging suffixes.
func Normalize(v string) string {
	v = strings.TrimPrefix(v, "v")
	if i := strings.Index(v, "-"); i >= 0 {
		v = v[:i]
	}
	return v
}

// ValidSemver returns true for "X.Y.Z" form (matches bash VERSION_PATTERN).
func ValidSemver(v string) bool {
	return versionRE.MatchString(v)
}

// IsNewer reports whether latest > installed under simple X.Y.Z comparison.
// Both inputs are normalized first.
func IsNewer(installed, latest string) bool {
	a := parseSegments(Normalize(installed))
	b := parseSegments(Normalize(latest))
	for i := 0; i < 3; i++ {
		if b[i] > a[i] {
			return true
		}
		if b[i] < a[i] {
			return false
		}
	}
	return false
}

func parseSegments(v string) [3]int {
	var out [3]int
	parts := strings.SplitN(v, ".", 3)
	for i := 0; i < len(parts) && i < 3; i++ {
		n, _ := strconv.Atoi(parts[i])
		out[i] = n
	}
	return out
}
