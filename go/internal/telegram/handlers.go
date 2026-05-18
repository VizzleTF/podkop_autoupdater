package telegram

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/VizzleTF/podkop_autoupdater/go/internal/logger"
	"github.com/VizzleTF/podkop_autoupdater/go/internal/service"
	"github.com/VizzleTF/podkop_autoupdater/go/internal/updater"
)

func (t *Bot) registerHandlers() {
	t.b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cmd_check_podkop", bot.MatchTypeExact, t.onCheckPodkop)
	t.b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cmd_check_self", bot.MatchTypeExact, t.onCheckSelf)
	t.b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cmd_check_dns", bot.MatchTypeExact, t.onCheckDNS)
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

// handleCallback runs the boilerplate shared by every callback: ACL check,
// ack to Telegram, adopt the clicked message as the tracked menu, and log.
// Returns false when the callback originated outside the configured chat —
// callers must early-return in that case.
func (t *Bot) handleCallback(ctx context.Context, update *models.Update, cmdName string) bool {
	if !t.fromAllowedChat(update.CallbackQuery) {
		return false
	}
	t.answer(ctx, update.CallbackQuery)
	t.adoptClickedMessage(ctx, update.CallbackQuery)
	logger.Logf("Callback: %s", cmdName)
	return true
}

// adoptClickedMessage makes the message the user just clicked on the
// bot's tracked menu. If a previous (orphan) menu was being tracked,
// it is deleted so the chat doesn't accumulate duplicate menus.
func (t *Bot) adoptClickedMessage(ctx context.Context, cb *models.CallbackQuery) {
	if cb == nil || cb.Message.Message == nil {
		return
	}
	clickedID := cb.Message.Message.ID
	oldID, changed := t.state.adoptMenuID(clickedID)
	if !changed {
		return
	}
	logger.Logf("Adopting clicked message id=%d (was tracking %d)", clickedID, oldID)
	if oldID != 0 {
		_ = t.deleteMessage(ctx, oldID)
	}
}

// versionCheck parameterizes the shared "press button → busy → refresh →
// render result" flow used by both onCheckPodkop and onCheckSelf.
type versionCheck struct {
	busyText     string
	refreshFn    func(ctx context.Context)
	readState    func() (current, latest string, available bool)
	kbUpdate     *models.InlineKeyboardMarkup
	errText      string
	upAvailText  func(latest string) string
	noUpdateText func(current, latest, checkTime string) string
}

func (t *Bot) runVersionCheck(ctx context.Context, vc versionCheck) {
	t.editBusy(ctx, vc.busyText)
	checkTime := time.Now().Format("15:04:05")
	vc.refreshFn(ctx)
	current, latest, available := vc.readState()

	switch {
	case latest == "":
		t.editResult(ctx, vc.errText+"\nПроверено: "+checkTime)
	case available:
		text := vc.upAvailText(latest) + "\nТекущая: " + current
		t.editUpdateAvailable(ctx, text, vc.kbUpdate)
	default:
		t.editResult(ctx, vc.noUpdateText(current, latest, checkTime))
	}
}

// onCheckPodkop: проверка версии podkop.
func (t *Bot) onCheckPodkop(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_check_podkop") {
		return
	}
	t.runVersionCheck(ctx, versionCheck{
		busyText:  "Проверка podkop...",
		refreshFn: t.refreshPodkop,
		readState: func() (string, string, bool) {
			installed, latest, available, _ := t.state.snapshotPodkop()
			return installed, latest, available
		},
		kbUpdate:    kbUpdatePodkop,
		errText:     "Ошибка проверки podkop. Подробности в логе.",
		upAvailText: func(latest string) string { return "Доступна новая версия podkop: <b>" + latest + "</b>" },
		noUpdateText: func(current, latest, checkTime string) string {
			return "Нет обновлений podkop\nУстановлено: " + current +
				"\nПоследнее: " + latest + "\nПроверено: " + checkTime
		},
	})
}

