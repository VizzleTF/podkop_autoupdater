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
	RunUpdate(ctx context.Context, targetVersion string) (statusText string, err error)
	RunRestart(ctx context.Context) (statusText string, err error)
	RunSelfUpdate(ctx context.Context) (statusText string, err error)
}

// Bot is the daemon-side Telegram client. The Bot struct itself holds only
// immutable wiring (chat config, HTTP client, runner, compile-time version,
// DNS probe config); per-session mutable fields live in botState.
type Bot struct {
	chatID        int64
	checkInterval time.Duration
	hostname      string
	selfVer       string
	runner        UpdateRunner
	hc            *http.Client
	dnsCfg        service.DNSConfig

	b     *bot.Bot
	state *botState
}

type Options struct {
	Token         string
	ChatID        int64
	Hostname      string
	SelfVersion   string // compile-time version of this binary
	HTTPClient    *http.Client
	CheckInterval time.Duration
	Runner        UpdateRunner
	DNSConfig     service.DNSConfig

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
	tb := &Bot{
		chatID:        opts.ChatID,
		checkInterval: opts.CheckInterval,
		hostname:      opts.Hostname,
		selfVer:       opts.SelfVersion,
		runner:        opts.Runner,
		hc:            opts.HTTPClient,
		dnsCfg:        opts.DNSConfig,
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
			logger.Logf("Periodic podkop check")
			_, prevLatest, prevAvailable, _ := t.state.snapshotPodkop()
			t.refreshPodkop(ctx)
			_, newLatest, newAvailable, oldMID := t.state.snapshotPodkop()

			if !newAvailable {
				continue
			}
			// Notify when newly available, or when latest version changed.
			if !prevAvailable || newLatest != prevLatest {
				if oldMID != 0 {
					_ = t.deleteMessage(ctx, oldMID)
				}
				t.state.setMenuID(0)
				if err := t.sendUpdatePodkopMenu(ctx); err != nil {
					logger.Errf("send periodic update menu: %v", err)
				}
			}
		}
	}
}

func (t *Bot) chatIDStr() string {
	return strconv.FormatInt(t.chatID, 10)
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
