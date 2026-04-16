[English version](./README.md)

# Podkop Updater для OpenWrt

Автоматическая проверка обновлений [podkop](https://github.com/itdoginfo/podkop) на роутерах OpenWrt/ImmortalWrt с управлением через Telegram-бот.

## Возможности
- Постоянный Telegram-бот с inline-меню (daemon mode, по умолчанию)
- Две кнопки: «Проверить версию» и «Перезапустить podkop», всегда доступны
- При обнаружении новой версии меню переключается на «Обновить» / «Отменить»
- Автоматическая периодическая проверка версии (настраиваемый интервал)
- 3-уровневый fallback транспорт: SOCKS5 через Podkop → Прямое подключение → Аварийные IP Telegram
- DNS-проверка после обновления/перезапуска
- procd init.d сервис с автоперезапуском при сбое
- Альтернативные режимы: cron с подтверждением в Telegram, cron с автообновлением, ручной

## Требования
- Роутер OpenWrt или ImmortalWrt
- Пакеты: `curl`, `jq`, `wget`, `nslookup` (устанавливаются автоматически)
- Telegram-бот: токен от [@BotFather](https://t.me/BotFather), ID чата от [@getmyid_bot](https://t.me/getmyid_bot)

## Установка
```sh
sh <(curl -sfL https://raw.githubusercontent.com/VizzleTF/podkop_autoupdater/main/install.sh)
```

Установщик проведёт через выбор режима и настройку.

### Режимы обновления
| Режим | Описание |
|-------|----------|
| 1 | Ручной — запуск из консоли, без автоматизации |
| 2 | Автоматический — cron, без Telegram |
| 3 | Cron + подтверждение в Telegram |
| **4 (по умолчанию)** | **Daemon с постоянным меню в Telegram** |

## Использование

| Команда | Описание |
|---------|----------|
| `podkop_updater.sh --daemon` | Запуск постоянного Telegram-бота (используется init.d) |
| `podkop_updater.sh` | Разовая проверка обновлений (подтверждение в Telegram) |
| `podkop_updater.sh --force` | Автообновление без подтверждения |
| `podkop_updater.sh --dry-run` | Тест всего процесса без изменений |

### Управление сервисом (daemon mode)
```sh
/etc/init.d/podkop_updater start
/etc/init.d/podkop_updater stop
/etc/init.d/podkop_updater restart
```

## Настройка

Учётные данные хранятся в UCI (`/etc/config/podkop_updater`):
```sh
uci set podkop_updater.settings.bot_token="ВАШ_ТОКЕН"
uci set podkop_updater.settings.chat_id="ВАШ_CHAT_ID"
uci set podkop_updater.settings.check_interval=6  # часы, только для daemon mode
uci commit podkop_updater
```

## Устранение неполадок

Проверьте логи: `cat /tmp/podkop_update.log`

Частые проблемы:
- **Нет сообщения в Telegram**: Проверьте токен бота и ID чата, доступ к `api.telegram.org`
- **DNS-проверка не прошла**: Нормально, если сервис podkop ещё не запустился
- **Daemon не запускается**: Проверьте `/etc/init.d/podkop_updater status`, просмотрите логи

## Лицензия
[MIT](https://opensource.org/licenses/MIT)
