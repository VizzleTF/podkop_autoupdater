package telegram

import (
	"context"
	"html"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/VizzleTF/podkop_autoupdater/go/internal/logger"
)

// SettingsStore persists the runtime-editable settings to UCI. Implemented in
// main against the config package; nil disables persistence (in-memory only).
type SettingsStore interface {
	SaveAutoUpdate(bool) error
	SaveAutoUpdateSelf(bool) error
	SaveCheckInterval(hours int) error
	SaveRouterLabel(string) error
	SaveAdminIDs([]int64) error
	SaveBackupKeep(int) error
}

// botSettings holds the mutable, runtime-editable configuration behind an
// RWMutex. Reads come from many goroutines (ACL checks, menu rendering, the
// periodic checker); writes come from the settings menu.
type botSettings struct {
	mu             sync.RWMutex
	autoUpdate     bool
	autoUpdateSelf bool
	checkHours     int
	label          string
	admins         map[int64]bool
	backupKeep     int

	store        SettingsStore
	onInterval   func()    // signal the periodic checker to reset its timer
	onBackupKeep func(int) // push the new retention limit to the runner
}

func newBotSettings(autoUpdate, autoUpdateSelf bool, checkHours int, label string, admins []int64, backupKeep int, store SettingsStore) *botSettings {
	m := make(map[int64]bool, len(admins))
	for _, id := range admins {
		m[id] = true
	}
	if checkHours <= 0 {
		checkHours = 6
	}
	return &botSettings{
		autoUpdate:     autoUpdate,
		autoUpdateSelf: autoUpdateSelf,
		checkHours:     checkHours,
		label:          label,
		admins:         m,
		backupKeep:     backupKeep,
		store:          store,
	}
}

func (s *botSettings) AutoUpdate() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.autoUpdate
}

func (s *botSettings) AutoUpdateSelf() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.autoUpdateSelf
}

func (s *botSettings) CheckHours() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.checkHours
}

func (s *botSettings) CheckInterval() time.Duration {
	return time.Duration(s.CheckHours()) * time.Hour
}

func (s *botSettings) Label() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.label
}

func (s *botSettings) BackupKeep() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.backupKeep
}

// Allowed reports whether userID may issue commands (empty admin set = anyone).
func (s *botSettings) Allowed(userID int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.admins) == 0 || s.admins[userID]
}

func (s *botSettings) AdminIDs() []int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]int64, 0, len(s.admins))
	for id := range s.admins {
		out = append(out, id)
	}
	return out
}

func (s *botSettings) SetAutoUpdate(v bool) {
	s.mu.Lock()
	s.autoUpdate = v
	s.mu.Unlock()
	if s.store != nil {
		if err := s.store.SaveAutoUpdate(v); err != nil {
			logger.Errf("persist auto_update: %v", err)
		}
	}
}

func (s *botSettings) SetAutoUpdateSelf(v bool) {
	s.mu.Lock()
	s.autoUpdateSelf = v
	s.mu.Unlock()
	if s.store != nil {
		if err := s.store.SaveAutoUpdateSelf(v); err != nil {
			logger.Errf("persist auto_update_self: %v", err)
		}
	}
}

func (s *botSettings) SetCheckHours(h int) {
	if h <= 0 {
		h = 1
	}
	s.mu.Lock()
	s.checkHours = h
	s.mu.Unlock()
	if s.store != nil {
		if err := s.store.SaveCheckInterval(h); err != nil {
			logger.Errf("persist check_interval: %v", err)
		}
	}
	if s.onInterval != nil {
		s.onInterval()
	}
}

func (s *botSettings) SetLabel(label string) {
	s.mu.Lock()
	s.label = label
	s.mu.Unlock()
	if s.store != nil {
		if err := s.store.SaveRouterLabel(label); err != nil {
			logger.Errf("persist router_label: %v", err)
		}
	}
}

func (s *botSettings) SetBackupKeep(n int) {
	if n < 0 {
		n = 0
	}
	s.mu.Lock()
	s.backupKeep = n
	s.mu.Unlock()
	if s.store != nil {
		if err := s.store.SaveBackupKeep(n); err != nil {
			logger.Errf("persist backup_keep: %v", err)
		}
	}
	if s.onBackupKeep != nil {
		s.onBackupKeep(n)
	}
}

func (s *botSettings) AddAdmin(id int64) {
	s.mu.Lock()
	if s.admins == nil {
		s.admins = map[int64]bool{}
	}
	s.admins[id] = true
	s.mu.Unlock()
	s.persistAdmins()
}

func (s *botSettings) ClearAdmins() {
	s.mu.Lock()
	s.admins = map[int64]bool{}
	s.mu.Unlock()
	s.persistAdmins()
}

