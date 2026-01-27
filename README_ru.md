[English version](./README.md)

# Podkop Updater для OpenWrt

Автоматическая проверка обновлений [podkop](https://github.com/itdoginfo/podkop) на роутерах OpenWrt/ImmortalWrt.

## Возможности
- Проверка последней версии через GitHub API
- Три режима: ручной, автоматический (`--force`), с подтверждением в Telegram (по умолчанию)
- Тестовый режим без изменений (`--dry-run`)
- DNS-проверка после обновления для контроля работоспособности podkop
- Long polling для эффективной обработки ответов Telegram

## Требования
- Роутер OpenWrt или ImmortalWrt
- Пакеты: `curl`, `jq`, `wget`, `nslookup`
- Telegram-бот (для режима подтверждения): токен от [@BotFather](https://t.me/BotFather), ID чата от [@getmyid_bot](https://t.me/getmyid_bot)

## Установка
```sh
sh <(wget -O - https://raw.githubusercontent.com/VizzleTF/podkop_autoupdater/main/install.sh)
```

Установщик проведёт через выбор режима и настройку.

## Использование

| Команда | Описание |
|---------|----------|
| `podkop_updater.sh` | Проверка обновлений (режим Telegram) |
| `podkop_updater.sh --force` | Автообновление без подтверждения |
| `podkop_updater.sh --dry-run` | Тест всего процесса без изменений |

В режиме Telegram отвечайте на сообщение бота `yes` или `no` (через Reply).

## Настройка

Редактируйте `/usr/bin/podkop_updater.sh`:
```sh
BOT_TOKEN="your_bot_token"
CHAT_ID="your_chat_id"
```

Основные таймауты (в секундах):
- `POLL_TIMEOUT=3300` — Максимальное ожидание ответа в Telegram (~55 мин)
- `DNS_CHECK_DELAY=60` — Задержка перед DNS-проверкой после обновления

## Устранение неполадок

Проверьте логи: `cat /tmp/podkop_update.log`

Частые проблемы:
- **Нет сообщения в Telegram**: Проверьте `BOT_TOKEN` и `CHAT_ID`, доступ к `api.telegram.org`
- **Ответ не распознан**: Нужно отвечать на сообщение (функция Reply в Telegram)
- **DNS-проверка не прошла**: Нормально, если сервис podkop ещё не запустился

## Лицензия
[MIT](https://opensource.org/licenses/MIT)
