package telegram

import (
	"context"
	"sort"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/VizzleTF/podkop_autoupdater/go/internal/logger"
	"github.com/VizzleTF/podkop_autoupdater/go/internal/updater"
)

// callbackArg returns the text after prefix in a callback's data field.
func callbackArg(update *models.Update, prefix string) string {
	if update.CallbackQuery == nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(update.CallbackQuery.Data, prefix))
}

// sortVersionsDesc sorts X.Y.Z versions newest-first.
func sortVersionsDesc(vs []string) {
	sort.Slice(vs, func(i, j int) bool { return updater.IsNewer(vs[j], vs[i]) })
}

// appendVersionRows appends version buttons (two per row) to rows. Versions
// present in backupSet are marked 💾. Callback data is prefix+version.
func appendVersionRows(rows [][]models.InlineKeyboardButton, prefix string, versions []string, backupSet map[string]bool) [][]models.InlineKeyboardButton {
	var row []models.InlineKeyboardButton
	for _, v := range versions {
		label := v
		if backupSet[v] {
			label += " 💾"
		}
		row = append(row, models.InlineKeyboardButton{Text: label, CallbackData: prefix + v})
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return rows
}

// buildVersionListKB renders one button per version plus a cancel row that
// returns to the backups submenu.
func buildVersionListKB(prefix string, versions []string, backupSet map[string]bool) *models.InlineKeyboardMarkup {
	rows := appendVersionRows(nil, prefix, versions, backupSet)
	rows = append(rows, []models.InlineKeyboardButton{{Text: "✖️ Отмена", CallbackData: "cmd_backups"}})
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// onRefresh: 🔍 Проверить — refresh versions + DNS and redraw the dashboard.
func (t *Bot) onRefresh(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_refresh") {
		return
	}
	t.editBusy(ctx, "Проверка...")
	t.refreshAll(ctx)
	if err := t.sendDefaultMenu(ctx); err != nil {
		logger.Errf("refresh redraw: %v", err)
	}
}

// minRollbackVersion is podkop's own supported floor (install.sh refuses to
// run below this); we never offer to roll back past it.
const minRollbackVersion = "0.7.0"

// geMin reports whether v >= minRollbackVersion.
func geMin(v string) bool { return !updater.IsNewer(v, minRollbackVersion) }

// onRollback: ↩️ Откат версии — screen 1. Lists versions that have a local
// config backup (and still exist as downloadable releases), marked 💾, plus a
// button to the full release list for a no-config-backup rollback. Read-only;
// the actual downgrade happens at the confirm step.
func (t *Bot) onRollback(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_rollback") {
		return
	}
	installed := t.state.installed()
	t.editBusy(ctx, "Поиск версий для отката...")

	relMap, err := updater.ReleaseMap(ctx, t.hc)
	if err != nil {
		logger.Errf("rollback release map: %v", err)
		t.updateMenu(ctx, "Не удалось получить список релизов\nПодробности в логе", kbBackToBackups)
		return
	}
	var backups []string
	if t.runner != nil {
		backups, _ = t.runner.ListBackupVersions()
	}

	// Backed-up versions that are still downloadable and supported.
	var versions []string
	backupSet := map[string]bool{}
	for _, v := range backups {
		if v != installed && relMap[v] != "" && geMin(v) && !backupSet[v] {
			versions = append(versions, v)
			backupSet[v] = true
		}
	}
	sortVersionsDesc(versions)

	var rows [][]models.InlineKeyboardButton
	rows = appendVersionRows(rows, "cmd_rollback_to:", versions, backupSet)
	rows = append(rows, []models.InlineKeyboardButton{
		{Text: "⬇️ Откат без бэкапа конфига (все версии)", CallbackData: "cmd_rollback_all"},
	})
	rows = append(rows, []models.InlineKeyboardButton{{Text: "✖️ Отмена", CallbackData: "cmd_backups"}})

	text := "Откат podkop с <b>" + orDash(installed) + "</b>\n"
	if len(versions) > 0 {
		text += "Версии с бэкапом конфига (💾):"
	} else {
		text += "Бэкапов конфига нет — выберите любую версию ниже."
	}
	t.updateMenu(ctx, text, &models.InlineKeyboardMarkup{InlineKeyboard: rows})
}

