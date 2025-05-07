[English version](./README.md)

# Обновление Podkop для OpenWrt

Этот скрипт (`podkop_updater.sh`) автоматизирует проверку обновлений пакета `podkop` на маршрутизаторе OpenWrt или ImmortalWrt. Поддерживаются три режима: ручное обновление через консоль, автоматическое обновление без подтверждения и автоматическое обновление с подтверждением через Telegram-бота (по умолчанию).

## Возможности
- Проверяет последнюю версию `podkop` через API GitHub.
- Сравнивает с установленной версией с помощью `opkg`.
- Поддерживает три режима:
  - **Ручной**: Запуск через консоль без cron (`/usr/bin/podkop_updater.sh`).
  - **Автоматический**: Запуск через cron без подтверждения Telegram (`--force`).
  - **Telegram**: Запуск через cron с подтверждением через Telegram-бота (по умолчанию).
- Отправляет сообщения в Telegram для подтверждения, успешного или неуспешного обновления в режиме Telegram.
- Автоматизирует ответы на запросы скрипта обновления: обновление `podkop` и установка русской локализации.
- Выполняет проверку DNS после успешного обновления, используя домен из переменной `TEST_DOMAIN` в `/usr/bin/podkop`, для подтверждения работоспособности `podkop`.
- Логирует действия в `/tmp/podkop_update.log`.
- Поддерживает регистронезависимые ответы в Telegram ("yes", "Yes", "YES" и т.д.).

## Требования
- **Маршрутизатор OpenWrt или ImmortalWrt** с доступом в интернет.
- **Установленные пакеты**:
  - `curl`: Для запросов к API (обычно предустановлен).
  - `jq`: Для обработки JSON (зависимость `podkop`).
  - `wget`: Для загрузки скрипта обновления.
  - `nslookup`: Для проверки DNS после обновления (предоставляется `busybox` или `bind-tools`).