// onCheckSelf: проверка версии podkop_updater.
func (t *Bot) onCheckSelf(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_check_self") {
		return
	}
	t.runVersionCheck(ctx, versionCheck{
		busyText:  "Проверка updater...",
		refreshFn: t.refreshSelf,
		readState: func() (string, string, bool) {
			latest, available := t.state.snapshotSelf()
			return t.selfVer, latest, available
		},
		kbUpdate:    kbUpdateSelf,
		errText:     "Нет опубликованных релизов updater пока.",
		upAvailText: func(latest string) string { return "Доступна новая версия updater: <b>" + latest + "</b>" },
		noUpdateText: func(current, latest, checkTime string) string {
			return "Нет обновлений updater\nУстановлено: " + current +
				"\nПоследнее: " + latest + "\nПроверено: " + checkTime
		},
	})
}

// onCheckDNS: показать статус DNS подкопа + fakeip.
func (t *Bot) onCheckDNS(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_check_dns") {
		return
	}

	t.editBusy(ctx, "Проверка DNS...")

	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	status, err := service.CheckPodkopDNS(dctx)
	text := "<b>DNS статус</b>\n"
	if err != nil {
		text += "podkop check_dns_available: " + err.Error() + "\n"
	} else {
		text += fmt.Sprintf("Тип: %s\nСервер: %s %s\nНа роутере: %s\nBootstrap: %s %s\nDHCP: %s\n",
			status.DNSType,
			status.DNSServer, dnsOK(status.DNSStatus),
			dnsOK(status.DNSOnRouter),
			status.BootstrapDNSServer, dnsOK(status.BootstrapDNSStatus),
			dnsOK(status.DHCPConfigStatus),
		)
	}
	ip, fakeIPOK := service.FakeIPProbe(dctx, t.dnsCfg)
	if fakeIPOK {
		text += "FakeIP: ✅ " + t.dnsCfg.TestDomain + " → " + ip
	} else {
		text += "FakeIP: ❌ " + t.dnsCfg.TestDomain + " → " + ip
	}
	logger.Logf("DNS check: %s", strings.ReplaceAll(text, "\n", " "))
	t.editResult(ctx, text)
}

func dnsOK(v int) string {
	if v == 1 {
		return "✅"
	}
	return "❌"
}

// onRestart: перезагрузка podkop.
func (t *Bot) onRestart(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_restart") {
		return
	}

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
	if !t.handleCallback(ctx, update, "cmd_update_podkop") {
		return
	}

	target := t.state.latest()
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
	if !t.handleCallback(ctx, update, "cmd_update_self") {
		return
	}

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
	if !t.handleCallback(ctx, update, "cmd_ok") {
		return
	}
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
	t.state.setMenuID(0)
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
	if err != nil {
		logger.Errf("github podkop fetch: %v", err)
		t.state.setPodkopFetchError(installed)
		return
	}
	available := updater.IsNewer(installed, latest)
	t.state.setPodkopFetch(installed, latest, available)
	logger.Logf("podkop check: installed=%s latest=%s update=%v", installed, latest, available)
}

// refreshSelf fetches latest podkop_autoupdater release and compares with
// the binary's compile-time version.
func (t *Bot) refreshSelf(ctx context.Context) {
	latest, err := updater.LatestSelfRelease(ctx, t.hc)
	if err != nil {
		logger.Errf("github self fetch: %v", err)
		t.state.setSelfFetchError()
		return
	}
	if latest == "" {
		t.state.setSelfFetch("", false)
		logger.Logf("self check: current=%s latest=(none)", t.selfVer)
		return
	}
	available := updater.IsNewer(t.selfVer, latest)
	t.state.setSelfFetch(latest, available)
	logger.Logf("self check: current=%s latest=%s update=%v", t.selfVer, latest, available)
}
