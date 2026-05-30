package telegram

import (
	"context"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/VizzleTF/podkop_autoupdater/go/internal/cfgbackup"
	"github.com/VizzleTF/podkop_autoupdater/go/internal/logger"
)

// kbBackToBackups returns the user to the 📦 Бэкапы submenu after an action.
var kbBackToBackups = &models.InlineKeyboardMarkup{
	InlineKeyboard: [][]models.InlineKeyboardButton{
		{{Text: "‹ К бэкапам", CallbackData: "cmd_backups"}},
	},
}

// onBackups: 📦 Бэкапы — open the backups submenu.
func (t *Bot) onBackups(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_backups") {
		return
	}
	t.sendBackupsMenu(ctx)
}

// onBackup: 💾 Создать бэкап — snapshot the current podkop config under the
// installed version with a fresh timestamp.
func (t *Bot) onBackup(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_backup") {
		return
	}
	if t.runner == nil {
		t.editResult(ctx, "Бэкап недоступен")
		return
	}
	t.editBusy(ctx, "Бэкап конфига...")
	path, err := t.runner.BackupConfig("")
	if err != nil {
		t.updateMenu(ctx, "Ошибка бэкапа конфига\n"+err.Error(), kbBackToBackups)
		return
	}
	t.updateMenu(ctx, "✅ Конфиг сохранён:\n<code>"+escapePre(path)+"</code>", kbBackToBackups)
}

// onRestore: 📂 Восстановить — step 1: pick the version whose config you want
// back. Two-level (version → timestamp) so multiple snapshots per version are
// addressable.
func (t *Bot) onRestore(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_restore") {
		return
	}
	if t.runner == nil {
		t.editResult(ctx, "Восстановление недоступно")
		return
	}
	versions, err := t.runner.ListBackupVersions()
	if err != nil {
		logger.Errf("list backup versions: %v", err)
		t.editResult(ctx, "Не удалось прочитать список бэкапов")
		return
	}
	if len(versions) == 0 {
		t.editResult(ctx, "Нет сохранённых бэкапов конфига\nСначала нажмите «Бэкап конфига»")
		return
	}
	text := "Восстановление конфига — шаг 1\nВыберите версию:"
	t.updateMenu(ctx, text, buildVersionListKB("cmd_restore_ver:", versions, nil))
}

// onRestoreVer: step 2 — list the timestamped backups for the chosen version,
// newest first.
func (t *Bot) onRestoreVer(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_restore_ver") {
		return
	}
	version := callbackArg(update, "cmd_restore_ver:")
	if version == "" || t.runner == nil {
		t.editResult(ctx, "Не удалось разобрать версию")
		return
	}
	entries, err := t.runner.ListBackupsForVersion(version)
	if err != nil || len(entries) == 0 {
		t.editResult(ctx, "Нет бэкапов для версии "+version)
		return
	}
	var rows [][]models.InlineKeyboardButton
	for _, e := range entries {
		rows = append(rows, []models.InlineKeyboardButton{
			{Text: "🕐 " + e.Display(), CallbackData: "cmd_restore_id:" + e.ID},
		})
	}
	rows = append(rows, []models.InlineKeyboardButton{{Text: "✖️ Отмена", CallbackData: "cmd_backups"}})
	text := "Восстановление конфига <b>" + version + "</b> — шаг 2\nВыберите дату/время:"
	t.updateMenu(ctx, text, &models.InlineKeyboardMarkup{InlineKeyboard: rows})
}

// onRestoreID: stage the chosen backup id and ask to confirm.
func (t *Bot) onRestoreID(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_restore_id") {
		return
	}
	id := callbackArg(update, "cmd_restore_id:")
	e, ok := cfgbackup.Parse(id)
	if !ok {
		t.editResult(ctx, "Не удалось разобрать бэкап")
		return
	}
	t.state.setRestore(id)
	text := "Восстановить конфиг <b>" + e.Version + "</b> от <b>" + e.Display() + "</b>?\n" +
		"Пакет podkop не меняется, текущий конфиг сохранится в бэкап."
	t.updateMenu(ctx, text, kbRestoreConfirm(e.Version))
}

// onRestoreConfirm performs the staged config restore under the busy guard.
func (t *Bot) onRestoreConfirm(ctx context.Context, _ *bot.Bot, update *models.Update) {
	release, ok := t.beginHeavy(ctx, update, "cmd_restore_confirm")
	if !ok {
		return
	}
	defer release()
	t.performRestore(ctx)
}

