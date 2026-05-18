// Package service implements podkop restart, fakeip DNS health check, and
// the UpdateRunner used by the telegram bot.
package service

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/VizzleTF/podkop_autoupdater/go/internal/logger"
	"github.com/VizzleTF/podkop_autoupdater/go/internal/selfupdate"
	"github.com/VizzleTF/podkop_autoupdater/go/internal/updater"
)

const (
	DNSServer        = "127.0.0.42:53"
	DNSTestDomain    = "fakeip.podkop.fyi"
	DNSExpectedPfx   = "198.18."
	DNSPollInterval  = 2 * time.Second
	DNSTotalBudget   = 60 * time.Second
	DNSLookupTimeout = 2 * time.Second
)

// Runner implements telegram.UpdateRunner backed by real system operations.
type Runner struct {
	hc      *http.Client
	logPath string
}

func NewRunner(hc *http.Client, logPath string) *Runner {
	return &Runner{hc: hc, logPath: logPath}
}

// RunRestart restarts the podkop service and polls the fakeip DNS check
// until it succeeds (up to DNSTotalBudget).
func (r *Runner) RunRestart(ctx context.Context) (string, error) {
	logger.Logf("Restarting podkop")
	if err := restartPodkop(ctx); err != nil {
		logger.Errf("podkop restart: %v", err)
		return "podkop restart failed", err
	}
	logger.Logf("Podkop restarted, polling DNS")
	status, _ := dnsCheck(ctx)
	logger.Logf("%s", status)
	return "Podkop перезапущен\n" + status, nil
}

// RunUpdate downloads and runs the upstream podkop install.sh, then polls
// the DNS check.
func (r *Runner) RunUpdate(ctx context.Context, target string) (string, error) {
	logger.Logf("Starting update to %s", target)
	out, closeOut := openLogAppend(r.logPath)
	if closeOut != nil {
		defer closeOut()
	}
	if err := updater.RunInstallScript(ctx, r.hc, out); err != nil {
		logger.Errf("install.sh: %v", err)
		return "Ошибка запуска install.sh: " + err.Error(), err
	}
	logger.Logf("install.sh completed, polling DNS")
	status, _ := dnsCheck(ctx)
	logger.Logf("%s", status)
	return "Обновлено до " + target + "\n" + status, nil
}

// RunSelfUpdate downloads the latest podkop_updater binary, swaps it in
// place, and schedules a process exit so procd respawns into the new
// binary. The status is returned for the UI before the exit timer fires.
func (r *Runner) RunSelfUpdate(ctx context.Context) (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "Не удалось определить путь к бинарю", err
	}
	if err := selfupdate.Run(ctx, r.hc, exePath); err != nil {
		return "Ошибка self-update: " + err.Error(), err
	}
	// Give the bot time to push the success message to Telegram before procd
	// respawns us.
	time.AfterFunc(3*time.Second, func() {
		logger.Logf("Self-update: exiting for procd respawn")
		os.Exit(0)
	})
	return "Updater обновлён, перезапуск через 3с...", nil
}

func restartPodkop(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "/etc/init.d/podkop", "restart")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("init.d podkop restart: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// dnsCheck polls DNSTestDomain via DNSServer every DNSPollInterval, returning
// on the first response inside 198.18.0.0/15 (podkop fakeip). Gives up after
// DNSTotalBudget. The returned string is human-readable; error is non-nil
// only on context cancel.
func dnsCheck(ctx context.Context) (string, error) {
	start := time.Now()
	deadline := start.Add(DNSTotalBudget)
	var last string
	for {
		ip, ok := dnsLookup(ctx)
		if ok {
			elapsed := time.Since(start).Round(100 * time.Millisecond)
			return fmt.Sprintf("DNS OK через %s: %s → %s", elapsed, DNSTestDomain, ip), nil
		}
		last = "DNS не отвечает (" + ip + ")"
		if time.Now().After(deadline) {
			return last + fmt.Sprintf(" — таймаут %s", DNSTotalBudget), nil
		}
		t := time.NewTimer(DNSPollInterval)
		select {
		case <-ctx.Done():
			t.Stop()
			return last + " (отменено)", ctx.Err()
		case <-t.C:
		}
	}
}

// dnsLookup performs a single resolution; returns (ip-or-error-string, success).
func dnsLookup(ctx context.Context) (string, bool) {
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(c context.Context, _, _ string) (net.Conn, error) {
			d := &net.Dialer{Timeout: DNSLookupTimeout}
			return d.DialContext(c, "udp", DNSServer)
		},
	}
	c, cancel := context.WithTimeout(ctx, DNSLookupTimeout+500*time.Millisecond)
	defer cancel()
	ips, err := resolver.LookupHost(c, DNSTestDomain)
	if err != nil {
		return err.Error(), false
	}
	for _, ip := range ips {
		if strings.HasPrefix(ip, DNSExpectedPfx) {
			return ip, true
		}
	}
	if len(ips) == 0 {
		return "пустой ответ", false
	}
	return strings.Join(ips, ","), false
}

func openLogAppend(path string) (*os.File, func()) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil
	}
	return f, func() { f.Close() }
}
