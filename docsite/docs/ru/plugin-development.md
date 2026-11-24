# Разработка расширений: протокол плагинов и руководство по участию

Эта статья предназначена для разработчиков плагинов, интеграторов и команд корпоративных платформ и описывает протокол плагинов LACTSSL, структуру репозитория, процесс разработки, сборку, упаковку, индексацию, подпись и публикацию. **Специалистам по эксплуатации обычно достаточно прочитать [Управление плагинами](plugins.md) и соответствующие страницы плагинов DNS/развёртывания/уведомлений.**

Официальные реализации DNS/развёртывания/уведомлений находятся в `internal/plugin/` и компилируются в основную программу. `plugins/` — SDK для разработки внешних плагинов (протокол, helper-фреймворк и общие утилиты). Загруженные плагины по-прежнему используют подпроцессы протокола v1. Основная программа не использует индекс плагинов или внешний репозиторий плагинов.

Описанные ниже генераторы, упаковка бандлов и инструменты индекса находятся в `internal/plugin/`; команды выполняются из этого каталога. Внешним плагинам нужны только `plugins/protocol` и `plugins/sdk`.

## 1. Архитектура и концептуальная модель

Плагин — не динамическая библиотека, а **одноразовый дочерний процесс**:

```text
хост LACTSSL
  └─ exec lactssl-dns-all-native <subcommand>
       ├─ manifest          вывести JSON самоописания плагина
       ├─ present / cleanup stdin JSON → записать/удалить TXT → выход
       ├─ verify            проверка только для чтения (необязательно)
       └─ код выхода 0 — успех; ненулевой — ошибка, ошибка записывается в stderr
```

Четыре типа helper:

| Helper | Тип | Подкоманды |
|---|---|---|
| `tools/helpers/dns-helper` | DNS (v4 / native) | `manifest` / `present` / `cleanup` / `verify` |
| `tools/helpers/dns-helper-v5` | DNS (lego v5) | `manifest` / `present` / `cleanup` / `verify` |
| `tools/helpers/deploy-helper` | Развёртывание сертификатов | `manifest` / `deploy` / `options` / `plan` / `verify` |
| `tools/helpers/notify-helper` | Уведомления | `manifest` / `send` |

Репозиторий плагинов использует **модульный монолит**:

- один каталог на каждого provider;
- метаданные, тексты и значки разделены по provider;
- агрегированные файлы генерируются `make manifest`, ручное изменение запрещено;
- несколько участников могут разрабатывать параллельно, что уменьшает конфликты в одном файле.

## 2. Протокол v1

Протокол остаётся `v1`; до `v2` он повышается только при несовместимых изменениях. Новые возможности расширяются необязательными полями и функциональными флагами; старый хост игнорирует нераспознанные поля.

### 2.1 Подкоманды и вывод

DNS:

```text
dns-plugin manifest
dns-plugin present   # stdin JSON
dns-plugin cleanup   # stdin JSON
dns-plugin verify    # stdin JSON
```

Развёртывание:

```text
deploy-plugin manifest
deploy-plugin deploy
deploy-plugin options
deploy-plugin plan
deploy-plugin verify
```

Уведомления:

```text
notify-plugin manifest
notify-plugin send
```

Соглашения:

- Код выхода `0` — успех, отличный от `0` — ошибка; stderr попадает в журнал основной программы.
- stdout DNS `present` / `cleanup` должен быть пустым.
- При успешном `deploy` stdout пуст; `verify` / `options` / `plan` выводят JSON.
- При успешном `send` выводится одна строка `{"ok":true,"message":""}`; при ошибке также выводится `{"ok":false,...}` и процесс завершается с ненулевым кодом.
- Запросы и ответы используют UTF-8 JSON, не-ASCII символы не экранируются.

### 2.2 Переменные окружения

