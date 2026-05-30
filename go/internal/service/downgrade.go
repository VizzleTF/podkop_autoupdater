package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/VizzleTF/podkop_autoupdater/go/internal/logger"
	"github.com/VizzleTF/podkop_autoupdater/go/internal/updater"
)

// RunDowngrade installs a specific older podkop release by downloading that
// tag's package assets straight from the GitHub release (same source podkop's
// own install.sh uses) and installing them with the package manager's
// downgrade path. Then restarts podkop and polls the DNS health check.
func (r *Runner) RunDowngrade(ctx context.Context, target, tag string) (string, error) {
	before := updater.InstalledVersion()
	isAPK := pkgIsAPK()
	logger.Logf("Downgrade %s → %s (tag %q, apk=%v)", before, target, tag, isAPK)

	// Snapshot the current config before changing anything, so the user can
	// come back to it. Best-effort — a backup failure must not block rollback.
	if _, err := r.cfg.Backup(before, ""); err != nil {
		logger.Errf("downgrade: backup current config: %v", err)
	} else {
		logger.Logf("Downgrade: backed up current config (%s)", before)
	}

	urls, err := updater.AssetURLsForTag(ctx, r.hc, tag, isAPK)
	if err != nil {
		logger.Errf("downgrade asset lookup: %v", err)
		return "Не нашёл пакеты для версии " + target, err
	}

	dir, err := os.MkdirTemp("", "podkop_dg_*")
	if err != nil {
		return "Не удалось создать временную папку", err
	}
	defer os.RemoveAll(dir)

	var files []string
	for _, u := range urls {
		path, derr := downloadTo(ctx, r.hc, u, dir)
		if derr != nil {
			logger.Errf("downgrade download %s: %v", u, derr)
			return "Ошибка скачивания пакета: " + derr.Error(), derr
		}
		files = append(files, path)
	}
	logger.Logf("Downgrade: downloaded %d packages", len(files))

	// Restore the target version's newest config backup if we have one, so the
	// downgraded package runs against config it understands.
	restored := false
	if id, ok := r.cfg.LatestIDForVersion(target); ok {
		if err := r.cfg.RestoreID(id); err != nil {
			logger.Errf("downgrade: restore config %s: %v", id, err)
		} else {
			restored = true
			logger.Logf("Downgrade: restored config backup %s", id)
		}
	}

	if err := installPackages(ctx, files, isAPK); err != nil {
		logger.Errf("downgrade install: %v", err)
		return "Ошибка установки пакетов: " + err.Error(), err
	}

	logger.Logf("Downgrade install done, restarting podkop")
	if err := restartPodkop(ctx); err != nil {
		logger.Errf("downgrade restart: %v", err)
		return "Пакеты установлены, но рестарт упал: " + err.Error(), err
	}
	status, _ := dnsCheck(ctx, r.dns)
	logger.Logf("%s", status)

	after := updater.InstalledVersion()
	cfgNote := ""
	if restored {
		cfgNote = " (конфиг восстановлен)"
	}
	msg := "Откат до " + after + cfgNote + "\n" + status
	if updater.Normalize(after) != updater.Normalize(target) {
		logger.Errf("post-downgrade version mismatch: target=%s installed=%s", target, after)
		msg = "⚠️ Установлена версия " + after + " (ожидалась " + target + ")\n" + status
	} else if !dnsHealthy(status) {
		msg = "⚠️ Откат до " + after + cfgNote + ", но DNS не поднялся\n" + status
	}
	return msg, nil
}

// pkgIsAPK reports whether this system uses apk (OpenWrt 24.10+/ImmortalWrt)
// rather than opkg.
func pkgIsAPK() bool {
	_, err := exec.LookPath("apk")
	return err == nil
}

// installPackages installs the given local package files, allowing a
// downgrade. apk downgrades automatically when handed an older local file;
// opkg needs --force-downgrade. Untrusted is required because podkop ships no
// signatures (same as its install.sh).
func installPackages(ctx context.Context, files []string, isAPK bool) error {
	var cmd *exec.Cmd
	if isAPK {
		args := append([]string{"add", "--allow-untrusted"}, files...)
		cmd = exec.CommandContext(ctx, "apk", args...)
	} else {
		args := append([]string{"install", "--force-downgrade", "--force-reinstall"}, files...)
		cmd = exec.CommandContext(ctx, "opkg", args...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// downloadTo fetches url into dir, returning the written file path. The
// filename is the URL's last path segment.
func downloadTo(ctx context.Context, hc *http.Client, url, dir string) (string, error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	name := filepath.Base(url)
	if name == "" || name == "." || name == "/" {
		return "", fmt.Errorf("bad asset name in %s", url)
	}
	path := filepath.Join(dir, name)
	out, err := os.Create(path)
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(out, resp.Body)
	closeErr := out.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return path, nil
}
