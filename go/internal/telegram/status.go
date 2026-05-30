package telegram

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/VizzleTF/podkop_autoupdater/go/internal/logger"
)

// allowTextCmd is the read-only counterpart to startTextCommand: it enforces
// the chat + admin ACL and logs, but does NOT reset the tracked menu id
// (status/log are informational and must not strand the menu).
func (t *Bot) allowTextCmd(update *models.Update, name string) bool {
	if update.Message == nil || update.Message.Chat.ID != t.chatID {
		return false
	}
	if update.Message.From != nil && !t.allowedUser(update.Message.From.ID) {
		logger.Logf("Text command %s denied for user %d", name, update.Message.From.ID)
		return false
	}
	logger.Logf("Text command: %s", name)
	return true
}

// sendText posts a standalone (untracked) message. Used for /status, /log and
// busy notices that should not disturb the tracked menu.
func (t *Bot) sendText(ctx context.Context, text string) {
	_, err := t.b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    t.chatIDStr(),
		Text:      t.withLabel(text),
		ParseMode: models.ParseModeHTML,
	})
	if err != nil {
		logger.Errf("sendText: %v", err)
	}
}

// onTextStatus: /status — снимок состояния демона.
func (t *Bot) onTextStatus(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.allowTextCmd(update, "/status") {
		return
	}
	t.sendText(ctx, t.statusText())
}

// onTextLog: /log — последние строки лога.
func (t *Bot) onTextLog(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.allowTextCmd(update, "/log") {
		return
	}
	tail := logTail(t.logPath, 25)
	if tail == "" {
		t.sendText(ctx, "Лог пуст или недоступен")
		return
	}
	t.sendText(ctx, "<b>Лог</b>\n<pre>"+escapePre(tail)+"</pre>")
}

// onStatus: 📊 Статус (callback) — redraw the dashboard card from cache
// (instant, no network). The 🔍 Проверить button is the networked refresh.
func (t *Bot) onStatus(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_status") {
		return
	}
	if err := t.sendDefaultMenu(ctx); err != nil {
		logger.Errf("status redraw: %v", err)
	}
}

// onLog: последние строки лога (callback).
func (t *Bot) onLog(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_log") {
		return
	}
	tail := logTail(t.logPath, 25)
	if tail == "" {
		t.editResult(ctx, "Лог пуст или недоступен")
		return
	}
	t.editResult(ctx, "<b>Лог</b>\n<pre>"+escapePre(tail)+"</pre>")
}

// statusText renders the /status body: versions, active transport tier, last
// check time, uptime, and auto-update mode.
func (t *Bot) statusText() string {
	installed, latest, available, _ := t.state.snapshotPodkop()
	selfLatest, selfAvail := t.state.snapshotSelf()

	var b strings.Builder
	b.WriteString("<b>Статус</b>\n")
	b.WriteString("podkop: " + orDash(installed))
	if available {
		b.WriteString(" → " + latest + " ⬆️")
	}
	b.WriteString("\nupdater: " + orDash(t.selfVer))
	if selfAvail {
		b.WriteString(" → " + selfLatest + " ⬆️")
	}
	b.WriteString("\n")
	if t.tiers != nil {
		b.WriteString("Канал: " + orDash(t.tiers.CurrentTier()) + "\n")
	}
	if lc := t.state.lastCheckTime(); !lc.IsZero() {
		b.WriteString("Проверка: " + lc.Format("15:04:05") + " (" + humanizeSince(time.Since(lc)) + " назад)\n")
	}
	b.WriteString("Аптайм: " + humanizeSince(time.Since(t.startTime)) + "\n")
	b.WriteString("Авто-обновление: " + onoff(t.set.AutoUpdate()))
	return b.String()
}

// autoUpdatePodkop installs a newly-detected podkop release without waiting
// for a button press. It posts a fresh menu (so Telegram surfaces a
// notification) and runs the standard update flow under the busy guard.
func (t *Bot) autoUpdatePodkop(ctx context.Context, oldMID int) {
	if !t.state.tryBusy() {
		logger.Logf("auto-update skipped: busy")
		return
	}
	defer t.state.clearBusy()
	if oldMID != 0 {
		_ = t.deleteMessage(ctx, oldMID)
	}
	t.state.setMenuID(0)
	target, _ := t.state.latestAndTag()
	logger.Logf("Auto-updating podkop to %s", target)
	t.performPodkopUpdate(ctx)
}

// autoSelfUpdate installs a newly-detected updater release without a button
// press. Posts a fresh menu (so Telegram notifies) and runs self-update under
// the busy guard. Self-update exits the process for procd respawn.
func (t *Bot) autoSelfUpdate(ctx context.Context) {
	if !t.state.tryBusy() {
		logger.Logf("auto self-update skipped: busy")
		return
	}
	defer t.state.clearBusy()
	if oldMID := t.state.menuID(); oldMID != 0 {
		_ = t.deleteMessage(ctx, oldMID)
	}
	t.state.setMenuID(0)
	logger.Logf("Auto self-update starting")
	t.performSelfUpdate(ctx)
}

// notifySelfUpdate posts a fresh "updater update available" menu, deleting the
// previous tracked menu so Telegram raises a notification (edits are silent).
func (t *Bot) notifySelfUpdate(ctx context.Context, latest string) {
	if oldMID := t.state.menuID(); oldMID != 0 {
		_ = t.deleteMessage(ctx, oldMID)
	}
	t.state.setMenuID(0)
	text := "Доступна новая версия updater: <b>" + latest + "</b>\nТекущая: " + t.selfVer
	if err := t.replaceMenu(ctx, text, kbUpdateSelf); err != nil {
		logger.Errf("notify self update: %v", err)
	}
}

// logTail returns the last n lines of the file at path (empty string on any
// error or empty path).
func logTail(path string, n int) string {
	if path == "" || n <= 0 {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	ring := make([]string, 0, n)
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 1024*1024)
	for s.Scan() {
		if len(ring) < n {
			ring = append(ring, s.Text())
		} else {
			copy(ring, ring[1:])
			ring[n-1] = s.Text()
		}
	}
	return strings.Join(ring, "\n")
}

// escapePre escapes the characters that would break out of a <pre> block.
func escapePre(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func onoff(b bool) string {
	if b {
		return "вкл"
	}
	return "выкл"
}

// humanizeSince renders a duration compactly in Russian-ish short units.
func humanizeSince(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%dс", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dм", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dч %dм", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dд %dч", int(d.Hours())/24, int(d.Hours())%24)
}
