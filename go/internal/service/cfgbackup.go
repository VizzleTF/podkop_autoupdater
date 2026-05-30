package service

import (
	"context"

	"github.com/VizzleTF/podkop_autoupdater/go/internal/cfgbackup"
	"github.com/VizzleTF/podkop_autoupdater/go/internal/logger"
	"github.com/VizzleTF/podkop_autoupdater/go/internal/updater"
)

// BackupConfig snapshots the live podkop config under the given version with a
// fresh timestamp. Empty version falls back to the currently installed one.
func (r *Runner) BackupConfig(version string) (string, error) {
	if version == "" {
		version = updater.InstalledVersion()
	}
	path, err := r.cfg.Backup(version, "")
	if err != nil {
		logger.Errf("config backup %s: %v", version, err)
		return "", err
	}
	logger.Logf("Config backed up: %s", path)
	r.pruneBackups()
	return path, nil
}

// ListBackupVersions returns the distinct versions that have a config backup.
func (r *Runner) ListBackupVersions() ([]string, error) {
	return r.cfg.Versions()
}

// ListBackupsForVersion returns the timestamped backups for a version.
func (r *Runner) ListBackupsForVersion(version string) ([]cfgbackup.Entry, error) {
	return r.cfg.ForVersion(version)
}

// HasConfigBackup reports whether any config backup exists for version.
func (r *Runner) HasConfigBackup(version string) bool {
	return r.cfg.HasVersion(version)
}

// DeleteBackup removes a single config backup by id.
func (r *Runner) DeleteBackup(id string) error {
	if err := r.cfg.Delete(id); err != nil {
		logger.Errf("config backup delete %s: %v", id, err)
		return err
	}
	logger.Logf("Config backup deleted: %s", id)
	return nil
}

// RestoreConfig backs up the current config, restores the chosen backup by id,
// restarts podkop, and polls the DNS check. The package is NOT touched — this
// only swaps the config file.
func (r *Runner) RestoreConfig(ctx context.Context, id string) (string, error) {
	cur := updater.InstalledVersion()
	if _, err := r.cfg.Backup(cur, ""); err != nil {
		logger.Errf("config backup before restore: %v", err)
	} else {
		r.pruneBackups()
	}
	if err := r.cfg.RestoreID(id); err != nil {
		logger.Errf("config restore %s: %v", id, err)
		return "Ошибка восстановления конфига: " + err.Error(), err
	}
	label := id
	if e, ok := cfgbackup.Parse(id); ok {
		label = e.Version + " · " + e.Display()
	}
	logger.Logf("Config restored from backup %s, restarting podkop", id)
	if err := restartPodkop(ctx); err != nil {
		logger.Errf("restart after restore: %v", err)
		return "Конфиг восстановлен, но рестарт упал: " + err.Error(), err
	}
	status, _ := dnsCheck(ctx, r.dns)
	logger.Logf("%s", status)
	msg := "Конфиг восстановлен: " + label + "\n" + status
	if !dnsHealthy(status) {
		msg = "⚠️ Конфиг " + label + " восстановлен, но DNS не поднялся\n" + status
	}
	return msg, nil
}
