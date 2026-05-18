package telegram

import (
	"context"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/VizzleTF/podkop_autoupdater/go/internal/logger"
	"github.com/VizzleTF/podkop_autoupdater/go/internal/updater"
)

func (t *Bot) registerHandlers() {
	t.b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cmd_check_podkop", bot.MatchTypeExact, t.onCheckPodkop)
	t.b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cmd_check_self", bot.MatchTypeExact, t.onCheckSelf)
	t.b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cmd_restart", bot.MatchTypeExact, t.onRestart)
	t.b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cmd_update_podkop", bot.MatchTypeExact, t.onUpdatePodkop)
	t.b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cmd_update_self", bot.MatchTypeExact, t.onUpdateSelf)
	t.b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cmd_ok", bot.MatchTypeExact, t.onOK)
	t.b.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypeExact, t.onStartOrMenu)
	t.b.RegisterHandler(bot.HandlerTypeMessageText, "/menu", bot.MatchTypeExact, t.onStartOrMenu)
}

func (t *Bot) answer(ctx context.Context, cb *models.CallbackQuery) {
	if cb == nil {
		return
	}
	_, _ = t.b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: cb.ID,
	})
}

func (t *Bot) fromAllowedChat(cb *models.CallbackQuery) bool {
	if cb == nil || cb.Message.Message == nil {
		return false
	}
	return cb.Message.Message.Chat.ID == t.chatID
}

// adoptClickedMessage makes the message the user just clicked on the
// bot's tracked menu. If a previous (orphan) menu was being tracked,
// it is deleted so the chat doesn't accumulate duplicate menus.
func (t *Bot) adoptClickedMessage(ctx context.Context, cb *models.CallbackQuery) {
	if cb == nil || cb.Message.Message == nil {
		return
	}
	clickedID := cb.Message.Message.ID
	t.mu.Lock()
	oldID := t.menuMID
	if oldID == clickedID {
		t.mu.Unlock()
		return
	}
	t.menuMID = clickedID
	t.mu.Unlock()
	logger.Logf("Adopting clicked message id=%d (was tracking %d)", clickedID, oldID)
	if oldID != 0 {
		_ = t.deleteMessage(ctx, oldID)
	}
}

// onCheckPodkop: проверка версии podkop.
func (t *Bot) onCheckPodkop(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.fromAllowedChat(update.CallbackQuery) {
		return
	}
	t.answer(ctx, update.CallbackQuery)
	t.adoptClickedMessage(ctx, update.CallbackQuery)
	logger.Logf("Callback: cmd_check_podkop")

	t.editBusy(ctx, "Проверка podkop...")
	checkTime := time.Now().Format("15:04:05")
	t.refreshPodkop(ctx)

	t.mu.Lock()
	installed := t.installedVer
	latest := t.latestVer
	available := t.updateAvailable
	t.mu.Unlock()

	switch {
	case latest == "":
		t.editResult(ctx, "Ошибка проверки podkop. Подробности в логе.\nПроверено: "+checkTime)
	case available:
		text := "Доступна новая версия podkop: <b>" + latest + "</b>\nТекущая: " + installed
		t.editUpdateAvailable(ctx, text, kbUpdatePodkop)
	default:
		text := "Нет обновлений podkop\nУстановлено: " + installed +
			"\nПоследнее: " + latest + "\nПроверено: " + checkTime
		t.editResult(ctx, text)
	}
}

// onCheckSelf: проверка версии podkop_updater.
func (t *Bot) onCheckSelf(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.fromAllowedChat(update.CallbackQuery) {
		return
	}
	t.answer(ctx, update.CallbackQuery)
	t.adoptClickedMessage(ctx, update.CallbackQuery)
	logger.Logf("Callback: cmd_check_self")

	t.editBusy(ctx, "Проверка updater...")
	checkTime := time.Now().Format("15:04:05")
	t.refreshSelf(ctx)

	t.mu.Lock()
	current := t.selfVer
	latest := t.selfLatest
	available := t.selfUpdateAvailable
	t.mu.Unlock()

	switch {
	case latest == "":
		t.editResult(ctx, "Нет опубликованных релизов updater пока.\nПроверено: "+checkTime)
	case available:
		text := "Доступна новая версия updater: <b>" + latest + "</b>\nТекущая: " + current
		t.editUpdateAvailable(ctx, text, kbUpdateSelf)
	default:
		text := "Нет обновлений updater\nУстановлено: " + current +
			"\nПоследнее: " + latest + "\nПроверено: " + checkTime
		t.editResult(ctx, text)
	}
}

