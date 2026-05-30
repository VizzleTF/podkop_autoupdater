// Package telegram wraps github.com/go-telegram/bot for podkop_updater:
// persistent inline menu, callback dispatch, periodic version check.
package telegram

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/VizzleTF/podkop_autoupdater/go/internal/logger"
	"github.com/VizzleTF/podkop_autoupdater/go/internal/service"
)

// UpdateRunner abstracts the update/restart/self-update side effects so the
// bot does not depend on the runner implementation directly.
type UpdateRunner interface {
	RunUpdate(ctx context.Context, targetVersion, tag string) (statusText string, err error)
	RunRestart(ctx context.Context) (statusText string, err error)
	RunSelfUpdate(ctx context.Context) (statusText string, err error)
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
	chatID        int64
	checkInterval time.Duration
	label         string
	selfVer       string
	runner        UpdateRunner
	hc            *http.Client
	dnsCfg        service.DNSConfig
	adminIDs      map[int64]bool // empty = anyone in the chat may issue commands
	autoUpdate    bool
	tiers         TierReporter
	logPath       string
	startTime     time.Time

	b     *bot.Bot
	state *botState
}

type Options struct {
	Token         string
	ChatID        int64
	Label         string // identifier shown in message header (hostname or user-set router label)
	SelfVersion   string // compile-time version of this binary
	HTTPClient    *http.Client
	CheckInterval time.Duration
	Runner        UpdateRunner
	DNSConfig     service.DNSConfig
	AdminIDs      []int64      // Telegram user IDs allowed to issue commands; empty = anyone in the chat
	AutoUpdate    bool         // auto-install new podkop releases on periodic check
	Tiers         TierReporter // live transport state for /status (optional)
	LogPath       string       // daemon log path for /log (optional)
	StartTime     time.Time    // process start, for /status uptime

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
	admins := make(map[int64]bool, len(opts.AdminIDs))
	for _, id := range opts.AdminIDs {
		admins[id] = true
	}
	startTime := opts.StartTime
	if startTime.IsZero() {
		startTime = time.Now()
	}
	tb := &Bot{
		chatID:        opts.ChatID,
		checkInterval: opts.CheckInterval,
		label:         opts.Label,
		selfVer:       opts.SelfVersion,
		runner:        opts.Runner,
		hc:            opts.HTTPClient,
		dnsCfg:        opts.DNSConfig,
		adminIDs:      admins,
		autoUpdate:    opts.AutoUpdate,
		tiers:         opts.Tiers,
		logPath:       opts.LogPath,
		startTime:     startTime,
		state:         newBotState(opts.InitialMenuMID, opts.PersistMenuMID),
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
	t.refreshPodkop(ctx)

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
	tick := time.NewTicker(t.checkInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			logger.Logf("Periodic check")
			_, prevLatest, prevAvailable, _ := t.state.snapshotPodkop()
			prevSelfLatest, prevSelfAvail := t.state.snapshotSelf()
			t.refreshPodkop(ctx)
			t.refreshSelf(ctx)
			_, newLatest, newAvailable, oldMID := t.state.snapshotPodkop()

			if newAvailable && (!prevAvailable || newLatest != prevLatest) {
				if t.autoUpdate {
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
				t.notifySelfUpdate(ctx, newSelfLatest)
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

func (t *Bot) fallbackHandler(_ context.Context, _ *bot.Bot, update *models.Update) {
	// Silently ignore messages and callbacks from other chats.
	if update.Message != nil && update.Message.Chat.ID != t.chatID {
		return
	}
	if update.CallbackQuery != nil && update.CallbackQuery.Message.Message != nil &&
		update.CallbackQuery.Message.Message.Chat.ID != t.chatID {
		return
	}
}
