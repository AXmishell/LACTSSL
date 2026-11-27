# Интеграция по API
Интерфейсы консоли LACTSSL доступны через сеанс браузера или API-ключ. Ключи подходят для CI/CD, мониторинга, синхронизации с CMDB и распространения сертификатов; для каждого настраиваются права, срок и состояние.

> Безопасность учётной записи, ключи доступа и управление сеансами доступны только из сеанса браузера. API-ключи не могут изменить пароль, привязать ключ доступа или завершить сеансы.

## Выбор способа интеграции
| Сценарий | Рекомендуемый способ | Причина |
| --- | --- | --- |
| Ручной выпуск, устранение неполадок, настройка | Консоль браузера | Полный аудит и интерактивное подтверждение |
| Продление/развёртывание из CI/CD | API-ключ + `tasks:run` / `deploy:execute` | Минимальные права и ротация |
| Синхронизация с CMDB, аудит, мониторинг | API-ключ только для чтения (`system:read`, `monitors:read`, `logs:read`) | Закрытый ключ не раскрывается, короткий срок |
| Скачивание и распространение сертификатов | API-ключ + `certificates:key` + `deploy:execute` | Высокая чувствительность, нужны сетевые ограничения |

API-ключи не являются многопользовательским RBAC. Разделение полномочий между людьми реализуется в собственной платформе или шлюзе.

## Аутентификация
Ключ передаётся в заголовке `Authorization: Bearer` или `X-API-Key`:

```bash
curl -H "Authorization: Bearer xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" \
  https://ssl.example.com/api/certificates
```
```bash
curl -H "X-API-Key: xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" \
  https://ssl.example.com/api/certificates
```
- Интерфейсы всегда находятся по `/api`; устаревший `/<entry>/api` сохранён как совместимый псевдоним.
- Запросы требуют сеанса или API-ключа и не зависят от защищённой точки входа; при ключе Cookie и `X-LACTSSL-CSRF` не нужны.
- Используйте HTTPS и не передавайте ключ в параметрах URL. В ответах есть `X-Request-ID` для диагностики.

## Жизненный цикл API-ключа
### Создание
1. Войдите в консоль, откройте «API-ключи» → «Создать ключ» и укажите имя, срок и разрешения.
2. Полный ключ показывается один раз; после закрытия диалога доступна только ротация.

Ключ — 86-символьная строка Base64URL (64 байта энтропии) без префикса; сервер хранит только дайджест, поэтому восстановить открытый ключ из базы невозможно. Пример:

```bash
curl -X POST \
  -H "Authorization: Bearer xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "CI deployment",
    "scopes": ["tasks:read", "tasks:run", "deploy:execute"],
    "expires_at": "2027-01-01T00:00:00Z"
  }' \
  https://ssl.example.com/api/api-keys
```
Пустой `expires_at` означает бессрочный ключ; в продакшене так делать не рекомендуется. Создание и изменение ключей — операции высокого риска.

### Ротация и отключение
- После ротации старый токен сразу недействителен; после отключения или удаления вызовы возвращают `401`. При управлении другими ключами нельзя выдавать права, которых нет у самого ключа, и изменять ключ с более высокими правами.

### Хранение
- Храните ключ в менеджере паролей или системе секретов, не в репозитории, образах и журналах; выдавайте отдельный ключ каждой системе с указанием среды и ротируйте до истечения срока.

## Модель разрешений
Разрешения разделяются по схеме «ресурс:действие»; `*` обозначает все возможности API, включая чувствительные.

