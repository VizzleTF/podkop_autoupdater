[English version](./README.md)

# podkop_updater

Telegram-бот для роутеров OpenWrt/ImmortalWrt, который следит за релизами
[podkop](https://github.com/itdoginfo/podkop) и позволяет запускать
обновление и перезагрузку прямо из чата. Реализован как единый статический
Go-бинарь, управляемый procd.

## Возможности
- Постоянное меню в Telegram с тремя кнопками: проверить версию podkop,
  проверить версию updater, перезагрузить podkop
- Inline-редактирование: все действия меняют то же самое сообщение через
  состояния «busy → результат»
- Периодическая проверка версии (по умолчанию каждые 6 часов); при
  появлении новой версии podkop бот шлёт свежее уведомление
- 3-уровневый транспорт: SOCKS5 через podkop → прямой → аварийные IP
  Telegram, со sticky последним рабочим тиром
- Аварийные IP обновляются раз в сутки через DoH (используется DNS-сервер,
  настроенный в самом podkop), записываются в UCI и переживают перезагрузку
- Атомарное self-update с `.bak` бэкапом для отката; procd respawn'ит в
  новый бинарь
- Проверка DNS после restart/update — поллит `fakeip.podkop.fyi` до тех
  пор, пока он не отрезолвится в fakeip-диапазон podkop'а

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
uci commit podkop_updater
```

Демон сам пишет обнаруженные аварийные IP в
`podkop_updater.settings.emergency_ips` (через пробел).

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