func (t *Bot) performRestore(ctx context.Context) {
	id := t.state.restore()
	if id == "" {
		t.editResult(ctx, "Бэкап для восстановления не выбран")
		return
	}
	label := id
	if e, ok := cfgbackup.Parse(id); ok {
		label = e.Version + " · " + e.Display()
	}
	t.editBusy(ctx, "Восстановление конфига "+label+"...")
	if t.runner == nil {
		t.editResult(ctx, "Восстановление недоступно")
		return
	}
	status, err := t.runner.RestoreConfig(ctx, id)
	if err != nil {
		logger.Errf("restore config: %v", err)
		t.editResult(ctx, "Ошибка восстановления\n"+status)
		return
	}
	t.refreshAll(ctx)
	t.editResult(ctx, "Готово\n"+status)
}

// kbRestoreConfirm is the confirm keyboard for a config restore.
func kbRestoreConfirm(version string) *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: "✅ Восстановить " + version, CallbackData: "cmd_restore_confirm"}},
			{{Text: "✖️ Отмена", CallbackData: "cmd_backups"}},
		},
	}
}

// onDelete: 🗑 Удалить — step 1: pick the version to delete a backup of.
func (t *Bot) onDelete(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_bk_delete") {
		return
	}
	if t.runner == nil {
		t.updateMenu(ctx, "Недоступно", kbBackToBackups)
		return
	}
	versions, err := t.runner.ListBackupVersions()
	if err != nil || len(versions) == 0 {
		t.updateMenu(ctx, "Нет бэкапов для удаления", kbBackToBackups)
		return
	}
	t.updateMenu(ctx, "Удаление бэкапа — шаг 1\nВыберите версию:",
		buildVersionListKB("cmd_del_ver:", versions, nil))
}

// onDeleteVer: step 2 — list the timestamps for the chosen version.
func (t *Bot) onDeleteVer(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_del_ver") {
		return
	}
	version := callbackArg(update, "cmd_del_ver:")
	if version == "" || t.runner == nil {
		t.updateMenu(ctx, "Не удалось разобрать версию", kbBackToBackups)
		return
	}
	entries, err := t.runner.ListBackupsForVersion(version)
	if err != nil || len(entries) == 0 {
		t.updateMenu(ctx, "Нет бэкапов для версии "+version, kbBackToBackups)
		return
	}
	var rows [][]models.InlineKeyboardButton
	for _, e := range entries {
		rows = append(rows, []models.InlineKeyboardButton{
			{Text: "🕐 " + e.Display(), CallbackData: "cmd_del_id:" + e.ID},
		})
	}
	rows = append(rows, []models.InlineKeyboardButton{{Text: "✖️ Отмена", CallbackData: "cmd_backups"}})
	t.updateMenu(ctx, "Удаление бэкапа <b>"+version+"</b> — шаг 2\nВыберите дату/время:",
		&models.InlineKeyboardMarkup{InlineKeyboard: rows})
}

// onDeleteID: stage the backup id and ask to confirm deletion.
func (t *Bot) onDeleteID(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_del_id") {
		return
	}
	id := callbackArg(update, "cmd_del_id:")
	e, ok := cfgbackup.Parse(id)
	if !ok {
		t.updateMenu(ctx, "Не удалось разобрать бэкап", kbBackToBackups)
		return
	}
	t.state.setDelete(id)
	text := "Удалить бэкап <b>" + e.Version + "</b> от <b>" + e.Display() + "</b>?\nЭто действие необратимо."
	t.updateMenu(ctx, text, &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: "🗑 Удалить", CallbackData: "cmd_del_confirm"}},
			{{Text: "✖️ Отмена", CallbackData: "cmd_backups"}},
		},
	})
}

// onDeleteConfirm: delete the staged backup (a plain file removal — no busy
// guard needed) and return to the backups submenu.
func (t *Bot) onDeleteConfirm(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_del_confirm") {
		return
	}
	id := t.state.deleteID()
	if id == "" || t.runner == nil {
		t.updateMenu(ctx, "Бэкап для удаления не выбран", kbBackToBackups)
		return
	}
	if err := t.runner.DeleteBackup(id); err != nil {
		t.updateMenu(ctx, "Ошибка удаления\n"+err.Error(), kbBackToBackups)
		return
	}
	t.state.setDelete("")
	t.sendBackupsMenu(ctx)
}