| Разрешение | Описание | Чувствительное |
| --- | --- | --- |
| `*` | Все возможности API системы | Да |
| `profile:read` | Просмотр учётной записи текущего ключа | Нет |
| `system:read` | Просмотр версии, состояния и метаданных | Нет |
| `dashboard:read` | Статистика панели управления | Нет |
| `certificates:read` | Сертификаты, цепочки и анализ | Нет |
| `certificates:write` | Очистка и удаление сертификатов | Нет |
| `certificates:key` | Просмотр и скачивание закрытых ключей | Да |
| `certificates:request` | CSR клиента: проверка и выпуск в публичном CA либо подпись частным CA (также нужен `private_cas:write`) | Да |
| `deploy:execute` | Ручное развёртывание, создание/изменение/запуск задач с узлами развёртывания (запись файлов; для команд нужен `deploy:command`) | Да |
| `deploy:command` | Выполнение reload-команды при локальном/SSH-развёртывании (произвольные команды от имени службы) | Да |
| `tasks:read` | Список задач и журналы задач | Нет |
| `tasks:write` | Создание, изменение, удаление, включение и отключение задач, очистка журналов (для сразу выполняемой задачи нужен `tasks:run`, при узлах — `deploy:execute`, для частного CA — `private_cas:write`) | Нет |
| `tasks:run` | Ручной или пакетный запуск (при узлах — `deploy:execute`, для частного CA — `private_cas:write`) | Нет |
| `monitors:read` | Мониторинг сертификатов сайтов | Нет |
| `monitors:write` | Создание, изменение, удаление и проверка мониторинга | Нет |
| `dns_accounts:read` | Учётные записи DNS (ключи маскируются) | Нет |
| `dns_accounts:write` | Создание, изменение и удаление учётных записей DNS | Да |
| `deploy_accounts:read` | Учётные записи целей (ключи маскируются) | Нет |
| `deploy_accounts:write` | Создание, изменение и удаление учётных записей целей | Да |
| `deployments:read` | История развёртывания | Нет |
| `ca:read` | ACME CA, конечные точки, учётные записи и заказы | Нет |
| `ca:write` | Изменение ACME CA, конечных точек и EAB, очистка диагностики | Да |
| `private_cas:read` | Сертификаты частного CA и цепочки | Нет |
| `private_cas:write` | Создание, импорт и удаление частных CA | Да |
| `private_cas:key` | Скачивание закрытых ключей частного CA | Да |
| `plugins:read` | Плагины, встроенные элементы и состояние включения | Нет |
| `plugins:write` | Загрузка, установка, обновление, удаление и включение плагинов | Да |
| `logs:read` | Просмотр и экспорт журналов операций, архивы | Нет |
| `logs:write` | Очистка журналов операций и удаление архивов (оставляет запись аудита) | Да |
| `system_logs:read` | Просмотр и экспорт журналов процессов, архивы | Нет |
| `system_logs:write` | Удаление системных журналов и архивов | Да |
| `settings:read` | Системные настройки и цепочка доверия внутреннего CA | Нет |
| `settings:write` | Общие настройки (SMTP/каналы; тестовые сообщения — только сеанс браузера) | Да |
| `backup:read` | Состояние и размер резервной копии | Нет |
| `backup:download` | Экспорт зашифрованной копии (только сеанс браузера; совместимость со старыми ключами) | Да |
| `backup:restore` | Загрузка и восстановление копии с перезапуском (только сеанс браузера; совместимость) | Да |
| `upgrade:read` | Состояние миграции базы и обновления программы | Нет |
| `upgrade:write` | Миграции, оптимизация базы и замена программы | Да |
| `security:write` | Доверенные корни и якоря плагинов (точка входа — только сеанс браузера) | Да |
| `api_keys:read` | Список API-ключей и разрешений | Нет |
| `api_keys:write` | Создание, изменение, ротация и удаление ключей | Да |

### Рекомендуемое сопоставление ролей
| Роль | Разрешения |
| --- | --- |
| Плановая проверка только для чтения | `system:read`, `dashboard:read`, `certificates:read`, `tasks:read`, `monitors:read`, `deployments:read` |
| Продление CI/CD | `tasks:read`, `tasks:run`; при узлах — `deploy:execute` |
| Распространение сертификатов | `certificates:read`, `certificates:key`, `deploy:execute` |
| Аудит и мониторинг | `logs:read`, `system_logs:read`, `system:read`, `monitors:read` |