// onRestart: перезагрузка podkop.
func (t *Bot) onRestart(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.fromAllowedChat(update.CallbackQuery) {
		return
	}
	t.answer(ctx, update.CallbackQuery)
	t.adoptClickedMessage(ctx, update.CallbackQuery)
	logger.Logf("Callback: cmd_restart")

	t.editBusy(ctx, "Перезагрузка podkop...")

	if t.runner == nil {
		t.editResult(ctx, "Готово\n[stub] Перезагрузка пока не реализована (phase 3)")
		return
	}
	status, err := t.runner.RunRestart(ctx)
	if err != nil {
		logger.Errf("restart: %v", err)
		t.editResult(ctx, "Ошибка перезагрузки\n"+status)
		return
	}
	t.editResult(ctx, "Готово\n"+status)
}

// onUpdatePodkop: запуск обновления podkop.
func (t *Bot) onUpdatePodkop(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.fromAllowedChat(update.CallbackQuery) {
		return
	}
	t.answer(ctx, update.CallbackQuery)
	t.adoptClickedMessage(ctx, update.CallbackQuery)
	logger.Logf("Callback: cmd_update_podkop")

	t.mu.Lock()
	target := t.latestVer
	t.mu.Unlock()

	if target == "" {
		t.editResult(ctx, "Нет данных о новой версии podkop")
		return
	}
	t.editBusy(ctx, "Обновление podkop до "+target+"...")

	if t.runner == nil {
		t.editResult(ctx, "Готово\n[stub] Обновление пока не реализовано (phase 3)")
		return
	}
	status, err := t.runner.RunUpdate(ctx, target)
	if err != nil {
		logger.Errf("update: %v", err)
		t.editResult(ctx, "Ошибка обновления\n"+status)
		return
	}
	t.refreshPodkop(ctx)
	t.editResult(ctx, "Готово\n"+status)
}

// onUpdateSelf: запуск self-update.
func (t *Bot) onUpdateSelf(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.fromAllowedChat(update.CallbackQuery) {
		return
	}
	t.answer(ctx, update.CallbackQuery)
	t.adoptClickedMessage(ctx, update.CallbackQuery)
	logger.Logf("Callback: cmd_update_self")

	t.editBusy(ctx, "Обновление updater...")

	if t.runner == nil {
		t.editResult(ctx, "Готово\n[stub] Self-update пока не реализован (phase 4)")
		return
	}
	status, err := t.runner.RunSelfUpdate(ctx)
	if err != nil {
		logger.Errf("self-update: %v", err)
		t.editResult(ctx, "Ошибка self-update\n"+status)
		return
	}
	t.editResult(ctx, "Готово\n"+status)
}

// onOK: вернуться в дефолтное меню.
func (t *Bot) onOK(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.fromAllowedChat(update.CallbackQuery) {
		return
	}
	t.answer(ctx, update.CallbackQuery)
	t.adoptClickedMessage(ctx, update.CallbackQuery)
	logger.Logf("Callback: cmd_ok")
	if err := t.sendDefaultMenu(ctx); err != nil {
		logger.Errf("send default menu: %v", err)
	}
}

// onStartOrMenu: /start и /menu — пересоздать сообщение-меню.
func (t *Bot) onStartOrMenu(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if update.Message == nil || update.Message.Chat.ID != t.chatID {
		return
	}
	logger.Logf("Text command: %s", update.Message.Text)
	t.mu.Lock()
	t.menuMID = 0
	t.mu.Unlock()
	if err := t.sendDefaultMenu(ctx); err != nil {
		logger.Errf("send menu after /start|/menu: %v", err)
	}
}

// refreshPodkop fetches latest podkop release + installed version.
// Errors are logged; on failure latestVer is cleared so the UI shows
// an explicit "error" state instead of misleading data.
func (t *Bot) refreshPodkop(ctx context.Context) {
	latest, err := updater.LatestRelease(ctx, t.hc)
	installed := updater.InstalledVersion()
	t.mu.Lock()
	defer t.mu.Unlock()
	if err != nil {
		logger.Errf("github podkop fetch: %v", err)
		t.latestVer = ""
		t.updateAvailable = false
		t.installedVer = installed
		return
	}
	t.installedVer = installed
	t.latestVer = latest
	t.updateAvailable = updater.IsNewer(installed, latest)
}

// refreshSelf fetches latest podkop_autoupdater release and compares with
// the binary's compile-time version.
func (t *Bot) refreshSelf(ctx context.Context) {
	latest, err := updater.LatestSelfRelease(ctx, t.hc)
	t.mu.Lock()
	defer t.mu.Unlock()
	if err != nil {
		logger.Errf("github self fetch: %v", err)
		t.selfLatest = ""
		t.selfUpdateAvailable = false
		return
	}
	t.selfLatest = latest
	if latest == "" {
		t.selfUpdateAvailable = false
		return
	}
	t.selfUpdateAvailable = updater.IsNewer(t.selfVer, latest)
}