| Переменная | Описание |
|---|---|
| `LACTSSL_PLUGIN_PROTOCOL` | Версия протокола, сейчас `v1` |
| `LACTSSL_PLUGIN_MEMORY_LIMIT` | Бюджет постоянной памяти (в байтах), helper должен вызывать `debug.SetMemoryLimit` |
| `LACTSSL_DNS_PLUGIN_FEATURES` | Список возможностей DNS через запятую |
| `LACTSSL_DEPLOY_PLUGIN_FEATURES` | Список возможностей развёртывания через запятую |

Текущие возможности:

| Возможность | Значение |
|---|---|
| `merged-deployers-v1` | Цели развёртывания выводят только объединённые канонические ID |
| `dynamic-options-v1` | Поддержка подкоманды `options` и `options_source` / `multiple` |
| `verify-v1` | Поддержка проверки только для чтения `verify` |
| `plan-v1` | Поддержка предварительного сопоставления `plan` |

### 2.3 Соглашения безопасности

- Закрытые ключи и token передаются только через stdin, не записываются в argv или переменные окружения;
- основная программа передаёт только минимальный набор переменных окружения;
- helper должен быть исполняемым, и запись в него не должна быть разрешена группе или другим пользователям; символические ссылки отклоняются;
- основная программа опрашивает `VmRSS` и при превышении `memory_limit_mb` завершает всю группу процессов;
- `manifest.sha256` может быть пустым; целостность при установке из репозитория обеспечивается SHA-256 ресурса индекса и записью установки.

## 3. Структура репозитория

```text
plugins/                     внешний SDK плагинов (отдельный module)
  protocol/                  формат протокола v1
  sdk/                       общие HTTP, подпись, DNS, helper-фреймворк
  outbound/                  исходящий HTTP-клиент с защитой от SSRF
  i18n/                      общие переводы для плагинов
internal/plugin/
  host/
    dns/                     DNS-адаптер хоста (интерфейсы, реестр, обнаружение)
    deploy/                  адаптер развёртывания
    notify/                  адаптер уведомлений
  providers/                 отдельный Go module (lactssl/providers)
    go.mod
    api/                     Native-интерфейсы и реестры (dns/deploy/notify)
    catalog/                 метаданные provider и целей развёртывания
      dns/<id>.json          метаданные DNS provider (вручную)
      deploy/<id>.json       метаданные цели развёртывания (вручную)
      data.json              генерируется: агрегированные метаданные
    dns/lego5/               обёртки lego v5, группы, метаданные (генерируется)
    deploy/<id>/             цели развёртывания (вручную)
    notify/channels/         по одному channel_<id>.go на канал
  runtime/
    registry/                общий реестр ID/алиасов/источников
    discovery/               обнаружение плагинов и общие утилиты
    exec/                    одноразовый запуск подпроцесса
    sandbox/                 песочница загруженных плагинов
    install/                 атомарная установка/откат
    metadata/                кэш снимков manifest
    icon/                    MIME/data URI иконок
  builtin/                   адаптеры официальных реализаций к реестрам хоста
  assets/icons/              иконки
  tools/helpers/             отдельные helper / signer (совместимость/релиз)
  scripts/                   генераторы и инструменты сборки
  plugins.json               генерируется: манифест бандла
```

Генерируемые файлы:

```text
providers/catalog/data.json
plugins/i18n/locales/*.json
tools/helpers/deploy-helper/imports_gen.go
providers/dns/lego5/**
providers/dns/lego5/groups/**
plugins.json
```

После изменения исходных файлов выполните:

```bash
make manifest
```

Эта команда последовательно выполняет `catalog`, `i18n`, `deployimports`, `gen-v5`, затем генерирует `plugins.json` и группы. CI проверяет, не пропущены ли генерируемые файлы; в PR того же репозитория bot `Update generated files` автоматически записывает их обратно.

## 4. Разработка DNS provider

Официальные DNS provider используют lego v5. Файлы-обёртки генерируются:

```text
internal/plugin/providers/dns/lego5/providers/<id>/plugin.go
```