Разрешения на ключи дополнительные: для ключа сертификата нужен `certificates:read`, для ключа частного CA — `private_cas:read`. Задачи с узлами требуют `deploy:execute`, `reload_command` — `deploy:command`, задачи частного CA — `private_cas:write`, а `enabled:true` — `tasks:run`.

## Формат ответа
```json
{
  "success": true,
  "data": {}
}
```
```json
{
  "success": false,
  "code": "api.error.api_key_scope_denied",
  "message": "У API-ключа отсутствует требуемое разрешение: certificates:read",
  "params": { "scope": "certificates:read" }
}
```
| Код | Значение |
| --- | --- |
| `400` | Ошибка параметров или проверки конфигурации |
| `401` | Нет аутентификации; ключ не существует, отключён, истёк или удалён |
| `403` | Недостаточно прав или интерфейс доступен только из сеанса браузера |
| `404` | Ресурс не найден или неверный путь |
| `409` | Конфликт состояний (уже выполняется, повторная операция) |
| `429` | Слишком частые запросы; учитывайте `Retry-After` |
| `500` | Ошибка сервера; запишите `X-Request-ID` |

## Основные интерфейсы
### Просмотр состояния и сертификатов
```bash
# Состояние процесса (требуется system:read)
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/status

# Список сертификатов (требуется certificates:read)
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/certificates
```
Списки возвращают массив, журналы — объект с постраничной разбивкой:

```bash
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  "https://ssl.example.com/api/logs?page=1&size=50&level=error&source=task&q=demo"
```
`size` — не более 200; `q` сопоставляется с исходным содержимым и значениями параметров. `DELETE /api/logs` требует `logs:write`; при включённом `log.archive.enabled` сначала создаётся архив, при сбое возвращается 500 и журналы не удаляются.

Архивы журналов:

```bash
# Список: поддерживаются page, size, category, reason, from, to, q
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  "https://ssl.example.com/api/log-archives?page=1&size=20&category=operation"

# Скачивание файла архива
curl -fsS -OJ -H "Authorization: Bearer $LACTSSL_TOKEN" \
  "https://ssl.example.com/api/log-archives/<archive_id>/download"

# Удаление архива
curl -fsS -X DELETE -H "Authorization: Bearer $LACTSSL_TOKEN" \
  "https://ssl.example.com/api/log-archives/<archive_id>"
```
Архивы принимают `logs:*` или `system_logs:*`: чтение для просмотра/скачивания, запись для удаления. `category` — `operation` или `runtime`; `reason` — `manual_clear`, `retention` или `max_rows`.

### Предварительный просмотр целевого потока
Целевой поток Beta читает сайты до сохранения задачи. Интерфейс только для чтения, требуется `deploy:execute`:

```bash
curl -fsS -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -H "Content-Type: application/json" \
  https://ssl.example.com/api/tasks/target-flow/preview \
  -d '{
    "sources": [{"deploy_account_id": 3, "deploy_config": {"target": "site"}}],
    "selection": {"mode": "all"},
    "policy": {"strategy": "smart", "max_domains_per_cert": 100},
    "public_ca": true
  }'
```
При создании задачи передайте `flow_mode: "target_beta"` и `target_flow`; стратегии — `smart`, `per_site`, `per_base_domain`, `single`, `per_source`, `custom`. Связи сертификатов записываются в `task_certificate_links`.

### Запуск задачи
```bash
curl -fsS -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/tasks/123/run
```
Если в задаче есть узлы развёртывания, нужен `deploy:execute`. По умолчанию повторно используется действительный сертификат вне окна продления и выполняются только узлы развёртывания; для принудительного выпуска используйте `?force=1` или `{"force":true}` (расходует квоту CA).

Журнал задачи (`tasks:read`):

```bash
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/tasks/123/logs
```
Очистка журнала (`tasks:write`):

```bash
curl -fsS -X DELETE -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/tasks/123/logs
```
Очистка удаляет журналы выпуска и развёртывания, возвращает `deleted` и оставляет запись аудита.

