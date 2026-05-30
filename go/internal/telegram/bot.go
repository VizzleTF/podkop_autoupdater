// Package telegram wraps github.com/go-telegram/bot for podkop_updater:
// persistent inline menu, callback dispatch, periodic version check.
package telegram

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/VizzleTF/podkop_autoupdater/go/internal/cfgbackup"
	"github.com/VizzleTF/podkop_autoupdater/go/internal/logger"
	"github.com/VizzleTF/podkop_autoupdater/go/internal/service"
)

// UpdateRunner abstracts the update/restart/self-update side effects so the
// bot does not depend on the runner implementation directly.
type UpdateRunner interface {
	RunUpdate(ctx context.Context, targetVersion, tag string) (statusText string, err error)
	RunDowngrade(ctx context.Context, targetVersion, tag string) (statusText string, err error)
	RunRestart(ctx context.Context) (statusText string, err error)
	RunSelfUpdate(ctx context.Context) (statusText string, err error)

	BackupConfig(version string) (path string, err error)
	ListBackupVersions() ([]string, error)
	ListBackupsForVersion(version string) ([]cfgbackup.Entry, error)
	HasConfigBackup(version string) bool
	RestoreConfig(ctx context.Context, backupID string) (statusText string, err error)
	DeleteBackup(backupID string) error
	SetBackupKeep(n int)
}

// TierReporter exposes the live transport state for the /status command,
// without the telegram package depending on the concrete transport type.
type TierReporter interface {
	CurrentTier() string
	Tiers() []string
}

// Bot is the daemon-side Telegram client. The Bot struct itself holds only
// immutable wiring (chat config, HTTP client, runner, compile-time version,
// DNS probe config); per-session mutable fields live in botState.
type Bot struct {
	chatID     int64
	selfVer    string
	runner     UpdateRunner
	hc         *http.Client
	dnsCfg     service.DNSConfig
	tiers      TierReporter
	logPath    string
	startTime  time.Time
	refreshIPs func(context.Context) // forces an emergency-IP DoH refresh (optional)

	set        *botSettings
	settingsCh chan struct{} // wakes periodicCheck when the interval changes

	b     *bot.Bot
	state *botState
}

type Options struct {
	Token          string
	ChatID         int64
	Label          string // identifier shown in message header (hostname or user-set router label)
	SelfVersion    string // compile-time version of this binary
	HTTPClient     *http.Client
	CheckInterval  time.Duration
	Runner         UpdateRunner
	DNSConfig      service.DNSConfig
	AdminIDs       []int64               // Telegram user IDs allowed to issue commands; empty = anyone in the chat
	AutoUpdate     bool                  // auto-install new podkop releases on periodic check
	AutoUpdateSelf bool                  // auto-install new updater releases on periodic check
	BackupKeep     int                   // config-backup retention (0 = unlimited)
	Tiers          TierReporter          // live transport state for /status (optional)
	LogPath        string                // daemon log path for /log (optional)
	StartTime      time.Time             // process start, for /status uptime
	Settings       SettingsStore         // persists runtime settings edits (optional)
	RefreshIPs     func(context.Context) // forces an emergency-IP DoH refresh (optional)

	// InitialMenuMID is the previously persisted Telegram menu message id
	// (0 if none). When non-zero, Start edits that message instead of
	// posting a new menu.
	InitialMenuMID int
	// PersistMenuMID is called whenever the tracked menu id changes so the
	// next daemon start can pick up where we left off. nil disables persist.
	PersistMenuMID func(int)
}

// New constructs a Bot and registers callback handlers. Call Start to begin
// long-polling.
func New(opts Options) (*Bot, error) {
	if opts.Token == "" {
		return nil, fmt.Errorf("telegram: empty token")
	}
	if opts.ChatID == 0 {
		return nil, fmt.Errorf("telegram: empty chat id")
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = http.DefaultClient
	}
	if opts.CheckInterval == 0 {
		opts.CheckInterval = 6 * time.Hour
	}
	checkHours := int(opts.CheckInterval.Hours())
	if checkHours <= 0 {
		checkHours = 6
	}
	startTime := opts.StartTime
	if startTime.IsZero() {
		startTime = time.Now()
	}
	tb := &Bot{
		chatID:     opts.ChatID,
		selfVer:    opts.SelfVersion,
		runner:     opts.Runner,
		hc:         opts.HTTPClient,
		dnsCfg:     opts.DNSConfig,
		tiers:      opts.Tiers,
		logPath:    opts.LogPath,
		startTime:  startTime,
		refreshIPs: opts.RefreshIPs,
		settingsCh: make(chan struct{}, 1),
		set:        newBotSettings(opts.AutoUpdate, opts.AutoUpdateSelf, checkHours, opts.Label, opts.AdminIDs, opts.BackupKeep, opts.Settings),
		state:      newBotState(opts.InitialMenuMID, opts.PersistMenuMID),
	}
	tb.set.onInterval = func() {
		select {
		case tb.settingsCh <- struct{}{}:
		default:
		}
	}
	if opts.Runner != nil {
		opts.Runner.SetBackupKeep(opts.BackupKeep)
		tb.set.onBackupKeep = func(n int) { opts.Runner.SetBackupKeep(n) }
	}
	b, err := bot.New(opts.Token,
		bot.WithHTTPClient(60*time.Second+15*time.Second, opts.HTTPClient),
		bot.WithDefaultHandler(tb.fallbackHandler),
	)
	if err != nil {
		return nil, fmt.Errorf("telegram: %w", err)
	}
	tb.b = b
	tb.registerHandlers()
	return tb, nil
}

