[English version](./README.md)

# podkop_updater

Один статический Go-бинарь (procd-сервис): следит за релизами
[podkop](https://github.com/itdoginfo/podkop) и управляет
обновлением / откатом / перезагрузкой / бэкапом конфига из Telegram-дашборда.

## Возможности
- Дашборд-меню (карточка статуса + кнопки) и slash-команды: `/menu`,
  `/check_podkop`, `/check_self`, `/check_dns`, `/restart`, `/status`, `/log`
- Периодическая проверка (по умолчанию 6 ч) podkop и updater; авто-обновление опционально для каждого
- Откат podkop на любой релиз ≥ 0.7.0; авто-откат предлагается, если обновление уронило DNS
- Бэкап / восстановление / удаление конфига (версии + дата-время) с ретеншеном;
  при откате конфиг нужной версии восстанавливается сам
- Меню настроек (⚙️), применяется на лету: тумблеры авто-обновления, интервал,
  ретеншен, имя роутера, админы, обновление emergency-IP, показ конфига
- Контроль доступа по Telegram user ID; guard от двойного клика на root-действиях
- `install.sh` с пином на тег релиза; self-update сверяется с опубликованным `.sha256`
- 3-уровневый транспорт: podkop SOCKS5 → прямой → аварийные IP Telegram (DoH, sticky)
- Проверка DNS после каждого restart / update / отката

## Установка
```sh
sh -c "$(curl -sfL https://raw.githubusercontent.com/VizzleTF/podkop_autoupdater/main/install.sh)"
```
Определяет архитектуру, ставит procd-сервис, спрашивает токен бота
([@BotFather](https://t.me/BotFather)) и chat id ([@get_id_bot](https://t.me/get_id_bot)).
Если на роутере есть свой `/usr/bin/podkop_bot` — отключите его. Два демона
на один токен воруют друг у друга обновления.

## Настройки (UCI `/etc/config/podkop_updater`)

| ключ | значение |
|------|----------|
| `bot_token` | токен Telegram-бота (обязательно) |
| `chat_id` | chat id Telegram (обязательно) |
| `check_interval` | часы между проверками (по умолчанию 6) |
| `router_label` | имя жирным в шапке; пусто = hostname |
| `admin_ids` | разрешённые user ID через пробел; пусто = любой в чате |
| `auto_update` | `1` = авто-установка релизов podkop |
| `auto_update_self` | `1` = авто-установка релизов updater (с проверкой sha256) |
| `backup_keep` | сколько бэкапов конфига хранить (0 = без лимита) |

Всё кроме `bot_token`/`chat_id` правится на лету из меню ⚙️ (пишется обратно
в UCI). Демон также ведёт `emergency_ips` и `menu_mid`.

## Лицензия
[MIT](https://opensource.org/licenses/MIT)
