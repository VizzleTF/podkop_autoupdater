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
	t.b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cmd_refresh", bot.MatchTypeExact, t.onRefresh)
	t.b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cmd_status", bot.MatchTypeExact, t.onStatus)
	t.b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cmd_log", bot.MatchTypeExact, t.onLog)
	t.b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cmd_backups", bot.MatchTypeExact, t.onBackups)
	t.b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cmd_backup", bot.MatchTypeExact, t.onBackup)
	t.b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cmd_restore", bot.MatchTypeExact, t.onRestore)
	t.b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cmd_bk_delete", bot.MatchTypeExact, t.onDelete)
	t.b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cmd_del_ver:", bot.MatchTypePrefix, t.onDeleteVer)
	t.b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cmd_del_id:", bot.MatchTypePrefix, t.onDeleteID)
	t.b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cmd_del_confirm", bot.MatchTypeExact, t.onDeleteConfirm)
	// Prefix handlers carry the chosen version / backup id after the ':'.
	t.b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cmd_restore_ver:", bot.MatchTypePrefix, t.onRestoreVer)
	t.b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cmd_restore_id:", bot.MatchTypePrefix, t.onRestoreID)
	t.b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cmd_restore_confirm", bot.MatchTypeExact, t.onRestoreConfirm)
	t.b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cmd_rollback", bot.MatchTypeExact, t.onRollback)
	t.b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cmd_rollback_all", bot.MatchTypeExact, t.onRollbackAll)
	t.b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cmd_rollback_to:", bot.MatchTypePrefix, t.onRollbackTo)
	t.b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cmd_rollback_confirm", bot.MatchTypeExact, t.onRollbackConfirm)
	t.b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cmd_ok", bot.MatchTypeExact, t.onOK)
	t.b.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypeExact, t.onStartOrMenu)
	t.b.RegisterHandler(bot.HandlerTypeMessageText, "/menu", bot.MatchTypeExact, t.onStartOrMenu)
	t.b.RegisterHandler(bot.HandlerTypeMessageText, "/check_podkop", bot.MatchTypeExact, t.onTextCheckPodkop)
	t.b.RegisterHandler(bot.HandlerTypeMessageText, "/check_self", bot.MatchTypeExact, t.onTextCheckSelf)
	t.b.RegisterHandler(bot.HandlerTypeMessageText, "/check_dns", bot.MatchTypeExact, t.onTextCheckDNS)
	t.b.RegisterHandler(bot.HandlerTypeMessageText, "/restart", bot.MatchTypeExact, t.onTextRestart)
	t.b.RegisterHandler(bot.HandlerTypeMessageText, "/status", bot.MatchTypeExact, t.onTextStatus)
	t.b.RegisterHandler(bot.HandlerTypeMessageText, "/log", bot.MatchTypeExact, t.onTextLog)
}

// allowedUser reports whether userID may issue commands. An empty admin set
// means anyone in the configured chat is allowed (backward-compatible).
func (t *Bot) allowedUser(userID int64) bool {
	if len(t.adminIDs) == 0 {
		return true
	}
	return t.adminIDs[userID]
}

// answerAlert acks a callback with a popup alert (used for access-denied and
// busy notices). Best-effort.
func (t *Bot) answerAlert(ctx context.Context, cb *models.CallbackQuery, text string) {
	if cb == nil {
		return
	}
	_, _ = t.b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: cb.ID,
		Text:            text,
		ShowAlert:       true,
	})
}

// startTextCommand is the entry guard shared by every slash-command handler:
// filters chat, logs the command, and resets the tracked menu id so the
// resulting busy/result flow lands in a fresh message instead of replacing
// the menu the user was looking at.
func (t *Bot) startTextCommand(update *models.Update, name string) bool {
	if update.Message == nil || update.Message.Chat.ID != t.chatID {
		return false
	}
	if update.Message.From != nil && !t.allowedUser(update.Message.From.ID) {
		logger.Logf("Text command %s denied for user %d", name, update.Message.From.ID)
		return false
	}
	logger.Logf("Text command: %s", name)
	t.state.setMenuID(0)
	return true
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
	cb := update.CallbackQuery
	if !t.fromAllowedChat(cb) {
		return false
	}
	if !t.allowedUser(cb.From.ID) {
		logger.Logf("Callback %s denied for user %d", cmdName, cb.From.ID)
		t.answerAlert(ctx, cb, "Нет доступа")
		return false
	}
	t.answer(ctx, cb)
	t.adoptClickedMessage(ctx, cb)
	logger.Logf("Callback: %s", cmdName)
	return true
}