### Скачивание сертификатов
```bash
# Полная цепочка (требуется certificates:read)
curl -fL -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -o example.com.fullchain.pem \
  "https://ssl.example.com/api/certificates/42/download?type=fullchain"

# Закрытый ключ (требуется certificates:read + certificates:key)
curl -fL -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -o example.com.key \
  "https://ssl.example.com/api/certificates/42/download?type=key"
```
`type` — `fullchain` (по умолчанию), `cert` или `key`. Ответ — исходный PEM без JSON; сохраняйте его с ограниченными правами и не пишите в журналы.

### Мониторинг сайтов
```bash
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/monitors

curl -fsS -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/monitors/7/check
```
### Управление API-ключами
| Интерфейс | Метод | Разрешение | Описание |
| --- | --- | --- | --- |
| `/api-keys` | GET | `api_keys:read` | Список без открытых токенов |
| `/api-keys/scopes` | GET | `api_keys:read` | Разрешения и локализованные описания |
| `/api-keys` | POST | `api_keys:write` | Создание; `token` возвращается один раз |
| `/api-keys/:id` | PUT | `api_keys:write` | Имя, разрешения, состояние и срок |
| `/api-keys/:id/rotate` | POST | `api_keys:write` | Ротация; старый токен сразу недействителен |
| `/api-keys/:id` | DELETE | `api_keys:write` | Удаление |

### Использование клиентского CSR для запроса сертификата
Сервер принимает CSR и выпускает сертификат после проверки DNS-01 в публичном CA либо напрямую через частный CA. Закрытый ключ не создаётся и не сохраняется — он остаётся у клиента. Требуется `certificates:request`; для частного CA также `private_cas:write`:

```bash
curl -fsS -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "csr": "-----BEGIN CERTIFICATE REQUEST-----\n...\n-----END CERTIFICATE REQUEST-----\n",
    "ca": "letsencrypt",
    "challenge": "dns01",
    "dns_account_id": 3,
    "idempotency_key": "order-20261118-001",
    "wait_seconds": 0
  }' \
  https://ssl.example.com/api/certificate-requests
```
Для частного CA укажите `ca` как `private-ca:<id>` или `private_ca_id`; DNS-01, `dns_account_id` и контактный email не используются, внутренние домены разрешены:

```bash
curl -fsS -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "csr": "-----BEGIN CERTIFICATE REQUEST-----\n...\n-----END CERTIFICATE REQUEST-----\n",
    "private_ca_id": 2,
    "idempotency_key": "internal-20261121-001"
  }' \
  https://ssl.example.com/api/certificate-requests
```
Возвращается `202 Accepted` и ID запроса. Состояние опрашивается асинхронно:

```bash
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  https://ssl.example.com/api/certificate-requests/csr_xxxxxxxx
```
После `issued` можно скачать цепочку; закрытый ключ не предоставляется:

```bash
curl -fsS -H "Authorization: Bearer $LACTSSL_TOKEN" \
  "https://ssl.example.com/api/certificate-requests/csr_xxxxxxxx/certificate?type=fullchain" \
  -o example.com.fullchain.crt
```
- Публичный CA поддерживает только DNS-01; для wildcard он обязателен. Частный CA подписывает напрямую и применяет свои ExtKeyUsage и NameConstraints. Сервер проверяет подпись CSR, стойкость ключа, SAN и общее имя; внутренние домены отклоняются для публичного CA.
- `idempotency_key` рекомендуется всегда (один незавершённый запрос на CSR, CA и набор DNS-маршрутов); `csr.allowed_domains` ограничивает домены, для частного CA пустой список означает NameConstraints CA.
- `key_available=false` означает отсутствие ключа на сервере: скачивание ключа и зависящее от него развёртывание невозможны.

Клиентский скрипт `scripts/csr_client.py` генерирует ключ и CSR локально, отправляет запрос, опрашивает состояние и проверяет соответствие после скачивания:

```bash
export LACTSSL_URL="https://ssl.example.com"
export LACTSSL_TOKEN="xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"

python3 scripts/csr_client.py \
  --domains example.com,www.example.com \
  --dns-account-id 3 \
  --ca letsencrypt \
  --out-dir ./certs

# Запрос к приватному CA (закрытый ключ по-прежнему создаётся локально)
python3 scripts/csr_client.py \
  --domains app.internal,api.internal \
  --private-ca-id 2 \
  --out-dir ./certs
```
Также можно использовать интерактивный мастер и заполнять поля по порядку, не запоминая параметры (при запуске в терминале без параметров мастер открывается автоматически):

```bash
python3 scripts/csr_client.py --interactive
```
Продление, проверка состояния и пакетные операции:

```bash
# Продление: создать новый CSR с существующим ключом (ключ не копируется и не изменяется)
python3 scripts/csr_client.py \
  --key ./certs/example.com.key \
  --domains example.com,www.example.com \
  --out-dir ./certs \
  --force

# Или продлить напрямую по файлу состояния (ключ, CA, DNS-аккаунт и имя вывода переиспользуются)
python3 scripts/csr_client.py --renew-from ./certs/example.com.request.json

# Состояние / отмена (--json удобен для скриптов)
python3 scripts/csr_client.py --status csr_xxxxxxxx --json
python3 scripts/csr_client.py --cancel csr_xxxxxxxx --yes
python3 scripts/csr_client.py --list --json

# Пакетный режим: последовательно, по умолчанию остановка на первой ошибке
python3 scripts/csr_client.py --manifest ./certs.json --json
```
Формат пакетного манифеста:

```json
{
  "defaults": {"ca": "letsencrypt", "dns_account_id": 3, "out_dir": "./certs"},
  "certificates": [
    {"domains": ["example.com", "www.example.com"]},
    {"domains": ["api.example.com"], "name": "api", "key_type": "rsa2048"}
  ]
}
```
Скрипт загружает только CSR; результаты включают `.key`, `.csr`, `.crt`, `.fullchain.crt` и `.request.json`. Новые файлы сохраняются как `.new` и атомарно заменяют итоговые только после проверки; `.request.json` позволяет продолжить с `--request-id`. Временные ошибки повторяются (`--retries`, `--auto-retry`), HTTP 429 соблюдает `Retry-After`; токен можно передать через `LACTSSL_TOKEN_FILE`, язык — через `--lang` или `LACTSSL_LANG` (`zh-CN`, `zh-TW`, `en-US`, `ja-JP`, `ru-RU`). Коды выхода: 0 успех, 1 общая ошибка, 2 аргументы, 3 аутентификация, 4 сеть, 5 ошибка сервера, 6 локальная проверка, 130 отмена.

### Проверка доступности учётных данных
Проверка DNS- или целевых учётных данных использует `dns_accounts:write` / `deploy_accounts:write`; повторная аутентификация браузера не требуется, результат не блокирует сохранение. Без `domain` сохранённая DNS-учётная запись использует привязку домена; если её нет, возвращается `mode: "scope"` и проверяются только конфигурация и право вызова.

| Метод | Путь | Описание |
| --- | --- | --- |
| POST | `/api/dns-accounts/:id/verify` | Сохранённая DNS-учётная запись; можно передать `domain`, `mode` |
| POST | `/api/dns-accounts/verify` | Несохранённая конфигурация DNS; ключ передаётся явно |
| POST | `/api/deploy-accounts/:id/verify` | Сохранённые данные цели; можно передать `deploy_config` |
| POST | `/api/deploy-accounts/verify` | Несохранённая конфигурация цели; ключ передаётся явно |
| POST | `/api/deploy-accounts/:id/options` | Динамические параметры полей (например, сайты BaoTa) |
| POST | `/api/deploy-accounts/:id/match` | Сопоставление сайтов по доменам; возвращает `field`, `multiple`, `values` |

```bash
curl -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"domain":"example.com","mode":"auto"}' \
  https://ssl.example.com/api/dns-accounts/3/verify
```
Для `/options` нужен `deploy_accounts:write`; в теле передаются имя поля и параметры развёртывания, учётные данные сервер читает из цели:

```bash
curl -X POST \
  -H "Authorization: Bearer $LACTSSL_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"field":"sites","config":{"target":"docker"}}' \
  https://ssl.example.com/api/deploy-accounts/3/options
```
Ответ содержит `options`; при недоступном плагине или панели возвращается явная ошибка и остаётся ручной ввод. Проверки различают сбой аутентификации, непринадлежность домена, недоступность каталога для записи и другие причины:

```json
{
  "success": true,
  "data": {
    "ok": false,
    "mode": "write",
    "checks": [
      {"key": "init", "status": "ok"},
      {"key": "domain", "status": "ok", "message": "example.com"},
      {"key": "write", "status": "failed", "message": "..."}
    ],
    "message": "...",
    "verified_at": "2026-11-18T12:00:00Z"
  }
}
```
DNS-проверка по умолчанию использует `auto`: при поддержке `verify-v1` читает зоны и проверяет домен, иначе пишет случайный TXT в `_acme-challenge` и сразу удаляет его. `mode=read` включает только чтение; для сохранённых учётных записей используется только конфигурация из базы, для несохранённой ключ передаётся явно. Встроенные `local`/`ssh` проверяют подключение и временный файл без `reload_command`; остальные цели до реализации `verify` возвращают `unsupported`. `deploy_config` позволяет проверить область конкретного узла.

## Примеры CI/CD
### Распространение после продления сертификата
```bash
#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${LACTSSL_URL:?}"
TOKEN="${LACTSSL_TOKEN:?}"

# Запуск задачи (ID задачи передаётся из CMDB или системы управления конфигурацией)
curl -fsS -X POST \
  -H "Authorization: Bearer ${TOKEN}" \
  "${BASE_URL}/api/tasks/${TASK_ID}/run"

# После завершения задачи скачать по ID сертификата и распространить
curl -fL \
  -H "Authorization: Bearer ${TOKEN}" \
  -o /run/secrets/example.com.fullchain.pem \
  "${BASE_URL}/api/certificates/${CERT_ID}/download?type=fullchain"
```
- У API нет ключа идемпотентности: повторный `run` снова выполняет процесс, но действительный сертификат пропускается и выполняется только развёртывание; дедупликацию обеспечивает скрипт. Запуск асинхронен: для ожидания опрашивайте задачи или подпишитесь на уведомления.
- Для закрытого ключа нужны дополнительные права; обычно достаточно цепочки, а ключ узел развёртывания записывает напрямую в цель.

### Плановая проверка
```bash
#!/usr/bin/env bash
set -euo pipefail

curl -fsS -H "Authorization: Bearer ${LACTSSL_TOKEN}" \
  "${LACTSSL_URL}/api/status" >/dev/null

curl -fsS -H "Authorization: Bearer ${LACTSSL_TOKEN}" \
  "${LACTSSL_URL}/api/certificates" |
  jq -e '[.data[] | select(.not_after < (now + 14*86400 | todate))] | length == 0'
```
(Используется `jq`; фактические имена полей определяются ответом интерфейса.)

## Обработка ошибок и повторные попытки
- `429`: отступайте с учётом `Retry-After`, не повторяйте сразу.
- `5xx`: экспоненциальное отступление и `X-Request-ID`; `401` не повторяйте, проверьте срок, отключение и ротацию; `403` — разрешения и ограничения интерфейса.
- `400`/`409` требуют исправления параметров или состояния; выпуск и развёртывание имеют внешние эффекты — перед повтором проверьте состояние.

## Рекомендации по безопасности
- Отдельный ключ для каждой автоматизированной задачи; только необходимые разрешения, без `*` для чтения; разумный срок и регулярная ротация.
- Не раскрывайте ключ в журналах, скриншотах и репозиториях; при утечке немедленно отключите или удалите его, проверьте журналы и ограничьте источники вызовов на сети.
- Связывайте вызовы API с журналом операций, сохраняя вызывающую сторону, время и `X-Request-ID`.