Не редактируйте сгенерированные файлы вручную. Чтобы добавить или обновить provider:

1. Убедитесь, что `github.com/go-acme/lego/v5` содержит нужный provider.
2. Добавьте поля и соответствие переменных окружения в `catalog/dns/<id>.json`.
3. Добавьте `zh-CN.json` и `en-US.json` в `plugins/i18n/providers/dns/<id>/`.
4. Запустите `make gen-v5`, чтобы сгенерировать обёртки, группы и метаданные.
5. Запустите `make metadata-check`, `make manifest` и `make test-providers`.

Пример `catalog/dns/<id>.json`:

```json
{
  "value": "example",
  "label_key": "meta.dns.example.label",
  "desc_key": "meta.dns.example.desc",
  "fields": [
    {
      "key": "token",
      "label_key": "meta.field.token.label",
      "type": "password",
      "required": true,
      "env": "EXAMPLE_TOKEN"
    }
  ]
}
```

`env` записывается helper в переменную окружения, которую читает lego v5. Переводы находятся в:

```text
plugins/i18n/providers/dns/<id>/zh-CN.json
plugins/i18n/providers/dns/<id>/en-US.json
```

Общие переводы полей находятся в `plugins/i18n/core/<locale>.json`. При отсутствии перевода используется язык по умолчанию, затем `label` / `value`.

## 5. Разработка цели развёртывания

Расположение: `providers/deploy/<id>/plugin.go`

```go
type plugin struct{}

func (plugin) ID() string        { return "example" }
func (plugin) Aliases() []string { return nil }
func (plugin) Deploy(ctx context.Context, req deploy.NativeRequest) error { ... }

func init() { deploy.RegisterNative(plugin{}) }
```

Необязательные возможности:

| Интерфейс | Назначение | Объявление |
|---|---|---|
| `deploy.NativeVerifier` | Проверка учётных данных/цели только для чтения | `deploy.MarkNativeVerifiable("example")` |
| `deploy.NativeOptionLister` | Динамические параметры раскрывающегося списка | поле catalog `options_source` |
| `deploy.NativePlanner` | Чтение сайтов/объектов перед пакетным развёртыванием | `deploy.MarkNativePlannable("example")` |

Шаги разработки:

1. Создайте `providers/deploy/<id>/plugin.go`;
2. создайте `catalog/deploy/<id>.json` с `fields` и `deploy_fields`;
3. создайте `i18n/providers/deploy/<id>/zh-CN.json` и `en-US.json`;
4. выполните `make manifest`, чтобы сгенерировать `tools/helpers/deploy-helper/imports_gen.go`;
5. добавьте модульные тесты `httptest`.

Соглашения безопасности:

- Для URL, управляемых пользователем, необходимо использовать `outbound.ValidateURL` / `outbound.ValidateWebhookURL`;
- исходящие запросы используют `outbound.NewClient`, не используйте напрямую `http.DefaultClient`;
- `Verify` / `Plan` / `ListOptions` не должны записывать сертификаты, создавать постоянные объекты или выполнять reload;
- сообщения об ошибках не содержат token/secret.

Исторические ID сопоставляются с каноническими целями через `deprecated_deploy_types` и `deploy_migrations` в `catalog/migrations.json`.

## 6. Разработка канала уведомлений

Расположение: `providers/notify/channels/channel_<id>.go`

```go
package channels

func init() {
    registerChannel(newChannel("example", []notify.NativeField{
        text("url", "notifications.fieldServerUrl", true),
        password("token", "notifications.fieldToken", true),
    }, sendExample))
}

func sendExample(cfg map[string]any, msg notify.NativeMessage) error { ... }
```

Конструкторы полей:

| Функция | Назначение |
|---|---|
| `text` | Текст |
| `password` | Пароль/ключ |
| `number` | Число |
| `selectField` + `opt` | Раскрывающийся список |
| `textarea` | Многострочный текст |

