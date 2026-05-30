package telegram

import (
	"context"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/VizzleTF/podkop_autoupdater/go/internal/logger"
)

// Static inline keyboards (typed values, no raw JSON). The dashboard's main
// keyboard is built dynamically in buildDashboardKB.
var (
	kbOK = &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: "✅ ОК", CallbackData: "cmd_ok"}},
		},
	}
	kbUpdatePodkop = &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: "⬆️ Обновить podkop", CallbackData: "cmd_update_podkop"}},
		},
	}
	kbUpdateSelf = &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: "⬆️ Обновить updater", CallbackData: "cmd_update_self"}},
		},
	}
	kbEmpty = &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{},
	}
)

// buildDashboardKB assembles the steady-state keyboard. Contextual update
// rows appear first when an update is available; the destructive rollback
// lives at the bottom row, separated from the diagnostics.
func buildDashboardKB(updPodkop, updSelf bool) *models.InlineKeyboardMarkup {
	var rows [][]models.InlineKeyboardButton
	if updPodkop {
		rows = append(rows, []models.InlineKeyboardButton{
			{Text: "⬆️ Обновить podkop", CallbackData: "cmd_update_podkop"},
		})
	}
	if updSelf {
		rows = append(rows, []models.InlineKeyboardButton{
			{Text: "⬆️ Обновить updater", CallbackData: "cmd_update_self"},
		})
	}
	rows = append(rows,
		[]models.InlineKeyboardButton{
			{Text: "🔄 Проверить статус", CallbackData: "cmd_refresh"},
			{Text: "♻️ Рестарт", CallbackData: "cmd_restart"},
		},
		[]models.InlineKeyboardButton{
			{Text: "📦 Бэкапы", CallbackData: "cmd_backups"},
			{Text: "⚙️ Настройки", CallbackData: "cmd_settings"},
		},
	)
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// buildBackupsKB is the keyboard for the 📦 Бэкапы submenu.
func buildBackupsKB() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: "💾 Создать бэкап конфига", CallbackData: "cmd_backup"}},
			{
				{Text: "📂 Восстановить", CallbackData: "cmd_restore"},
				{Text: "🗑 Удалить", CallbackData: "cmd_bk_delete"},
			},
			{{Text: "↩️ Откат версии podkop", CallbackData: "cmd_rollback"}},
			{{Text: "‹ Назад", CallbackData: "cmd_ok"}},
		},
	}
}

// sendBackupsMenu renders the backups submenu, listing the current backup
// count, into the tracked message.
func (t *Bot) sendBackupsMenu(ctx context.Context) {
	text := "📦 <b>Бэкапы конфига podkop</b>"
	if t.runner != nil {
		if vs, err := t.runner.ListBackupVersions(); err == nil {
			text += "\nСохранённых версий: " + strconv.Itoa(len(vs))
		}
	}
	text += "\n\nСоздать снимок конфига, восстановить из снимка, удалить лишний или откатить версию podkop."
	t.updateMenu(ctx, text, buildBackupsKB())
}

// sendDefaultMenu renders the dashboard: a status card plus the dynamic
// keyboard (with contextual update rows when available).
func (t *Bot) sendDefaultMenu(ctx context.Context) error {
	_, _, podkopUpd, _ := t.state.snapshotPodkop()
	_, selfUpd := t.state.snapshotSelf()
	// The dashboard card carries its own header, so skip the withLabel prefix.
	return t.replaceMenuRaw(ctx, t.dashboardText(), buildDashboardKB(podkopUpd, selfUpd))
}

// sendUpdatePodkopMenu re-renders the dashboard as a fresh message so the
// periodic check raises a Telegram notification (the dashboard already
// surfaces the "Обновить podkop" row when an update is available).
func (t *Bot) sendUpdatePodkopMenu(ctx context.Context) error {
	return t.sendDefaultMenu(ctx)
}

// replaceMenu edits the tracked menu (or sends fresh if none) and updates
// menuMID. Prepends the bold router label via withLabel.
func (t *Bot) replaceMenu(ctx context.Context, text string, kb *models.InlineKeyboardMarkup) error {
	return t.replaceMenuRaw(ctx, t.withLabel(text), kb)
}

// replaceMenuRaw is replaceMenu without the withLabel prefix, for text that
// already carries its own header (the dashboard card).
func (t *Bot) replaceMenuRaw(ctx context.Context, text string, kb *models.InlineKeyboardMarkup) error {
	newMID, err := t.sendOrEdit(ctx, t.state.menuID(), text, kb)
	if err != nil {
		return err
	}
	t.state.setMenuID(newMID)
	return nil
}

// withLabel prepends a single-line bold router label to text so each message
// in a shared chat (e.g. when multiple routers post to one supergroup) is
// attributable. Empty label leaves text untouched.
func (t *Bot) withLabel(text string) string {
	label := t.set.Label()
	if label == "" {
		return text
	}
	return "<b>" + html.EscapeString(label) + "</b>\n" + text
}