// beginHeavy is handleCallback plus a concurrency guard for the destructive,
// long-running actions (restart/update/self-update). It returns a release
// func and true when the caller owns the busy lock; the caller must
// `defer release()`. On denial/busy it answers the user and returns false.
func (t *Bot) beginHeavy(ctx context.Context, update *models.Update, cmdName string) (func(), bool) {
	cb := update.CallbackQuery
	if !t.fromAllowedChat(cb) {
		return nil, false
	}
	if !t.allowedUser(cb.From.ID) {
		logger.Logf("Callback %s denied for user %d", cmdName, cb.From.ID)
		t.answerAlert(ctx, cb, "Нет доступа")
		return nil, false
	}
	if !t.state.tryBusy() {
		logger.Logf("Callback %s rejected: busy", cmdName)
		t.answerAlert(ctx, cb, "Занято, дождитесь завершения текущей операции")
		return nil, false
	}
	t.answer(ctx, cb)
	t.adoptClickedMessage(ctx, cb)
	logger.Logf("Callback: %s", cmdName)
	return t.state.clearBusy, true
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

// podkopVC describes the version-check flow for podkop. Defined as a method
// so both onCheckPodkop (callback) and onTextCheckPodkop (slash command)
// share the same configuration.
func (t *Bot) podkopVC() versionCheck {
	return versionCheck{
		busyText:  "Проверка podkop...",
		refreshFn: t.refreshPodkop,
		readState: func() (string, string, bool) {
			installed, latest, available, _ := t.state.snapshotPodkop()
			return installed, latest, available
		},
		kbUpdate: kbUpdatePodkop,
		errText:  "Ошибка проверки podkop. Подробности в логе.",
		upAvailText: func(latest string) string {
			return "Доступна новая версия podkop: <b>" + latest + "</b>"
		},
		noUpdateText: func(current, latest, checkTime string) string {
			return "Нет обновлений podkop\nУстановлено: " + current +
				"\nПоследнее: " + latest + "\nПроверено: " + checkTime
		},
	}
}

// selfVC describes the version-check flow for the updater itself; symmetric
// to podkopVC.
func (t *Bot) selfVC() versionCheck {
	return versionCheck{
		busyText:  "Проверка updater...",
		refreshFn: t.refreshSelf,
		readState: func() (string, string, bool) {
			latest, available := t.state.snapshotSelf()
			return t.selfVer, latest, available
		},
		kbUpdate: kbUpdateSelf,
		errText:  "Нет опубликованных релизов updater пока.",
		upAvailText: func(latest string) string {
			return "Доступна новая версия updater: <b>" + latest + "</b>"
		},
		noUpdateText: func(current, latest, checkTime string) string {
			return "Нет обновлений updater\nУстановлено: " + current +
				"\nПоследнее: " + latest + "\nПроверено: " + checkTime
		},
	}
}

// onCheckPodkop: проверка версии podkop (callback).
func (t *Bot) onCheckPodkop(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_check_podkop") {
		return
	}
	t.runVersionCheck(ctx, t.podkopVC())
}

// onCheckSelf: проверка версии podkop_updater (callback).
func (t *Bot) onCheckSelf(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_check_self") {
		return
	}
	t.runVersionCheck(ctx, t.selfVC())
}

// onTextCheckPodkop: /check_podkop — то же, что кнопка, но в новом сообщении.
func (t *Bot) onTextCheckPodkop(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.startTextCommand(update, "/check_podkop") {
		return
	}
	t.runVersionCheck(ctx, t.podkopVC())
}

// onTextCheckSelf: /check_self — симметрично onTextCheckPodkop.
func (t *Bot) onTextCheckSelf(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.startTextCommand(update, "/check_self") {
		return
	}
	t.runVersionCheck(ctx, t.selfVC())
}