- **Telegram-бот** (для режима Telegram):
  - Создайте бота через [@BotFather](https://t.me/BotFather) и получите токен.
  - Получите ID чата через [@get_id_bot](https://t.me/get_id_bot) или аналогичный сервис.
- **Сетевой доступ** к:
  - API GitHub (`api.github.com`).
  - API Telegram (`api.telegram.org`) для режима Telegram.
  - Репозиториям пакетов OpenWrt/ImmortalWrt и URL скрипта обновления `podkop`.

## Установка
Запустите скрипт установщика одной командой:
```sh
sh <(wget -O - https://raw.githubusercontent.com/VizzleTF/podkop_autoupdater/refs/heads/main/install.sh)
```
Установщик:
- Установит необходимые пакеты (`curl`, `jq`, `wget`).
- Загрузит `podkop_updater.sh` в `/usr/bin/`.
- Запросит режим обновления:
  - Ручной: Устанавливает скрипт без cron или настройки Telegram.
  - Автоматический: Запрашивает частоту cron (в часах).
  - Telegram (по умолчанию): Запрашивает частоту cron, токен бота и ID чата.
- Настроит скрипт и задание cron при необходимости.
- Проверит сетевой доступ к API GitHub и Telegram.

## Ручная установка
1. **Сохранение скрипта**:
   ```sh
   wget -O /usr/bin/podkop_updater.sh https://raw.githubusercontent.com/VizzleTF/podkop_autoupdater/refs/heads/main/podkop_updater.sh
   chmod +x /usr/bin/podkop_updater.sh
   ```
2. **Настройка Telegram** (для режима Telegram):
   - Отредактируйте скрипт (`vi /usr/bin/podkop_updater.sh`).
   - Замените `your_bot_token` на токен Telegram-бота.
   - Замените `your_chat_id` на ID чата.
3. **Проверка зависимостей**:
   ```sh
   opkg update && opkg install curl jq wget
   ```

## Использование
1. **Ручной режим**:
   ```sh
   /usr/bin/podkop_updater.sh
   ```
   - Проверяет обновления и отправляет сообщение в Telegram, если доступна новая версия:
     ```
     Доступна новая версия: 0.3.43. Текущая: 0.3.41-1. Ответьте на это сообщение "yes" для обновления или "no" для отмены.
     ```
   - Ответьте **непосредственно** на сообщение "yes" для обновления или "no" для отмены.
   - После попытки обновления вы получите сообщение в Telegram с результатом:
     - Успех:
       ```
       Обновление до версии 0.3.43 выполнено успешно.
       Проверка DNS пройдена: fakeip.podkop.fyi разрешено в 198.18.x.x
       ```
     - Неудача:
       ```
       Обновление до версии 0.3.43 не удалось: Ошибка выполнения скрипта обновления. Проверка DNS не выполнялась.
       ```
     - Неудача проверки DNS из-за отсутствия TEST_DOMAIN:
       ```
       Обновление до версии 0.3.43 выполнено успешно.
       Проверка DNS не удалась: Не удалось получить TEST_DOMAIN из /usr/bin/podkop.
       ```
   - Действия логируются в `/tmp/podkop_update.log`.
2. **Автоматический режим (через cron)**:
   - Настраивается установщиком с параметром `--force`:
     ```sh
     /usr/bin/podkop_updater.sh --force
     ```
   - Выполняет обновление автоматически без подтверждения или уведомлений Telegram.
3. **Режим Telegram (через cron)**:
   - Настраивается установщиком (по умолчанию).
   - Периодически запускается, отправляет сообщения в Telegram для подтверждения и ждет ответа 5 минут.
   - Отправляет сообщение в Telegram с результатом обновления (успех или неудача).

## Как это работает
1. **Проверка версии**:
   - Загружает последнюю версию `podkop` с [GitHub](https://api.github.com/repos/itdoginfo/podkop/releases/latest).
   - Получает установленную версию через `opkg info podkop`.
2. **Обработка режима**:
   - **Ручной/Telegram**: Отправляет сообщение в Telegram и ждет ответа "yes" или "no".
   - **Автоматический**: Выполняет обновление немедленно, если используется `--force`.
3. **Обновление**:
   - Запускает скрипт обновления (`https://raw.githubusercontent.com/itdoginfo/podkop/refs/heads/main/install.sh`).
   - Автоматически отвечает на два запроса:
     - "Просто обновить podkop?" → `y`
     - "Нужен русский перевод?" → `y`
4. **Проверка после обновления** (при успешном обновлении):
   - Извлекает `TEST_DOMAIN` из `/usr/bin/podkop`.
   - Если `TEST_DOMAIN` не найден, логирует ошибку и, в режиме Telegram, отправляет уведомление.
   - Ждет 1 минуту после успешного обновления.
   - Выполняет `nslookup -timeout=2 $TEST_DOMAIN 127.0.0.42`.
   - Проверяет, разрешен ли IP в диапазоне `198.18.0.0/16` (например, `198.18.0.181`).
   - Логирует успех, если IP соответствует, или неудачу, если нет.
5. **Уведомление Telegram** (только в режиме Telegram):
   - Отправляется после попытки обновления:
     - Успех: Сообщает об успешном обновлении и результате проверки DNS (или неудаче из-за отсутствия `TEST_DOMAIN`).
     - Неудача: Сообщает о неудаче обновления (например, отсутствие wget, ошибка загрузки скрипта, ошибка выполнения) и указывает, что проверка DNS не выполнялась.
   - Примеры сообщений:
     ```
     Обновление до версии 0.3.43 выполнено успешно.
     Проверка DNS пройдена: fakeip.podkop.fyi разрешено в 198.18.x.x
     ```
     ```
     Обновление до версии 0.3.43 не удалось: Ошибка выполнения скрипта обновления. Проверка DNS не выполнялась.
     ```
     ```
     Обновление до версии 0.3.43 выполнено успешно.
     Проверка DNS не удалась: Не удалось получить TEST_DOMAIN из /usr/bin/podkop.
     ```
6. **Логирование**:
   - Все действия, включая проверку после обновления и уведомления Telegram, логируются в `/tmp/podkop_update.log`.

## Устранение неполадок
- **Сообщение Telegram не отправлено**:
  - Проверьте `/tmp/podkop_update.log` на наличие ошибок (например, "Cannot connect to Telegram API" или "Failed to send Telegram notification").
  - Убедитесь в правильности `BOT_TOKEN` и `CHAT_ID`.
  - Проверьте доступ к `api.telegram.org`:
    ```sh
    ping api.telegram.org
    curl -s https://api.telegram.org/bot<your_bot_token>/getMe
    ```
- **Ответ "yes" не обнаружен**:
  - Убедитесь, что вы отвечаете **непосредственно** на сообщение бота (нажмите "Ответить" в Telegram).
  - Проверьте настройки приватности бота в [@BotFather](https://t.me/BotFather):
    ```sh
    /setprivacy -> Выберите вашего бота -> Отключить
    ```
  - Выполните запрос к API Telegram:
    ```sh
    curl -s "https://api.telegram.org/bot<your_bot_token>/getUpdates"
    ```
    Ищите ответ "yes" с `reply_to_message.message_id`, соответствующим ID сообщения бота.
- **Сбой скрипта обновления**:
  - Протестируйте скрипт обновления вручную:
    ```sh
    echo -e "y\ny\n" | sh <(wget -O - https://raw.githubusercontent.com/itdoginfo/podkop/refs/heads/main/install.sh)
    ```
  - Проверьте `/tmp/podkop_update.log` на наличие ошибок (например, "Failed to fetch update script", "Update script execution error").
  - Убедитесь, что `wget` установлен и на маршрутизаторе достаточно памяти/места:
    ```sh
    df -h
    free
    ```
  - В режиме Telegram ожидайте уведомление о неудаче, например, "Обновление до версии X не удалось: Ошибка выполнения скрипта обновления."
- **Неудачная проверка после обновления**:
  - Убедитесь, что `nslookup` доступен:
    ```sh
    nslookup -timeout=2 $(grep 'TEST_DOMAIN=' /usr/bin/podkop | cut -d'"' -f2) 127.0.0.42
    ```
  - Проверьте вывод `nslookup` в `/tmp/podkop_update.log` или ошибку "Failed to retrieve TEST_DOMAIN".
  - Убедитесь, что `/usr/bin/podkop` существует и содержит корректный `TEST_DOMAIN`:
    ```sh
    grep 'TEST_DOMAIN=' /usr/bin/podkop
    ```
  - Убедитесь, что службы `podkop` запущены и DNS настроен корректно.
  - Если IP не в диапазоне `198.18.0.0/16`, `podkop` может работать некорректно.
- **Новая версия не обнаружена**:
  - Проверьте доступ к API GitHub:
    ```sh
    curl -s https://api.github.com/repos/itdoginfo/podkop/releases/latest
    ```
  - Проверьте установленную версию:
    ```sh
    opkg info podkop
    ```

## Пример лога
Успешный запуск в режиме Telegram:
```
Starting podkop update check at Fri May 2 14:00:00 UTC 2025
Telegram API connection successful
Latest version: 0.3.43
Installed version: 0.3.41-1
New version available: 0.3.43 (current: 0.3.41-1)
Sent Telegram message, ID: 1501
Initial offset: 2
Polling updates, offset: 2
Updates response: {"ok":true,"result":[{"update_id":2,"message":{"message_id":1502,"chat":{"id":<chat_id>},"text":"yes","reply_to_message":{"message_id":1501}}}]}
Update requested (yes response detected)
[output from install.sh, including package downloads and installation]
Update script executed successfully
Retrieving TEST_DOMAIN from /usr/bin/podkop...
Waiting 1 minute before performing post-update DNS check...
Running nslookup check for fakeip.podkop.fyi...
Server:         127.0.0.42
Address:        127.0.0.42:53
Non-authoritative answer:
Name:   fakeip.podkop.fyi
Address: 198.18.0.181
Post-update check passed: fakeip.podkop.fyi resolved to 198.18.x.x (podkop is working)
Sent Telegram notification: Update to version 0.3.43 succeeded. DNS check passed: fakeip.podkop.fyi resolved to 198.18.x.x
```

Неуспешный запуск в режиме Telegram (ошибка выполнения скрипта обновления):
```
Starting podkop update check at Fri May 2 14:00:00 UTC 2025
Telegram API connection successful
Latest version: 0.3.43
Installed version: 0.3.41-1
New version available: 0.3.43 (current: 0.3.41-1)
Sent Telegram message, ID: 1501
Initial offset: 2
Polling updates, offset: 2
Updates response: {"ok":true,"result":[{"update_id":2,"message":{"message_id":1502,"chat":{"id":<chat_id>},"text":"yes","reply_to_message":{"message_id":1501}}}]}
Update requested (yes response detected)
[output from install.sh, with error]
Error: Update script failed
Sent Telegram notification: Update to version 0.3.43 failed: Update script execution error. No DNS check performed.
```

Успешный запуск в режиме Telegram с отсутствующим TEST_DOMAIN:
```
Starting podkop update check at Fri May 2 14:00:00 UTC 2025
Telegram API connection successful
Latest version: 0.3.43
Installed version: 0.3.41-1
New version available: 0.3.43 (current: 0.3.41-1)
Sent Telegram message, ID: 1501
Initial offset: 2
Polling updates, offset: 2
Updates response: {"ok":true,"result":[{"update_id":2,"message":{"message_id":1502,"chat":{"id":<chat_id>},"text":"yes","reply_to_message":{"message_id":1501}}}]}
Update requested (yes response detected)
[output from install.sh, including package downloads and installation]
Update script executed successfully
Retrieving TEST_DOMAIN from /usr/bin/podkop...
Error: Failed to retrieve TEST_DOMAIN from /usr/bin/podkop
Sent Telegram notification: Update to version 0.3.43 succeeded. DNS check failed: Could not retrieve TEST_DOMAIN from /usr/bin/podkop.
```

Успешный запуск в автоматическом режиме:
```
Starting podkop update check at Fri May 2 14:00:00 UTC 2025
Running in force mode (automatic update without Telegram)
Latest version: 0.3.43
Installed version: 0.3.41-1
New version available: 0.3.43 (current: 0.3.41-1)
Proceeding with automatic update
[output from install.sh]
Update script executed successfully
Retrieving TEST_DOMAIN from /usr/bin/podkop...
Waiting 1 minute before performing post-update DNS check...
Running nslookup check for fakeip.podkop.fyi...
Server:         127.0.0.42
Address:        127.0.0.42:53
Non-authoritative answer:
Name:   fakeip.podkop.fyi
Address: 198.18.0.181
Post-update check passed: fakeip.podkop.fyi resolved to 198.18.x.x (podkop is working)
```

## Лицензия
Скрипт распространяется под [лицензией MIT](https://opensource.org/licenses/MIT).

## Вклад
Присылайте вопросы или запросы на включение изменений для улучшения скрипта. Приветствуются предложения по улучшению обработки ошибок, добавлению функций или поддержке других языков.

## Благодарности
Разработано для автоматизации обновлений `podkop` на маршрутизаторах OpenWrt и ImmortalWrt с использованием скрипта установки проекта [podkop](https://github.com/itdoginfo/podkop).