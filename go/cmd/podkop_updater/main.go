// Package main is the entrypoint for the podkop_updater daemon.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/VizzleTF/podkop_autoupdater/go/internal/config"
	"github.com/VizzleTF/podkop_autoupdater/go/internal/logger"
	"github.com/VizzleTF/podkop_autoupdater/go/internal/selfupdate"
	"github.com/VizzleTF/podkop_autoupdater/go/internal/service"
	"github.com/VizzleTF/podkop_autoupdater/go/internal/telegram"
	"github.com/VizzleTF/podkop_autoupdater/go/internal/transport"
)

// version is overridden at build time via -ldflags="-X main.version=...".
var version = "0.1.0-dev"

const (
	logPath           = "/tmp/podkop_update.log"
	logMaxLines       = 200
	logKeepLines      = 100
	lockPath          = "/tmp/podkop_updater.pid"
	fallbackHostname  = "router"
	longPollTimeout   = 60 * time.Second
	clientTotalBudget = longPollTimeout + 15*time.Second
)

// defaultEmergencyIPs is the compiled-in fallback list of api.telegram.org
// addresses. Used only when no UCI override exists and DoH discovery hasn't
// run yet. See internal/transport/discovery.go for the runtime refresh path.
var defaultEmergencyIPs = []string{
	"149.154.167.51",  // DC2 Amsterdam (bot API primary)
	"149.154.167.220", // legacy bot API endpoint
	"149.154.167.91",  // DC4 Amsterdam
	"91.108.56.130",   // DC5 Singapore
}

const (
	telegramHost      = "api.telegram.org"
	ipRefreshInterval = 24 * time.Hour
	ipRefreshInitial  = 30 * time.Second
)

func main() {
	if len(os.Args) < 2 {
		// Default to daemon when invoked without arguments (procd convention).
		os.Exit(runDaemon())
	}
	switch os.Args[1] {
	case "--daemon", "daemon":
		os.Exit(runDaemon())
	case "version", "--version", "-v":
		fmt.Println(version)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown argument: %s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `podkop_updater %s

Usage:
  podkop_updater [--daemon]   Run as persistent Telegram bot (procd default)
  podkop_updater version      Print version
  podkop_updater help         Print this help
`, version)
}

func runDaemon() int {
	if err := logger.Init(logPath, logMaxLines, logKeepLines); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := acquireLock(lockPath); err != nil {
		logger.Errf("lock: %v", err)
		return 0 // exit cleanly on lock conflict (matches bash behavior)
	}
	defer releaseLock(lockPath)

	cfg, err := config.Load()
	if err != nil {
		logger.Errf("config: %v", err)
		return 1
	}
	logger.Logf("Daemon started, check interval: %s, version: %s", cfg.CheckInterval, version)

	// Clean up self-update leftovers from the previous binary, if any.
	if exe, err := os.Executable(); err == nil {
		selfupdate.CleanupBackup(exe)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	socks := service.DetectSocksAddr()
	logger.Logf("SOCKS5 endpoint: %s", socks)

	startIPs := cfg.EmergencyIPs
	if len(startIPs) == 0 {
		startIPs = defaultEmergencyIPs
	}
	tt := transport.New(socks, telegramHost, startIPs)
	logger.Logf("Transport tiers: %v", tt.Tiers())

	hc := &http.Client{
		Timeout:   clientTotalBudget,
		Transport: tt,
	}

	// Separate HTTP client for the update flow: install.sh fetch can take
	// longer than a long-poll window. Reuses the same tiered transport.
	updateHC := &http.Client{
		Timeout:   5 * time.Minute,
		Transport: tt,
	}
	dnsCfg := service.LoadDNSConfig()
	runner := service.NewRunner(updateHC, logPath, dnsCfg)

	// DoH discovery uses a plain HTTP client; we don't want to route DNS
	// fallback queries through the same broken path.
	dohHC := &http.Client{Timeout: 10 * time.Second}
	go runEmergencyIPRefresh(ctx, dohHC, tt)

	tb, err := telegram.New(telegram.Options{
		Token:         cfg.BotToken,
		ChatID:        cfg.ChatID,
		Hostname:      readHostname(),
		SelfVersion:   version,
		HTTPClient:    hc,
		CheckInterval: cfg.CheckInterval,
		Runner:        runner,
		DNSConfig:     dnsCfg,
	})
	if err != nil {
		logger.Errf("telegram: %v", err)
		return 1
	}

	if err := tb.Start(ctx); err != nil {
		logger.Errf("daemon: %v", err)
		return 1
	}
	logger.Logf("Daemon stopping (context cancelled)")
	return 0
}

func readHostname() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	if b, err := os.ReadFile("/proc/sys/kernel/hostname"); err == nil {
		if s := strings.TrimRight(string(b), "\n\r"); s != "" {
			return s
		}
	}
	return fallbackHostname
}

// acquireLock writes our PID to lockPath, refusing if another live process
// owns it.
func acquireLock(path string) error {
	if data, err := os.ReadFile(path); err == nil {
		if pid, err := strconv.Atoi(strings.TrimRight(string(data), "\n\r ")); err == nil && pid > 0 {
			if processAlive(pid) {
				return errors.New("another instance is running (lock: " + path + ")")
			}
		}
	}
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644)
}

func releaseLock(path string) {
	_ = os.Remove(path)
}

func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, signal 0 returns nil if the process exists, ESRCH otherwise.
	return p.Signal(syscall.Signal(0)) == nil
}

// runEmergencyIPRefresh periodically queries DoH for fresh api.telegram.org
// IPs, applies them in-memory, and persists them to UCI for future starts.
// Skips silently if discovery fails (we keep whatever tiers we had).
func runEmergencyIPRefresh(ctx context.Context, hc *http.Client, tt *transport.TieredTransport) {
	timer := time.NewTimer(ipRefreshInitial)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		refreshEmergencyIPsOnce(ctx, hc, tt)
		timer.Reset(ipRefreshInterval)
	}
}

func refreshEmergencyIPsOnce(ctx context.Context, hc *http.Client, tt *transport.TieredTransport) {
	endpoints := dohEndpointsFromPodkop()
	logger.Logf("IP refresh: querying DoH %v for %s", endpoints, telegramHost)
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	discovered, err := transport.Discover(dctx, hc, telegramHost, endpoints)
	if err != nil {
		logger.Errf("IP refresh: %v", err)
		return
	}
	merged := mergeIPs(defaultEmergencyIPs, discovered)
	logger.Logf("IP refresh: discovered=%v merged=%v", discovered, merged)
	tt.RebuildEmergency(merged)
	if err := config.SaveEmergencyIPs(merged); err != nil {
		logger.Errf("IP refresh persist: %v", err)
	}
}

// dohEndpointsFromPodkop returns the DoH endpoint configured in podkop's
// UCI; falls back to the compiled defaults if podkop isn't using DoH.
func dohEndpointsFromPodkop() []string {
	if ep := service.PodkopDoHEndpoint(); ep != "" {
		return []string{ep}
	}
	return transport.DefaultDoHEndpoints
}

// mergeIPs returns the deduplicated, sorted union of two IP slices.
func mergeIPs(a, b []string) []string {
	set := make(map[string]struct{}, len(a)+len(b))
	for _, ip := range a {
		set[ip] = struct{}{}
	}
	for _, ip := range b {
		set[ip] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for ip := range set {
		out = append(out, ip)
	}
	sort.Strings(out)
	return out
}