// doCheckDNS is the actual DNS-status flow, used by both callback and
// text-command entry points.
func (t *Bot) doCheckDNS(ctx context.Context) {
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

// onCheckDNS: показать статус DNS подкопа + fakeip (callback).
func (t *Bot) onCheckDNS(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_check_dns") {
		return
	}
	t.doCheckDNS(ctx)
}

// onTextCheckDNS: /check_dns — то же, что кнопка, но в новом сообщении.
func (t *Bot) onTextCheckDNS(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.startTextCommand(update, "/check_dns") {
		return
	}
	t.doCheckDNS(ctx)
}

func dnsOK(v int) string {
	if v == 1 {
		return "✅"
	}
	return "❌"
}

// doRestart is the actual restart flow, used by both callback and
// text-command entry points.
func (t *Bot) doRestart(ctx context.Context) {
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

// onRestart: перезагрузка podkop (callback).
func (t *Bot) onRestart(ctx context.Context, _ *bot.Bot, update *models.Update) {
	release, ok := t.beginHeavy(ctx, update, "cmd_restart")
	if !ok {
		return
	}
	defer release()
	t.doRestart(ctx)
}

// onTextRestart: /restart — то же, что кнопка, но в новом сообщении.
func (t *Bot) onTextRestart(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.startTextCommand(update, "/restart") {
		return
	}
	if !t.state.tryBusy() {
		logger.Logf("/restart rejected: busy")
		t.sendText(ctx, "Занято, дождитесь завершения текущей операции")
		return
	}
	defer t.state.clearBusy()
	t.doRestart(ctx)
}

// onUpdatePodkop: запуск обновления podkop.
func (t *Bot) onUpdatePodkop(ctx context.Context, _ *bot.Bot, update *models.Update) {
	release, ok := t.beginHeavy(ctx, update, "cmd_update_podkop")
	if !ok {
		return
	}
	defer release()
	t.performPodkopUpdate(ctx)
}

// performPodkopUpdate runs the podkop update flow against the cached target
// version/tag and renders the result. The caller owns the busy guard. If the
// post-update DNS check did not recover, the result offers a one-tap rollback
// to the version that was installed before the update.
func (t *Bot) performPodkopUpdate(ctx context.Context) {
	target, tag := t.state.latestAndTag()
	if target == "" {
		t.editResult(ctx, "Нет данных о новой версии podkop")
		return
	}
	before := t.state.installed()
	t.editBusy(ctx, "Обновление podkop до "+target+"...")

	if t.runner == nil {
		t.editResult(ctx, "Готово\n[stub] Обновление пока не реализовано (phase 3)")
		return
	}
	status, err := t.runner.RunUpdate(ctx, target, tag)
	if err != nil {
		logger.Errf("update: %v", err)
		t.editResult(ctx, "Ошибка обновления\n"+status)
		return
	}
	t.refreshPodkop(ctx)
	// A post-update DNS failure is flagged by RunUpdate with this marker.
	if before != "" && strings.Contains(status, "DNS не поднялся") {
		t.state.setRollback(before, before) // podkop tags are bare versions
		t.editResultWithRollback(ctx, "Готово\n"+status, before)
		return
	}
	t.editResult(ctx, "Готово\n"+status)
}

// onUpdateSelf: запуск self-update.
func (t *Bot) onUpdateSelf(ctx context.Context, _ *bot.Bot, update *models.Update) {
	release, ok := t.beginHeavy(ctx, update, "cmd_update_self")
	if !ok {
		return
	}
	defer release()

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
	latest, tag, err := updater.LatestReleaseFull(ctx, t.hc)
	installed := updater.InstalledVersion()
	if err != nil {
		logger.Errf("github podkop fetch: %v", err)
		t.state.setPodkopFetchError(installed)
		return
	}
	available := updater.IsNewer(installed, latest)
	t.state.setPodkopFetch(installed, latest, tag, available, time.Now())
	logger.Logf("podkop check: installed=%s latest=%s tag=%s update=%v", installed, latest, tag, available)
}

// refreshAll refreshes podkop + updater versions and probes fakeip DNS, so
// the dashboard card reflects current reality. Used by the 🔍 Проверить button.
func (t *Bot) refreshAll(ctx context.Context) {
	t.refreshPodkop(ctx)
	t.refreshSelf(ctx)
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ip, ok := service.FakeIPProbe(dctx, t.dnsCfg)
	t.state.setDNS(ip, ok)
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