Каждый канал — отдельный файл, автоматически регистрируется через `init`, изменять общий список не нужно. Логика отправки предпочтительно использует `notifyJSON` / `notifyForm` / `notifyHTTP` и `outbound`.

## 7. Сборка

```bash
# все объединённые пакеты
make bundle

# отдельный вариант DNS
make bundle-lego
make bundle-native
make bundle-lego-v5
```

Параметры сборки:

```bash
CGO_ENABLED=0 go build -trimpath \
  -tags "bundle_dns_all_native,nethttpomithttp2,http2legacy" \
  -ldflags "-s -w -X main.buildVersion=<version>" \
  -o lactssl-dns-all-native ./tools/helpers/dns-helper
```

- `-trimpath` удаляет локальные пути;
- `-s -w` удаляет символы и DWARF;
- `bundle_dns_all_lego` / `bundle_dns_all_native` выбирают группу DNS;
- `nethttpomithttp2,http2legacy` обходят проблемы совместимости Go 1.27 `net/http` с новой версией `x/net/http2`;
- lego v5 дополнительно использует `urfave_cli_no_template,ozzo_validation_no_template,grpcnotrace,validator_novalidatefn` для удаления кода шаблонов.

## 8. Упаковка и индексация

Сборка одного пакета:

```bash
make plugin KIND=dns ID=all-native GOOS=linux GOARCH=amd64
```

Артефакты:

```text
dist/
├── lactssl-dns-all-native_<version>_linux_amd64.tar.gz
└── lactssl-dns-all-native_<version>_linux_amd64.meta.json
```

Содержимое пакета:

```text
plugin.json
lactssl-dns-all-native
LICENSE
icons/<provider>.<svg|png|webp>
```

`plugin.json` содержит версию, протокол, bundle, providers, variant, conflicts, sdk и ссылки на значки. Индекс:

```bash
make index BASE_URL=https://github.com/AXmishell/LACTSSL-Plugins/releases/download
```

Поля индекса:

- `schema` / `channel` / `protocol` / `generated_at`;
- `plugins[]`: kind, id, version, bundle, providers, variant_of, conflicts_with, sdk, assets;
- `assets[GOOS/GOARCH]`: url, sha256, size, uncompressed_size;
- корневые `icons[]`: встроенные значки base64 для отображения до установки.

### 8.1 Подпись

```bash
go run scripts/pluginctl/main.go sign -key key.b64 -in index.json -out index.json.sig
```

`key.b64` — закрытый ключ Ed25519 в кодировке base64 (32-байтовое семя или 64-байтовый закрытый ключ). В производственном выпуске следует обязательно настроить `PLUGIN_INDEX_SIGNING_KEY`; при его отсутствии не следует публиковать неподписанный индекс.

## 9. Политика версий

- Версии `all-lego` / `all-lego-v5` следуют версии lego в `go.mod`;
- номер патча `all-native`, `deploy/all`, `notify/all` автоматически увеличивается на основе отпечатка содержимого;
- изменения общих входных данных (`protocol/`, `sdk/`, `i18n/`, `outbound/`, `catalog/`, `go.mod`) вызывают повышение версии всех связанных пакетов, чтобы избежать «одна версия — разное содержимое»;
- изменения исходного кода самого provider влияют только на пакеты соответствующего типа;
- публикация выполняется по tag; при ручной публикации без tag используется внутренняя версия, необходимо учитывать её соотношение с уже опубликованными версиями tag.

## 10. Процесс совместной работы

### 10.1 Ветки и PR

```text
main
  ↑
короткая ветка (1–3 дня)
  ↑
PR → lint / generated / test / bundle / native → CODEOWNER review
  → merge queue → CI gate → слияние
  → после слияния bot обновляет генерируемые файлы
```

Требования:

- запрещено напрямую push в `main`;
- обязателен PR и прохождение `CI gate`;
- используйте Merge Queue;
- один PR на один provider / одну цель / один канал;
- генерируемые файлы записывает bot, участники не должны вручную разрешать конфликты в генерируемых файлах.

