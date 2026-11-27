# Разработка расширений: исходный код и сборка

Статья для команд разработки и интеграции, которым нужна доработка, приватная сборка или участие в проекте. Эксплуатации она не требуется: о развёртывании см. [Установка и запуск](install.md), о плагинах — [Управление плагинами](plugins.md).

## Структура репозитория

```text
cmd/server/           точка входа программы, запуск и инициализация защищённой точки входа
internal/api/         маршруты Gin, обработчики, сеансы и ограничение частоты
internal/auth/        логика JWT и паролей/TOTP
internal/config/      загрузка и проверка config.yaml
internal/db/          открытие и миграции SQLite
internal/i18n/        серверная интернационализация и языковые ресурсы
internal/logging/     журнал процесса, уровни, формат, ротация файлов и асинхронная запись в базу данных
internal/model/       сущности базы данных и процесс задачи
internal/acme/        встроенный протокол сервера ACME
internal/service/     клиент ACME, развёртывание, уведомления, мониторинг, планировщик
internal/plugin/      хост плагинов (host/, runtime/, providers/, builtin/) и официальные реализации
plugins/             SDK для внешних плагинов: протокол v1, helper-фреймворк, общие утилиты
internal/store/       слой доступа к данным
internal/util/        шифрование, проверка и другие утилиты
internal/webui/       встроенные артефакты сборки внешнего интерфейса
web/                  внешний интерфейс Vue 3 + Element Plus
scripts/              скрипты разработки и CI (TypeScript, запускается напрямую через Node)
docs/                 документация на упрощённом китайском языке
docs/en/              документация на английском языке
docs/zh-hant/         документация на традиционном китайском языке
docs/ja/              документация на японском языке
docs/ru/              документация на русском языке
```

Официальные реализации DNS/развёртывания/уведомлений находятся в `internal/plugin/` и собираются вместе с основным модулем; `internal/plugin/providers` — отдельный Go module (`lactssl/providers`), подключаемый через локальный `replace`. `plugins/` — SDK для внешних плагинов; загруженные плагины выполняются как подпроцессы протокола v1. Основная программа не зависит от внешнего репозитория плагинов или подписанного индекса.

## Локальная разработка

### Окружение

- Go 1.27+; Node.js 22.12+ и npm (для Vite 8); необязательно Docker / Docker Compose v2.

### Основные команды

```bash
make dev          # запустить Vite + Go в режиме разработки в фоне
make dev-down     # остановить процессы разработки
make dev-restart  # перезапустить процессы разработки
make dev-status   # посмотреть PID / порты / состояние работоспособности
make dev-logs     # отслеживать журналы бэкенда и Vite
make build        # производственная сборка, результат bin/lactssl
make backend      # собрать только бэкенд, результат bin/lactssl
make server-dev   # бэкенд в режиме разработки, результат bin/lactssl-dev (без встроенного интерфейса)
make run          # напрямую go run ./cmd/server
make test         # go test ./...
make typecheck    # vue-tsc внешнего интерфейса + tsc для scripts
make ci           # полный локальный CI
make i18n-check   # проверить согласованность ключей перевода (по умолчанию включая китайский, английский и традиционный китайский)
make i18n-check-all # полная проверка всех языков (включая русский и японский)
make tidy         # go mod tidy + установка зависимостей внешнего интерфейса
make clean        # очистить артефакты сборки
make docs-build   # собрать документацию на китайском и английском (strict)
make docs-serve   # локальный предпросмотр двуязычной документации
```

### Порты режима разработки

`make dev` запускает Vite на `:5173` с HMR и Go на `:8080` (читает `server.addr`/`server.port`). Откройте `http://<host>:5173/<entry>/`: Vite проксирует `/api` и устаревший `/<entry>/api` в Go, а Go `8080` не проксирует на `5173` — в производственном режиме Vite отсутствует.

Скрипты режима разработки:

```bash
./scripts/dev.sh start            # запустить бэкенд Go + Vite в фоне
./scripts/dev.sh status           # PID / порты / состояние работоспособности
./scripts/dev.sh logs panel       # отслеживать журнал бэкенда (logs / logs web / logs all)
./scripts/dev.sh restart --build  # перекомпилировать bin/lactssl-dev и перезапустить
./scripts/dev.sh stop             # остановить
```

Файлы PID и журналы находятся в `data/run/`:

```text
data/run/panel.pid  data/run/panel.log
data/run/web.pid    data/run/web.log
```

Основные параметры:

| Параметр | Описание |
| --- | --- |
| `--no-web` | Запустить только бэкенд, без Vite |
| `--build` | Перед запуском/перезапуском выполнить `make server-dev` для перекомпиляции |
| `WEB_PORT` / `DEV_HOST` | Порт Vite (по умолчанию `5173`) и адрес прослушивания (по умолчанию `0.0.0.0`) |

Порт бэкенда и защищённая точка входа читаются из `config.yaml`; при отсутствии файла он создаётся из `config.yaml.example`.

## Сборка и внедрение версии

- Артефакты внешнего интерфейса выводятся в `web/dist`, копируются в `internal/webui/dist` и встраиваются тегом `embed`.
- Версия берётся только из git tag через `scripts/version.sh` (`git describe --tags --always --dirty` без `v`) и внедряется через `-ldflags -X 'lactssl/internal/api.Version=...'`; файл `VERSION` не ведётся. Время сборки и архитектура внедряются так же, без внедрения отображается `dev`.
- Версию можно переопределить:

```bash
LACTSSL_VERSION=1.2.3 make build
VERSION=1.2.3 make docker-context
docker build --build-arg VERSION=1.2.3 -t lactssl:1.2.3 .
```

## Тестирование и CI

```bash
make test
make typecheck
make i18n-check
make ci
```

`make ci` эквивалентен `.github/workflows/ci.yml` и может выполняться частями:

```bash
./scripts/ci.sh --backend-only
./scripts/ci.sh --frontend-only
./scripts/ci.sh --no-build
```

CI также запускает `gofmt`, `go vet`, тесты с `-race`, статистику покрытия, проверку типов и сборку внешнего интерфейса.

## Интернационализация

- Серверные ресурсы — в `internal/i18n/locales/`, ресурсы интерфейса — в `web/src/i18n/locales/`; при добавлении текста дополните `zh-CN`, `en-US` и `zh-TW`, затем запустите `make i18n-check`.
- Встроены `zh-TW`, `ru-RU` и `ja-JP`: `zh-TW` участвует в проверке и откатывается на `zh-CN`; русский и японский по умолчанию не проверяются и откатываются по цепочке `язык → en-US → zh-CN`. После синхронизации запускайте `make i18n-check-all` (или `I18N_CHECK_ALL=1`).
- Скрипт проверки — TypeScript (`scripts/check-i18n.ts`), запускается Node со встроенным удалением типов, нужен Node 22.18+; его проверка типов входит в `make typecheck` (`scripts/tsconfig.json`).

## Сборка документации

Документация использует Zensical/MkDocs:

```bash
make docs-build   # собрать все языки в режиме strict
make docs-serve   # предпросмотр в фоне (порт 8000, -l задаёт язык, остановка: scripts/docs-serve.sh stop)
```

Конфигурации, исходники и результат сборки находятся в `docsite/`: каталог конфигурации считается корнем проекта, поэтому `docs_dir` и `site_dir` должны быть внутри.

- `docsite/mkdocs.yml` — английский, вывод в `docsite/site`; `docsite/mkdocs.zh.yml` → `docsite/site/zh`, `mkdocs.zh-hant.yml` → `site/zh-hant`, `mkdocs.ja.yml` → `site/ja`, `mkdocs.ru.yml` → `site/ru`.
- `docs-internal/` — внутренние записи проектирования, не входят в сайт.
- Новая страница требует зеркала в `docsite/docs/<язык>/` и обновления nav во всех языках.

## Процесс выпуска

Релиз использует tag как единственный источник. Workflow `Release` собирает и загружает артефакты:

```bash
gh release create v1.0.1 --generate-notes
```

- `lactssl_<version>_<os>_<arch>.tar.xz` и `.tar.gz` для linux/darwin amd64/arm64, `lactssl_<version>_windows_amd64.zip`, `checksums.txt` (SHA-256). Архив содержит бинарник, `README.md`, `LICENSE` и `config.yaml.example`; `.tar.xz` меньше, `.tar.gz` совместим со старыми клиентами автообновления.

Образы `linux/amd64`, `linux/arm64` публикуются в GHCR:

```text
ghcr.io/axmishell/lactssl:<version>
ghcr.io/axmishell/lactssl:<major>.<minor>
ghcr.io/axmishell/lactssl:<major>
ghcr.io/axmishell/lactssl:latest
```

Предварительные версии публикуют только `<version>` и не обновляют `latest`, `<major>.<minor>` и `<major>`. После первого релиза пакет GHCR приватный — переключите его на Public.

- Workflow запускается только при официальном Release; `gh release create --draft` начнёт сборку после публикации.
- `.github/workflows/release.yml` должен присутствовать в коммите tag; файлы, загруженные в Release заранее, будут перезаписаны с `--clobber`.
- Пересборка: `gh workflow run release.yml -f tag=v1.0.1`. Подпись: после настройки `ED25519_PRIVATE_KEY_B64` создаётся отсоединённая подпись `checksums.txt`.

Локальный скрипт выпуска:

```bash
make release TAG=v1.0.1
./scripts/release.sh v1.0.1 --no-docker
./scripts/release.sh v1.0.1 --multiarch
./scripts/release.sh v1.1.0-rc.1 --docker-only
./scripts/release.sh v1.0.1 --publish-only
./scripts/release.sh v1.0.1 --upload
```

`--multiarch` требует Docker Buildx; QEMU не нужен — образ копирует кросс-компилированные бинарники и не выполняет команды целевой архитектуры.

## Совместная отладка плагинов

Официальные реализации находятся в `internal/plugin/`; `plugins/` — SDK для внешних плагинов, подключаемый через локальный `replace` в `go.mod`. После изменения официальной реализации собирайте из корня репозитория; загружаемый плагин остаётся одним исполняемым файлом helper, протокол описан в разделе [Разработка плагинов](plugin-development.md).

```bash
make backend
make plugins-test
```

## Рекомендации по участию

- Не коммитьте `config.yaml`, `data/`, резервные копии, журналы и реальные учётные данные.
- При изменении безопасности обновляйте [Базовую безопасность](security.md) и тесты, при изменении API/конфигурации/плагинов — документацию на китайском и английском; для новых интерфейсов добавляйте политику разрешений API-ключей и тесты, для миграций — идемпотентные шаги, копию и инструкции отката. Перед коммитом запускайте `make ci` и `make docs-build`.
