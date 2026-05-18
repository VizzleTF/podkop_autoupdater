// Package selfupdate downloads a newer podkop_updater binary, swaps it in
// atomically (with a .bak rollback file), and lets procd respawn the
// daemon. Cleanup of the .bak file happens on the next clean startup.
package selfupdate

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"

	"github.com/VizzleTF/podkop_autoupdater/go/internal/logger"
)

const (
	releaseURLTpl = "https://github.com/VizzleTF/podkop_autoupdater/releases/latest/download/podkop_updater-%s"
	minBinSize    = 512 * 1024 // 512 KB — guard against truncated downloads
)

// ArchName returns the release-asset arch suffix matching the running binary.
// Must stay aligned with the CI matrix in .github/workflows/release.yml.
func ArchName() string {
	switch runtime.GOARCH {
	case "amd64":
		return "amd64"
	case "arm64":
		return "arm64"
	case "arm":
		return "armv7" // CI builds GOARM=7
	case "mipsle":
		return "mipsle"
	case "mips":
		return "mips"
	default:
		return runtime.GOARCH
	}
}

// ReleaseURL returns the URL the running binary would self-update from.
func ReleaseURL() string {
	return fmt.Sprintf(releaseURLTpl, ArchName())
}

// Run downloads the latest release binary for the current arch and atomically
// replaces the running executable. After a successful swap, the caller should
// exit so procd respawns into the new binary. The previous binary is preserved
// at currentExePath + ".bak" for manual rollback.
func Run(ctx context.Context, hc *http.Client, currentExePath string) error {
	if hc == nil {
		hc = http.DefaultClient
	}
	url := ReleaseURL()
	logger.Logf("Self-update: downloading %s", url)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}

	tmpPath := currentExePath + ".new"
	bakPath := currentExePath + ".bak"

	out, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("open tmp: %w", err)
	}
	n, copyErr := io.Copy(out, resp.Body)
	closeErr := out.Close()
	if copyErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("write tmp: %w", copyErr)
	}
	if closeErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close tmp: %w", closeErr)
	}
	if n < minBinSize {
		os.Remove(tmpPath)
		return fmt.Errorf("download too small: %d bytes", n)
	}
	logger.Logf("Self-update: downloaded %d bytes to %s", n, tmpPath)

	// Atomic swap: cur -> bak, new -> cur. Both renames are within the same
	// directory so they're atomic on the same filesystem.
	if err := os.Rename(currentExePath, bakPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("backup current: %w", err)
	}
	if err := os.Rename(tmpPath, currentExePath); err != nil {
		// Best-effort restore.
		if rerr := os.Rename(bakPath, currentExePath); rerr != nil {
			logger.Errf("self-update: restore after swap failure: %v", rerr)
		}
		return fmt.Errorf("install new: %w", err)
	}
	logger.Logf("Self-update: binary swapped (backup at %s)", bakPath)
	return nil
}

// CleanupBackup removes the .bak file left by a previous self-update, if any.
// Call once on successful startup to confirm the new binary is healthy.
func CleanupBackup(currentExePath string) {
	bak := currentExePath + ".bak"
	if _, err := os.Stat(bak); err == nil {
		if err := os.Remove(bak); err != nil {
			logger.Errf("self-update cleanup: %v", err)
		} else {
			logger.Logf("Self-update cleanup: removed %s", bak)
		}
	}
}