### 10.2 CODEOWNERS

`.github/CODEOWNERS` репозитория плагинов определяет владельцев по умолчанию. После расширения команды замените их на GitHub Team по областям:

- DNS native, lego, deploy, notify, infra;
- protocol / sdk / catalog schema / CI требуют проверки основными сопровождающими.

### 10.3 Многоуровневый CI

| Уровень | Триггер | Содержание |
|---|---|---|
| lint | каждый PR / merge queue | gofmt, go.mod/go.sum |
| generated | каждый PR / merge queue | `make manifest` + проверка генерируемых файлов |
| test | изменения Go | `go vet ./...`, `go test ./...` |
| bundle | изменения DNS/deploy/notify | сборка 5 helper, количество/согласованность manifest, smoke-тест упаковки |
| native | изменения DNS/SDK | `check_native.sh` |
| nightly | ежедневно | `go test -race`, полная сборка, govulncheck |

Для защиты ветки достаточно требовать одну проверку **CI gate**; если условное задание пропущено, gate всё равно проходит.

### 10.4 Merge Queue

Настройки репозитория:

- защитить `main`;
- Require status checks: `CI gate`;
- Require linear history;
- включить Merge Queue (максимальная параллельность 5, тайм-аут ожидания 5 минут);
- запретить прямой push;
- включить «Require branches to be up to date before merging».

## 11. Локальная отладка

```bash
# генерация
make manifest

# native DNS manifest
go run -tags bundle_dns_all_native ./tools/helpers/dns-helper manifest | jq '.providers | length'

# посмотреть один provider
go run -tags bundle_dns_all_native ./tools/helpers/dns-helper manifest \
  | jq '.providers[] | select(.id=="linode")'

# развёртывание
LACTSSL_DEPLOY_PLUGIN_FEATURES=merged-deployers-v1,verify-v1,plan-v1,dynamic-options-v1 \
  go run ./tools/helpers/deploy-helper manifest | jq

# уведомления
go run ./tools/helpers/notify-helper manifest | jq '.channels | length'
```

Пример запроса:

```bash
echo '{
  "action": "present",
  "provider": "linode",
  "config": {"token": "..."},
  "domain": "example.com",
  "token": "acme-token",
  "key_auth": "acme-token.thumbprint"
}' | go run -tags bundle_dns_all_native ./tools/helpers/dns-helper present
```

## 12. Требования к тестированию

- Модульные тесты используют `httptest` и не обращаются к реальной сети;
- покрытие DNS: записываемое значение, очистка удаляет только текущее значение, идемпотентность при отсутствии записи, отсутствие подходящей зоны;
- покрытие развёртывания: успех, сбой аутентификации, отсутствие записи в Verify/Plan;
- покрытие уведомлений: отсутствие конфигурации, тело запроса, ошибочный код состояния;
- количество и поля manifest проверяются в CI;
- для native-пакета дополнительно запускается `./scripts/check_native.sh`.

## 13. Проверки перед коммитом

```text
[ ] изменены только свой каталог provider + соответствующие catalog/i18n
[ ] регистрация в init, ID/alias уникальны
[ ] ключи полей catalog совпадают с кодом
[ ] тексты на китайском и английском дополнены
[ ] модульные тесты используют httptest
[ ] make manifest
[ ] gofmt / go vet / go test
[ ] check_native (native)
[ ] сгенерированные файлы закоммичены или ожидают записи bot
```

## 14. Связанная документация

- [Управление плагинами](plugins.md)
- [Плагины DNS](dns-plugins.md)
- [Плагины развёртывания](deploy-plugins.md)
- [Плагины уведомлений](notify-plugins.md)
- [Руководство по участию в репозитории плагинов](https://github.com/AXmishell/LACTSSL-Plugins/blob/main/CONTRIBUTING.md)