// Start blocks until ctx is cancelled. It performs an initial getMe check,
// runs the first version check, sends the initial menu, then runs the
// long-poll loop alongside a periodic-check ticker.
func (t *Bot) Start(ctx context.Context) error {
	me, err := t.b.GetMe(ctx)
	if err != nil {
		return fmt.Errorf("telegram: getMe: %w", err)
	}
	logger.Logf("Telegram bot connected as @%s", me.Username)

	t.publishCommands(ctx)
	t.refreshAll(ctx)

	var sendErr error
	if t.state.updateReady() {
		sendErr = t.sendUpdatePodkopMenu(ctx)
	} else {
		sendErr = t.sendDefaultMenu(ctx)
	}
	if sendErr != nil {
		logger.Errf("send initial menu: %v", sendErr)
	}

	go t.periodicCheck(ctx)
	t.b.Start(ctx)
	return nil
}

// periodicCheck triggers a podkop version check every checkInterval. When a
// new version appears (compared to what was previously shown), it deletes
// the current menu message and sends a fresh one with the Обновить button.
func (t *Bot) periodicCheck(ctx context.Context) {
	timer := time.NewTimer(t.set.CheckInterval())
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.settingsCh:
			// Interval changed via settings — restart the wait with the new
			// value instead of firing a check now.
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(t.set.CheckInterval())
		case <-timer.C:
			timer.Reset(t.set.CheckInterval())
			logger.Logf("Periodic check")
			_, prevLatest, prevAvailable, _ := t.state.snapshotPodkop()
			prevSelfLatest, prevSelfAvail := t.state.snapshotSelf()
			t.refreshAll(ctx)
			_, newLatest, newAvailable, oldMID := t.state.snapshotPodkop()

			if newAvailable && (!prevAvailable || newLatest != prevLatest) {
				if t.set.AutoUpdate() {
					t.autoUpdatePodkop(ctx, oldMID)
				} else {
					// Notify: delete the stale menu and post a fresh one so
					// Telegram actually surfaces a notification (edits don't).
					if oldMID != 0 {
						_ = t.deleteMessage(ctx, oldMID)
					}
					t.state.setMenuID(0)
					if err := t.sendUpdatePodkopMenu(ctx); err != nil {
						logger.Errf("send periodic update menu: %v", err)
					}
				}
			}

			// Self-update notification (notify-only; never auto-installs the
			// updater itself, so a bad self-release can't silently brick).
			newSelfLatest, newSelfAvail := t.state.snapshotSelf()
			if newSelfAvail && (!prevSelfAvail || newSelfLatest != prevSelfLatest) {
				if t.set.AutoUpdateSelf() {
					t.autoSelfUpdate(ctx)
				} else {
					t.notifySelfUpdate(ctx, newSelfLatest)
				}
			}
		}
	}
}

func (t *Bot) chatIDStr() string {
	return strconv.FormatInt(t.chatID, 10)
}

// publishCommands tells Telegram which slash commands the bot understands
// so they show up in the "/" suggestions in chat. Called at every startup
// (idempotent) so the list always matches what registerHandlers wires up,
// overwriting stale lists left by previous bot versions or BotFather.
func (t *Bot) publishCommands(ctx context.Context) {
	_, err := t.b.SetMyCommands(ctx, &bot.SetMyCommandsParams{
		Commands: []models.BotCommand{
			{Command: "menu", Description: "Открыть меню"},
			{Command: "start", Description: "Запуск"},
			{Command: "check_podkop", Description: "Проверить podkop"},
			{Command: "check_self", Description: "Проверить updater"},
			{Command: "check_dns", Description: "Проверить DNS"},
			{Command: "restart", Description: "Перезагрузить podkop"},
			{Command: "status", Description: "Статус демона"},
			{Command: "log", Description: "Последние строки лога"},
		},
	})
	if err != nil {
		logger.Errf("setMyCommands: %v", err)
	}
}

func (t *Bot) fallbackHandler(ctx context.Context, _ *bot.Bot, update *models.Update) {
	// Silently ignore messages and callbacks from other chats.
	if update.CallbackQuery != nil && update.CallbackQuery.Message.Message != nil &&
		update.CallbackQuery.Message.Message.Chat.ID != t.chatID {
		return
	}
	msg := update.Message
	if msg == nil || msg.Chat.ID != t.chatID {
		return
	}
	// A plain text reply may be the input the settings menu is waiting for.
	if msg.Text != "" && !strings.HasPrefix(msg.Text, "/") {
		if msg.From != nil && !t.set.Allowed(msg.From.ID) {
			return
		}
		t.handleAwaitedInput(ctx, msg.Text)
	}
}