func (s *botSettings) persistAdmins() {
	if s.store != nil {
		if err := s.store.SaveAdminIDs(s.AdminIDs()); err != nil {
			logger.Errf("persist admin_ids: %v", err)
		}
	}
}

// ---- Settings menu ----

// onSettings: ⚙️ Настройки — render the settings submenu.
func (t *Bot) onSettings(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_settings") {
		return
	}
	t.state.clearAwait() // entering settings cancels any pending text input
	t.sendSettingsMenu(ctx)
}

func (t *Bot) sendSettingsMenu(ctx context.Context) {
	keep := "∞"
	if k := t.set.BackupKeep(); k > 0 {
		keep = strconv.Itoa(k)
	}
	label := t.set.Label()
	if label == "" {
		label = "(hostname)"
	}
	kb := &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: "🤖 Авто-обн. podkop: " + onoff(t.set.AutoUpdate()), CallbackData: "cmd_set_autoupd"}},
			{{Text: "⬆️ Авто-обн. updater: " + onoff(t.set.AutoUpdateSelf()), CallbackData: "cmd_set_autoupd_self"}},
			{{Text: "⏱ Интервал проверки: " + strconv.Itoa(t.set.CheckHours()) + " ч", CallbackData: "cmd_set_interval"}},
			{{Text: "🧹 Хранить бэкапов: " + keep, CallbackData: "cmd_set_keep"}},
			{{Text: "🏷 Имя роутера: " + label, CallbackData: "cmd_set_label"}},
			{{Text: "👤 Админы: " + strconv.Itoa(len(t.set.AdminIDs())), CallbackData: "cmd_set_admins"}},
			{{Text: "📡 Обновить emergency-IP", CallbackData: "cmd_set_ipref"}},
			{{Text: "ℹ️ Показать конфиг", CallbackData: "cmd_set_show"}},
			{{Text: "‹ Назад", CallbackData: "cmd_ok"}},
		},
	}
	t.updateMenu(ctx, "⚙️ <b>Настройки</b>", kb)
}

// onSetAutoUpd toggles podkop auto-update.
func (t *Bot) onSetAutoUpd(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_set_autoupd") {
		return
	}
	t.set.SetAutoUpdate(!t.set.AutoUpdate())
	t.sendSettingsMenu(ctx)
}

// onSetAutoUpdSelf toggles updater (self) auto-update.
func (t *Bot) onSetAutoUpdSelf(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_set_autoupd_self") {
		return
	}
	t.set.SetAutoUpdateSelf(!t.set.AutoUpdateSelf())
	t.sendSettingsMenu(ctx)
}

// onSetInterval shows the check-interval presets.
func (t *Bot) onSetInterval(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_set_interval") {
		return
	}
	row := func(hs ...int) []models.InlineKeyboardButton {
		var r []models.InlineKeyboardButton
		for _, h := range hs {
			r = append(r, models.InlineKeyboardButton{Text: strconv.Itoa(h) + " ч", CallbackData: "cmd_set_int:" + strconv.Itoa(h)})
		}
		return r
	}
	kb := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		row(1, 3, 6), row(12, 24, 48),
		{{Text: "‹ Назад", CallbackData: "cmd_settings"}},
	}}
	t.updateMenu(ctx, "⏱ Интервал проверки версий:", kb)
}

func (t *Bot) onSetInt(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_set_int") {
		return
	}
	if n, err := strconv.Atoi(callbackArg(update, "cmd_set_int:")); err == nil && n > 0 {
		t.set.SetCheckHours(n)
	}
	t.sendSettingsMenu(ctx)
}

// onSetKeep shows the backup-retention presets.
func (t *Bot) onSetKeep(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_set_keep") {
		return
	}
	kb := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{
			{Text: "5", CallbackData: "cmd_set_keep_n:5"},
			{Text: "10", CallbackData: "cmd_set_keep_n:10"},
			{Text: "20", CallbackData: "cmd_set_keep_n:20"},
			{Text: "∞", CallbackData: "cmd_set_keep_n:0"},
		},
		{{Text: "‹ Назад", CallbackData: "cmd_settings"}},
	}}
	t.updateMenu(ctx, "🧹 Сколько бэкапов конфига хранить (старые удаляются автоматически):", kb)
}

func (t *Bot) onSetKeepN(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_set_keep_n") {
		return
	}
	if n, err := strconv.Atoi(callbackArg(update, "cmd_set_keep_n:")); err == nil && n >= 0 {
		t.set.SetBackupKeep(n)
	}
	t.sendSettingsMenu(ctx)
}

// onSetIPRefresh forces an emergency-IP DoH refresh.
func (t *Bot) onSetIPRefresh(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_set_ipref") {
		return
	}
	if t.refreshIPs == nil {
		t.updateMenu(ctx, "Обновление IP недоступно", kbBackToSettings)
		return
	}
	t.editBusy(ctx, "Обновление emergency-IP через DoH...")
	t.refreshIPs(ctx)
	tiers := ""
	if t.tiers != nil {
		tiers = "\nТиры: " + strings.Join(t.tiers.Tiers(), ", ")
	}
	t.updateMenu(ctx, "✅ Emergency-IP обновлены"+tiers, kbBackToSettings)
}

