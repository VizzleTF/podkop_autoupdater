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
	// watchInterval is how often the watchdog samples the transport's passive
	// liveness and degraded state. Cheap (in-memory read), so it ticks often.
	watchInterval = 30 * time.Second
	// watchProbeTimeout bounds the single on-demand getMe confirmation probe.
	watchProbeTimeout = 15 * time.Second
	// watchLivenessStale is how long the data path may go without a successful
	// round-trip before it is suspect. Must exceed the long-poll window (60s)
	// plus the client slack (15s) so a normally-cycling idle long-poll — which
	// completes one round-trip per window — never looks stale. A stale value
	// means the real path (not a synthetic probe) has stopped completing.
	watchLivenessStale = 100 * time.Second
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
// Liveness is derived from the *actual* data path, not a synthetic poll: a
// healthy long-poll completes one round-trip per poll window, so the transport
// advances LastSuccess continuously. The watchdog only fires an on-demand getMe
// probe when LastSuccess is stale (> watchLivenessStale), to confirm a genuine
// outage before alerting. This measures the connection that carries updates
// (not a fresh probe connection) and costs zero extra API calls while healthy.
//
// Two independent episodes are tracked, each edge-triggered:
//
//   - Outage: no successful round-trip within watchLivenessStale AND the
//     confirmation getMe fails. The "entered" alert can't be delivered (we're
//     offline), so only the recovery message is sent, reporting how long the
//     path was down (measured from the last successful round-trip). Recovery is
//     detected passively — when the long-poll resumes, LastSuccess advances.
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
		outage      bool
		outageStart time.Time // last-alive time when the outage was declared

		degraded       bool // last sampled degraded state
		degradeAlerted bool // whether the "entered degradation" alert was delivered
	)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		// Passive liveness first: only spend an active probe if the real path
		// has gone quiet longer than a healthy long-poll ever would.
		reachable := true
		if time.Since(t.tiers.LastSuccess()) > watchLivenessStale {
			pctx, cancel := context.WithTimeout(ctx, watchProbeTimeout)
			_, err := t.b.GetMe(pctx)
			cancel()
			reachable = err == nil
		}

		// --- Outage episode ---
		switch {
		case !outage && !reachable:
			outage = true
			outageStart = t.tiers.LastSuccess()
			logger.Errf("Watchdog: Telegram unreachable (no round-trip for %s, probe failed)",
				time.Since(outageStart).Round(time.Second))
		case outage && reachable:
			outage = false
			dur := time.Since(outageStart).Round(time.Second)
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
