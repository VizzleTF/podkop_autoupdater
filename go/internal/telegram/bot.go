// Package telegram wraps github.com/go-telegram/bot for podkop_updater:
// persistent inline menu, callback dispatch, periodic version check.
package telegram

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/VizzleTF/podkop_autoupdater/go/internal/logger"
)

// UpdateRunner abstracts the update/restart/self-update side effects so the
// bot does not depend on the runner implementation directly.
type UpdateRunner interface {
	RunUpdate(ctx context.Context, targetVersion string) (statusText string, err error)
	RunRestart(ctx context.Context) (statusText string, err error)
	RunSelfUpdate(ctx context.Context) (statusText string, err error)
}

// Bot is the daemon-side Telegram client.
type Bot struct {
	chatID        int64
	checkInterval time.Duration
	hostname      string
	selfVer       string
	runner        UpdateRunner
	hc            *http.Client

	b *bot.Bot

	mu sync.Mutex
	// Single tracked message; UI mutates this one or recreates it on auto-find.
	menuMID int

	// podkop state
	installedVer    string
	latestVer       string
	updateAvailable bool

	// updater (self) state
	selfLatest          string
	selfUpdateAvailable bool
}

type Options struct {
	Token         string
	ChatID        int64
	Hostname      string
	SelfVersion   string // compile-time version of this binary
	HTTPClient    *http.Client
	CheckInterval time.Duration
	Runner        UpdateRunner
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

	t.mu.Lock()
	available := t.updateAvailable
	t.mu.Unlock()

	if available {
		if err := t.sendUpdatePodkopMenu(ctx); err != nil {
			logger.Errf("send initial update menu: %v", err)
		}
	} else {
		if err := t.sendDefaultMenu(ctx); err != nil {
			logger.Errf("send initial default menu: %v", err)
		}
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
			t.mu.Lock()
			prevLatest := t.latestVer
			prevAvailable := t.updateAvailable
			t.mu.Unlock()

			t.refreshPodkop(ctx)

			t.mu.Lock()
			newLatest := t.latestVer
			newAvailable := t.updateAvailable
			oldMID := t.menuMID
			t.mu.Unlock()

			if !newAvailable {
				continue
			}
			// Notify when newly available, or when latest version changed.
			if !prevAvailable || newLatest != prevLatest {
				if oldMID != 0 {
					_ = t.deleteMessage(ctx, oldMID)
				}
				t.mu.Lock()
				t.menuMID = 0
				t.mu.Unlock()
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

func (t *Bot) fallbackHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	// Silently ignore messages and callbacks from other chats.
	if update.Message != nil && update.Message.Chat.ID != t.chatID {
		return
	}
	if update.CallbackQuery != nil && update.CallbackQuery.Message.Message != nil &&
		update.CallbackQuery.Message.Message.Chat.ID != t.chatID {
		return
	}
}