// onRollbackAll: ↩️ Откат версии — screen 2. Lists every supported release
// (>= minRollbackVersion) for a rollback without a config backup. Versions
// that do have a config backup are still marked 💾.
func (t *Bot) onRollbackAll(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_rollback_all") {
		return
	}
	installed := t.state.installed()
	t.editBusy(ctx, "Загрузка списка версий...")

	relMap, err := updater.ReleaseMap(ctx, t.hc)
	if err != nil {
		logger.Errf("rollback all release map: %v", err)
		t.updateMenu(ctx, "Не удалось получить список релизов", kbBackToBackups)
		return
	}
	backupSet := map[string]bool{}
	if t.runner != nil {
		if bs, _ := t.runner.ListBackupVersions(); bs != nil {
			for _, v := range bs {
				backupSet[v] = true
			}
		}
	}
	var versions []string
	for v := range relMap {
		if v != installed && geMin(v) {
			versions = append(versions, v)
		}
	}
	if len(versions) == 0 {
		t.updateMenu(ctx, "Нет доступных версий для отката (>= "+minRollbackVersion+")", kbBackToBackups)
		return
	}
	sortVersionsDesc(versions)

	var rows [][]models.InlineKeyboardButton
	rows = appendVersionRows(rows, "cmd_rollback_to:", versions, backupSet)
	rows = append(rows, []models.InlineKeyboardButton{{Text: "✖️ Отмена", CallbackData: "cmd_backups"}})
	text := "Все версии podkop (>= " + minRollbackVersion + ")\n💾 = есть бэкап конфига:"
	t.updateMenu(ctx, text, &models.InlineKeyboardMarkup{InlineKeyboard: rows})
}

// onRollbackTo stages the chosen rollback target (podkop tags are bare
// versions, so tag == version) and asks to confirm.
func (t *Bot) onRollbackTo(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_rollback_to") {
		return
	}
	version := callbackArg(update, "cmd_rollback_to:")
	if version == "" {
		t.editResult(ctx, "Не удалось разобрать версию")
		return
	}
	t.state.setRollback(version, version)
	hint := ""
	if t.runner != nil && t.runner.HasConfigBackup(version) {
		hint = "\nКонфиг будет восстановлен из бэкапа 💾"
	}
	text := "Откатить podkop\n<b>" + orDash(t.state.installed()) + "</b> → <b>" + version + "</b>?" + hint
	t.updateMenu(ctx, text, kbRollbackConfirm(version))
}

// onRollbackConfirm: actually perform the staged downgrade. Destructive +
// long-running, so it runs under the busy guard.
func (t *Bot) onRollbackConfirm(ctx context.Context, _ *bot.Bot, update *models.Update) {
	release, ok := t.beginHeavy(ctx, update, "cmd_rollback_confirm")
	if !ok {
		return
	}
	defer release()
	t.performDowngrade(ctx)
}

// performDowngrade runs the downgrade flow against the staged rollback target.
// The caller owns the busy guard.
func (t *Bot) performDowngrade(ctx context.Context) {
	target, tag := t.state.rollback()
	if target == "" {
		t.editResult(ctx, "Цель отката не задана")
		return
	}
	t.editBusy(ctx, "Откат podkop до "+target+"...")

	if t.runner == nil {
		t.editResult(ctx, "Готово\n[stub] Откат не реализован")
		return
	}
	status, err := t.runner.RunDowngrade(ctx, target, tag)
	if err != nil {
		logger.Errf("downgrade: %v", err)
		t.editResult(ctx, "Ошибка отката\n"+status)
		return
	}
	t.refreshPodkop(ctx)
	t.editResult(ctx, "Готово\n"+status)
}

// editResultWithRollback renders an action result that also offers a one-tap
// rollback (used when an update left the DNS check unhealthy). The target must
// already be staged via state.setRollback.
func (t *Bot) editResultWithRollback(ctx context.Context, text, target string) {
	kb := &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: "↩️ Откатить на " + target, CallbackData: "cmd_rollback_confirm"}},
			{{Text: "✅ ОК", CallbackData: "cmd_ok"}},
		},
	}
	t.updateMenu(ctx, text, kb)
}

// kbRollbackConfirm is the two-button confirm keyboard for a menu-initiated
// rollback.
func kbRollbackConfirm(target string) *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: "✅ Откатить до " + target, CallbackData: "cmd_rollback_confirm"}},
			{{Text: "✖️ Отмена", CallbackData: "cmd_backups"}},
		},
	}
}