// updateMenu is the fire-and-log variant used by callback handlers: edit
// failures are logged but not returned, since the handler has nothing
// useful to do with them.
func (t *Bot) updateMenu(ctx context.Context, text string, kb *models.InlineKeyboardMarkup) {
	if err := t.replaceMenu(ctx, text, kb); err != nil {
		logger.Errf("updateMenu: %v", err)
	}
}

// editBusy puts the menu into a "Проверка..." / "Перезагрузка..." in-progress
// state with no buttons.
func (t *Bot) editBusy(ctx context.Context, text string) {
	t.updateMenu(ctx, text, kbEmpty)
}

// editResult finishes an action with text and a single ОК button.
func (t *Bot) editResult(ctx context.Context, text string) {
	t.updateMenu(ctx, text, kbOK)
}

// editUpdateAvailable shows the "update available" state on the existing menu
// message with a single Обновить button.
func (t *Bot) editUpdateAvailable(ctx context.Context, text string, kb *models.InlineKeyboardMarkup) {
	t.updateMenu(ctx, text, kb)
}

// dashboardText builds the status card shown in the steady-state menu. It is
// emitted as normal HTML (not <pre>): emoji render full-width and break
// monospace alignment, so a framed list reads better than padded columns.
// The router label is the card header here, so sendDefaultMenu sends it
// without the withLabel prefix. All data comes from cached state (no
// network) — the "🔍 Проверить" button refreshes it.
func (t *Bot) dashboardText() string {
	installed, latest, podkopUpd, _ := t.state.snapshotPodkop()
	selfLatest, selfUpd := t.state.snapshotSelf()

	var b strings.Builder
	if label := t.set.Label(); label != "" {
		b.WriteString("🏠 <b>" + html.EscapeString(label) + "</b>\n")
	} else {
		b.WriteString("🤖 <b>Podkop Updater</b>\n")
	}
	b.WriteString("├ 📦 <b>podkop</b>  <code>" + orDash(installed) + "</code>  " + verState(podkopUpd, latest) + "\n")
	b.WriteString("├ 🛠 <b>updater</b> <code>" + orDash(t.selfVer) + "</code>  " + verState(selfUpd, selfLatest) + "\n")

	if ip, ok, checked := t.state.dnsSnapshot(); checked {
		mark := "✅"
		if !ok {
			mark = "❌"
		}
		b.WriteString("├ 🌐 <b>DNS</b>     " + mark + " <code>" + html.EscapeString(ip) + "</code>\n")
	} else {
		b.WriteString("├ 🌐 <b>DNS</b>     — нажми «Проверить»\n")
	}

	tier := "—"
	if t.tiers != nil {
		tier = orDash(t.tiers.CurrentTier())
	}
	b.WriteString("├ 🔌 <b>канал</b>   <code>" + html.EscapeString(tier) + "</code>\n")

	if lc := t.state.lastCheckTime(); !lc.IsZero() {
		b.WriteString("└ 🕐 " + lc.Format("15:04:05") + " · " + humanizeSince(time.Since(lc)) + " назад")
	} else {
		b.WriteString("└ 🕐 —")
	}
	return b.String()
}

// verState renders the status column of a version row: a checkmark when up to
// date, or "→ <latest> ⬆️" when an update is available.
func verState(updateAvailable bool, latest string) string {
	if updateAvailable && latest != "" {
		return "→ <b>" + latest + "</b> ⬆️"
	}
	return "✅"
}

// sendOrEdit edits the existing message (when msgID != 0) or sends a fresh
// one. Returns the message_id of the resulting message.
//
// When Telegram returns "message is not modified" the edit succeeded
// semantically (the message already has the desired content), so we keep
// the same msgID without falling back to a new send.
func (t *Bot) sendOrEdit(ctx context.Context, msgID int, text string, kb *models.InlineKeyboardMarkup) (int, error) {
	if msgID != 0 {
		edited, err := t.b.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:      t.chatIDStr(),
			MessageID:   msgID,
			Text:        text,
			ParseMode:   models.ParseModeHTML,
			ReplyMarkup: kb,
		})
		if err == nil && edited != nil {
			return edited.ID, nil
		}
		if isNotModified(err) {
			return msgID, nil
		}
		logger.Errf("editMessageText id=%d failed: %v (falling back to send)", msgID, err)
	}
	sent, err := t.b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      t.chatIDStr(),
		Text:        text,
		ParseMode:   models.ParseModeHTML,
		ReplyMarkup: kb,
	})
	if err != nil {
		return 0, err
	}
	return sent.ID, nil
}

// isNotModified reports whether the error is Telegram's "message is not
// modified" response. We treat that as a no-op success.
func isNotModified(err error) bool {
	return err != nil && strings.Contains(err.Error(), "message is not modified")
}

func (t *Bot) deleteMessage(ctx context.Context, msgID int) error {
	if msgID == 0 {
		return nil
	}
	_, err := t.b.DeleteMessage(ctx, &bot.DeleteMessageParams{
		ChatID:    t.chatIDStr(),
		MessageID: msgID,
	})
	return err
}