// onSetShow dumps the current configuration (token masked).
func (t *Bot) onSetShow(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_set_show") {
		return
	}
	keep := "∞"
	if k := t.set.BackupKeep(); k > 0 {
		keep = strconv.Itoa(k)
	}
	label := t.set.Label()
	if label == "" {
		label = "(hostname)"
	}
	admins := "все в чате"
	if ids := t.set.AdminIDs(); len(ids) > 0 {
		parts := make([]string, len(ids))
		for i, id := range ids {
			parts[i] = strconv.FormatInt(id, 10)
		}
		admins = strings.Join(parts, ", ")
	}
	var b strings.Builder
	b.WriteString("ℹ️ <b>Конфигурация</b>\n")
	b.WriteString("chat_id: <code>" + strconv.FormatInt(t.chatID, 10) + "</code>\n")
	b.WriteString("Авто-обн. podkop: " + onoff(t.set.AutoUpdate()) + "\n")
	b.WriteString("Авто-обн. updater: " + onoff(t.set.AutoUpdateSelf()) + "\n")
	b.WriteString("Интервал проверки: " + strconv.Itoa(t.set.CheckHours()) + " ч\n")
	b.WriteString("Хранить бэкапов: " + keep + "\n")
	b.WriteString("Имя роутера: " + html.EscapeString(label) + "\n")
	b.WriteString("Админы: " + html.EscapeString(admins))
	t.updateMenu(ctx, b.String(), kbBackToSettings)
}

// onSetLabel prompts for a new router label via a text reply.
func (t *Bot) onSetLabel(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_set_label") {
		return
	}
	t.state.setAwait(awaitLabel)
	t.updateMenu(ctx, "🏷 Пришлите новое имя роутера одним сообщением.\nПустое сообщение «-» сбросит на hostname.", kbCancelInput)
}

// onSetAdmins renders the admins management submenu.
func (t *Bot) onSetAdmins(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_set_admins") {
		return
	}
	t.sendAdminsMenu(ctx)
}

func (t *Bot) sendAdminsMenu(ctx context.Context) {
	ids := t.set.AdminIDs()
	text := "👤 <b>Админы</b> (кому можно команды)\n"
	if len(ids) == 0 {
		text += "Сейчас: <i>любой в чате</i>"
	} else {
		parts := make([]string, len(ids))
		for i, id := range ids {
			parts[i] = strconv.FormatInt(id, 10)
		}
		text += "Сейчас: <code>" + strings.Join(parts, ", ") + "</code>"
	}
	kb := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{{Text: "➕ Добавить", CallbackData: "cmd_set_admin_add"}},
		{{Text: "🗑 Очистить (открыть всем)", CallbackData: "cmd_set_admin_clear"}},
		{{Text: "‹ Назад", CallbackData: "cmd_settings"}},
	}}
	t.updateMenu(ctx, text, kb)
}

func (t *Bot) onSetAdminAdd(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_set_admin_add") {
		return
	}
	t.state.setAwait(awaitAdminAdd)
	t.updateMenu(ctx, "➕ Пришлите Telegram user ID для добавления в админы (число).", kbCancelInput)
}

func (t *Bot) onSetAdminClear(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if !t.handleCallback(ctx, update, "cmd_set_admin_clear") {
		return
	}
	t.set.ClearAdmins()
	t.sendAdminsMenu(ctx)
}

// handleAwaitedInput consumes a plain text message when the settings menu is
// waiting for one (router label or admin id). Returns true if it handled the
// message.
func (t *Bot) handleAwaitedInput(ctx context.Context, text string) bool {
	kind := t.state.await()
	if kind == awaitNone {
		return false
	}
	t.state.clearAwait()
	text = strings.TrimSpace(text)
	switch kind {
	case awaitLabel:
		if text == "-" {
			text = ""
		}
		t.set.SetLabel(text)
		t.sendSettingsMenu(ctx)
	case awaitAdminAdd:
		id, err := strconv.ParseInt(text, 10, 64)
		if err != nil || id == 0 {
			t.updateMenu(ctx, "Не похоже на user ID. Отменено.", kbBackToSettings)
			return true
		}
		t.set.AddAdmin(id)
		t.sendAdminsMenu(ctx)
	}
	return true
}

var (
	kbBackToSettings = &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: "‹ К настройкам", CallbackData: "cmd_settings"}},
		},
	}
	kbCancelInput = &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: "✖️ Отмена", CallbackData: "cmd_settings"}},
		},
	}
)
