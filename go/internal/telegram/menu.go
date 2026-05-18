package telegram

import (
	"context"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/VizzleTF/podkop_autoupdater/go/internal/logger"
)

// Inline keyboards (typed values, no raw JSON).
var (
	kbDefault = &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: "🔍 Проверить podkop", CallbackData: "cmd_check_podkop"}},
			{{Text: "⬆️ Проверить updater", CallbackData: "cmd_check_self"}},
			{{Text: "🌐 Проверить DNS", CallbackData: "cmd_check_dns"}},
			{{Text: "🔄 Перезагрузить podkop", CallbackData: "cmd_restart"}},
		},
	}
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

// sendDefaultMenu renders the steady-state 3-button menu.
func (t *Bot) sendDefaultMenu(ctx context.Context) error {
	return t.replaceMenu(ctx, t.defaultText(t.state.installed()), kbDefault)
}

// sendUpdatePodkopMenu shows the "podkop update available" prompt as a fresh
// message (caller is expected to have already deleted the previous one when
// triggered by the periodic check).
func (t *Bot) sendUpdatePodkopMenu(ctx context.Context) error {
	installed, latest := t.state.installedAndLatest()
	text := "Доступна новая версия podkop: <b>" + latest + "</b>\nТекущая: " + installed
	return t.replaceMenu(ctx, text, kbUpdatePodkop)
}

// replaceMenu edits the tracked menu (or sends fresh if none) and updates
// menuMID. Used by send* paths that propagate errors to the caller.
func (t *Bot) replaceMenu(ctx context.Context, text string, kb *models.InlineKeyboardMarkup) error {
	newMID, err := t.sendOrEdit(ctx, t.state.menuID(), text, kb)
	if err != nil {
		return err
	}
	t.state.setMenuID(newMID)
	return nil
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

// defaultText builds the title shown in the steady-state menu. The installed
// podkop version is passed in so the caller controls lock scope.
func (t *Bot) defaultText(installed string) string {
	text := "<b>Podkop Updater</b> on <b>" + t.hostname + "</b>"
	if installed != "" {
		text += "\npodkop: " + installed
	}
	if t.selfVer != "" {
		text += "\nupdater: " + t.selfVer
	}
	return text
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
