[English version](./README.md)

# podkop_updater

Telegram-бот для роутеров OpenWrt/ImmortalWrt, который следит за релизами
[podkop](https://github.com/itdoginfo/podkop) и позволяет запускать
обновление и перезагрузку прямо из чата. Реализован как единый статический
Go-бинарь, управляемый procd.

## Возможности
- Постоянное меню в Telegram: проверить версию podkop, проверить версию
  updater, проверить DNS, перезагрузить podkop, а также кнопки **статус**
  и **лог**
- Inline-редактирование: все действия меняют то же самое сообщение через
  состояния «busy → результат»
- Slash-команды дублируют кнопки: `/menu`, `/check_podkop`, `/check_self`,
  `/check_dns`, `/restart`, `/status`, `/log`
- Периодическая проверка (по умолчанию каждые 6 часов) **и** podkop, **и**
  самого updater; новая версия шлёт свежее уведомление
- Опциональное **авто-обновление**: при включении новые релизы podkop
  ставятся сами, а не просто уведомляют (сам updater всегда только
  уведомляет)
- **Контроль доступа**: ограничить команды конкретными Telegram user ID;
  **guard от двойного клика** не даёт запустить два `install.sh` от root
  одновременно
- **Защита цепочки поставки**: `install.sh` подкопа качается с пином на
  тег релиза (не HEAD ветки); self-update сверяет бинарь с опубликованным
  `.sha256`
- 3-уровневый транспорт: SOCKS5 через podkop → прямой → аварийные IP
  Telegram, со sticky последним рабочим тиром, который периодически
  сбрасывается обратно на основной путь
- Аварийные IP обновляются раз в сутки через DoH (параллельные запросы),
  записываются в UCI и переживают перезагрузку
- Атомарное self-update с `.bak` бэкапом для отката; procd respawn'ит в
  новый бинарь
- Проверка DNS после restart/update — поллит `fakeip.podkop.fyi` до тех
  пор, пока он не отрезолвится в fakeip-диапазон podkop'а; провал после
  обновления выводится явно

## Требования
- Роутер OpenWrt или ImmortalWrt (поддерживаемые архитектуры: amd64, arm64,
  armv7, mipsle softfloat, mips softfloat)
- Токен Telegram-бота (от [@BotFather](https://t.me/BotFather)) и chat_id
  (от [@get_id_bot](https://t.me/get_id_bot))

## Установка

```sh
sh -c "$(curl -sfL https://raw.githubusercontent.com/VizzleTF/podkop_autoupdater/main/install.sh)"
```

Скрипт определяет архитектуру, останавливает старую версию (bash или Go),
качает подходящий бинарь из последнего релиза GitHub, ставит procd-сервис
и спрашивает токен/chat_id, если их ещё нет в UCI.

Если на роутере есть собственный бот подкопа `/usr/bin/podkop_bot` —
остановите и отключите его. Два демона на один и тот же токен будут
воровать друг у друга обновления.

## Конфигурация

Настройки хранятся в UCI (`/etc/config/podkop_updater`):

```sh
uci set podkop_updater.settings.bot_token="ВАШ_ТОКЕН"
uci set podkop_updater.settings.chat_id="ВАШ_CHAT_ID"
uci set podkop_updater.settings.check_interval=6   # часы
uci set podkop_updater.settings.router_label="Дом"  # опционально: показывается в шапке сообщения
uci set podkop_updater.settings.admin_ids="123456789 987654321"  # опционально: разрешённые user ID
uci set podkop_updater.settings.auto_update=1   # опционально: авто-установка релизов podkop
uci commit podkop_updater
```

`router_label` опционально. Удобно задать, когда несколько демонов (с
разными ботами) пишут в один общий чат или топик супергруппы — тогда в
шапке каждого сообщения жирным выводится имя роутера, и видно, от кого
оно пришло. Если поле не задано, в шапку идёт hostname системы.

`admin_ids` — опциональный список Telegram user ID через пробел. Если
задан, команды (и кнопки, и slash) разрешены только этим пользователям
(проверка по `From.ID`), остальным — alert «Нет доступа». Если пусто —
команды доступны любому в настроенном чате.

`auto_update` (`1`/`true`) — периодическая проверка ставит новые релизы
podkop сама, а не просто уведомляет. Сам updater никогда не обновляет
себя автоматически, чтобы плохой self-релиз не сломал бота молча.

Демон сам пишет обнаруженные аварийные IP в
`podkop_updater.settings.emergency_ips` (через пробел) и хранит id
сообщения-меню в `podkop_updater.settings.menu_mid`.

## Управление сервисом

```sh
/etc/init.d/podkop_updater start
/etc/init.d/podkop_updater stop
/etc/init.d/podkop_updater restart
```

Логи: `/tmp/podkop_update.log` (in-place ротация при ~200 строках).

## Сборка из исходников

```sh
cd go
make build           # под текущий хост
make build-all       # кросс-компиляция под 5 архитектур OpenWrt
make upx             # сжать UPX'ом (нужен установленный upx)
```

Детали архитектуры — в [`go/DESIGN.md`](./go/DESIGN.md).

## Лицензия
[MIT](https://opensource.org/licenses/MIT)
