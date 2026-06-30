package telegram

import (
	"context"
	"fmt"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/VizzleTF/podkop_autoupdater/go/internal/logger"
)

const (
	// watchInterval is how often the watchdog probes Telegram reachability and
	// samples the transport's degraded state.
	watchInterval = 60 * time.Second
	// watchProbeTimeout bounds a single getMe reachability probe.
	watchProbeTimeout = 15 * time.Second
	// watchOutageThreshold is how many consecutive failed probes declare a full
	// outage. Avoids alerting on a single transient blip.
	watchOutageThreshold = 2
)

// sendAlert posts a standalone (untracked) message and returns the send error,
// so callers can tell whether the alert actually reached the user. Used for
// watchdog notices that must not disturb the tracked menu.
func (t *Bot) sendAlert(ctx context.Context, text string) error {
	_, err := t.b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    t.chatIDStr(),
		Text:      t.withLabel(text),
		ParseMode: models.ParseModeHTML,
	})
	return err
}

// transportWatch proactively alerts the user when the bot's path to Telegram
// degrades or breaks, and again when it recovers — Telegram never pushes a
// notification for a silent fallback, so without this the user only learns the
// link broke when a command goes unanswered.
//
// Two independent episodes are tracked, each edge-triggered:
//
//   - Outage: getMe fails watchOutageThreshold times in a row. The "entered"
//     alert can't be delivered (we're offline), so only the recovery message is
//     sent, reporting how long the outage lasted.
//   - Degradation: traffic fell through to a non-preferred tier (e.g. SOCKS5
//     down, now on pinned IPs) while Telegram is still reachable. Both the
//     enter and recover alerts are deliverable. Skipped entirely for the
//     default single-tier (pinned-IP-only) setup, where Degraded is always
//     false.
func (t *Bot) transportWatch(ctx context.Context) {
	if t.tiers == nil {
		return
	}
	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()

	var (
		probeFails  int
		firstFailAt time.Time
		outage      bool

		degraded       bool // last sampled degraded state
		degradeAlerted bool // whether the "entered degradation" alert was delivered
	)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		pctx, cancel := context.WithTimeout(ctx, watchProbeTimeout)
		_, err := t.b.GetMe(pctx)
		cancel()
		reachable := err == nil

		// --- Outage episode ---
		if reachable {
			probeFails = 0
		} else {
			if probeFails == 0 {
				firstFailAt = time.Now()
			}
			probeFails++
		}
		switch {
		case !outage && probeFails >= watchOutageThreshold:
			outage = true
			logger.Errf("Watchdog: Telegram unreachable (%d consecutive probe fails)", probeFails)
		case outage && reachable:
			outage = false
			dur := time.Since(firstFailAt).Round(time.Second)
			logger.Logf("Watchdog: Telegram reachable again after %s", dur)
			_ = t.sendAlert(ctx, fmt.Sprintf(
				"✅ Связь с Telegram восстановлена\nБыл недоступен ~%s · канал: %s",
				dur, orDash(t.tiers.CurrentTier())))
		}

		// Degradation logic only makes sense while we can actually reach
		// Telegram (and send alerts); during an outage the tier state is moot.
		if !reachable {
			continue
		}

		// --- Degradation episode ---
		nowDeg := t.tiers.Degraded()
		switch {
		case nowDeg && !degraded:
			degraded = true
			logger.Logf("Watchdog: transport degraded, on %s", t.tiers.CurrentTier())
			degradeAlerted = t.sendAlert(ctx, fmt.Sprintf(
				"⚠️ Основной путь до Telegram недоступен — иду через резерв\nКанал: %s",
				orDash(t.tiers.CurrentTier()))) == nil
		case !nowDeg && degraded:
			degraded = false
			logger.Logf("Watchdog: transport recovered, on %s", t.tiers.CurrentTier())
			if degradeAlerted {
				_ = t.sendAlert(ctx, fmt.Sprintf(
					"✅ Основной путь до Telegram восстановлен\nКанал: %s",
					orDash(t.tiers.CurrentTier())))
			}
			degradeAlerted = false
		}
	}
}
