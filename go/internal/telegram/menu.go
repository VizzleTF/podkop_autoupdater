package telegram

import (
	"context"

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
	t.mu.Lock()
	text := t.defaultText()
	mid := t.menuMID
	t.mu.Unlock()

	newMID, err := t.sendOrEdit(ctx, mid, text, kbDefault)
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.menuMID = newMID
	t.mu.Unlock()
	return nil
}

// sendUpdatePodkopMenu shows the "podkop update available" prompt as a fresh
// message (caller is expected to have already deleted the previous one when
// triggered by the periodic check).
func (t *Bot) sendUpdatePodkopMenu(ctx context.Context) error {
	t.mu.Lock()
	text := "Доступна новая версия podkop: <b>" + t.latestVer + "</b>\nТекущая: " + t.installedVer
	mid := t.menuMID
	t.mu.Unlock()

	newMID, err := t.sendOrEdit(ctx, mid, text, kbUpdatePodkop)
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.menuMID = newMID
	t.mu.Unlock()
	return nil
}

// editBusy puts the menu into a "Проверка..." / "Перезагрузка..." in-progress
// state with no buttons.
func (t *Bot) editBusy(ctx context.Context, text string) {
	t.mu.Lock()
	mid := t.menuMID
	t.mu.Unlock()
	newMID, err := t.sendOrEdit(ctx, mid, text, kbEmpty)
	if err != nil {
		logger.Errf("editBusy: %v", err)
		return
	}
	t.mu.Lock()
	t.menuMID = newMID
	t.mu.Unlock()
}

// editResult finishes an action with text and a single ОК button.
func (t *Bot) editResult(ctx context.Context, text string) {
	t.mu.Lock()
	mid := t.menuMID
	t.mu.Unlock()
	newMID, err := t.sendOrEdit(ctx, mid, text, kbOK)
	if err != nil {
		logger.Errf("editResult: %v", err)
		return
	}
	t.mu.Lock()
	t.menuMID = newMID
	t.mu.Unlock()
}

// editUpdateAvailable shows the "update available" state on the existing menu
// message with a single Обновить button.
func (t *Bot) editUpdateAvailable(ctx context.Context, text string, kb *models.InlineKeyboardMarkup) {
	t.mu.Lock()
	mid := t.menuMID
	t.mu.Unlock()
	newMID, err := t.sendOrEdit(ctx, mid, text, kb)
	if err != nil {
		logger.Errf("editUpdateAvailable: %v", err)
		return
	}
	t.mu.Lock()
	t.menuMID = newMID
	t.mu.Unlock()
}

// defaultText builds the title shown in the steady-state menu.
// Caller holds t.mu.
func (t *Bot) defaultText() string {
	text := "<b>Podkop Updater</b> on <b>" + t.hostname + "</b>"
	if t.installedVer != "" {
		text += "\npodkop: " + t.installedVer
	}
	if t.selfVer != "" {
		text += "\nupdater: " + t.selfVer
	}
	return text
}

// sendOrEdit edits the existing message (when msgID != 0) or sends a fresh
// one. Returns the message_id of the resulting message.
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
