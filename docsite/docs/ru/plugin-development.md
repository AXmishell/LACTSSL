# Разработка расширений: протокол плагинов и руководство по участию

Статья для разработчиков плагинов, интеграторов и платформенных разработчиков: протокол, структура репозитория, разработка, сборка, упаковка и публикация; использование и управление описаны в [Управлении плагинами](plugins.md) и страниц DNS/развёртывания/уведомлений. Официальные реализации DNS/развёртывания/уведомлений находятся в `internal/plugin/` и компилируются в основную программу; `plugins/` — SDK для внешних плагинов; загруженные плагины работают как подпроцессы протокола v1. Основная программа не использует индекс или внешний репозиторий плагинов. Генераторы и инструменты находятся в `internal/plugin/`; внешним плагинам нужны только `plugins/protocol` и `plugins/sdk`.

## Архитектура и концептуальная модель

Плагин — не динамическая библиотека, а **одноразовый дочерний процесс**:

```text
хост LACTSSL
  └─ exec lactssl-dns-all-native <subcommand>
       ├─ manifest          вывести JSON самоописания плагина
       ├─ present / cleanup stdin JSON → записать/удалить TXT → выход
       ├─ verify            проверка только для чтения (необязательно)
       └─ код выхода 0 — успех; ненулевой — ошибка, ошибка записывается в stderr
```

| Helper | Тип | Подкоманды |
|---|---|---|
| `tools/helpers/dns-helper`, `dns-helper-v5` | DNS (v4/native, lego v5) | `manifest` / `present` / `cleanup` / `verify` |
| `tools/helpers/deploy-helper` | Развёртывание сертификатов | `manifest` / `deploy` / `options` / `plan` / `verify` |
| `tools/helpers/notify-helper` | Уведомления | `manifest` / `send` |

Кодовая база плагинов использует **модульный монолит**: каталог на provider, раздельные метаданные, тексты и значки, агрегаты генерируются `make manifest`; параллельная разработка снижает конфликты в одном файле.

## Протокол v1

Протокол остаётся `v1`; до `v2` он повышается только при несовместимых изменениях, новые возможности добавляются необязательными полями и флагами, а старый хост игнорирует нераспознанные поля.

### Подкоманды и вывод

```text
dns-plugin manifest
dns-plugin present   # stdin JSON
dns-plugin cleanup   # stdin JSON
dns-plugin verify    # stdin JSON
```

```text
deploy-plugin manifest
deploy-plugin deploy
deploy-plugin options
deploy-plugin plan
deploy-plugin verify
```

```text
notify-plugin manifest
notify-plugin send
```

- Код выхода `0` — успех, ненулевой — ошибка; stderr попадает в журнал основной программы.
- stdout DNS `present`/`cleanup` пуст; при успешном `deploy` stdout пуст, `verify`/`options`/`plan` выводят JSON; `send` выводит `{"ok":true,"message":""}`, при ошибке — `{"ok":false,...}` и ненулевой код.
- Запросы и ответы — UTF-8 JSON без экранирования не-ASCII символов.

### Переменные окружения

| Переменная | Описание |
|---|---|
| `LACTSSL_PLUGIN_PROTOCOL` | Версия протокола, сейчас `v1` |
| `LACTSSL_PLUGIN_MEMORY_LIMIT` | Бюджет постоянной памяти (в байтах), helper должен вызывать `debug.SetMemoryLimit` |
| `LACTSSL_DNS_PLUGIN_FEATURES` | Список возможностей DNS через запятую |
| `LACTSSL_DEPLOY_PLUGIN_FEATURES` | Список возможностей развёртывания через запятую |

| Возможность | Значение |
|---|---|
| `merged-deployers-v1` | Цели развёртывания выводят только объединённые канонические ID |
| `dynamic-options-v1` | Поддержка `options` и `options_source` / `multiple` |
| `verify-v1` | Проверка только для чтения `verify` |
| `plan-v1` | Предварительное сопоставление `plan` |

### Соглашения безопасности

- Закрытые ключи и token передаются только через stdin, не через argv или переменные окружения; основная программа передаёт минимальный набор переменных.
- helper должен быть исполняемым, запись в него не разрешена группе и другим пользователям, символические ссылки отклоняются; основная программа опрашивает `VmRSS` и при превышении `memory_limit_mb` завершает всю группу процессов; `manifest.sha256` может быть пустым, целостность при установке обеспечивают SHA-256 ресурса индекса и запись установки.

## Структура репозитория

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

Команда последовательно выполняет `catalog`, `i18n`, `deployimports`, `gen-v5`, затем генерирует `plugins.json` и группы. CI проверяет пропуски, а bot `Update generated files` записывает их обратно в PR того же репозитория.

## Разработка DNS provider

Официальные DNS provider используют lego v5; файлы-обёртки генерируются и не редактируются вручную:

```text
internal/plugin/providers/dns/lego5/providers/<id>/plugin.go
```

Чтобы добавить provider:

1. Убедитесь, что `github.com/go-acme/lego/v5` содержит его, и добавьте поля с переменными окружения в `catalog/dns/<id>.json`.
3. Добавьте `zh-CN.json` и `en-US.json` в `plugins/i18n/providers/dns/<id>/`.
4. Запустите `make gen-v5`, затем `make metadata-check`, `make manifest` и `make test-providers`.

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

Общие переводы полей — в `plugins/i18n/core/<locale>.json`; при отсутствии перевода используется язык по умолчанию, затем `label` / `value`.

## Разработка цели развёртывания

Расположение: `providers/deploy/<id>/plugin.go`

```go
type plugin struct{}

func (plugin) ID() string        { return "example" }
func (plugin) Aliases() []string { return nil }
func (plugin) Deploy(ctx context.Context, req deploy.NativeRequest) error { ... }

func init() { deploy.RegisterNative(plugin{}) }
```

| Интерфейс | Назначение | Объявление |
|---|---|---|
| `deploy.NativeVerifier` | Проверка учётных данных/цели только для чтения | `deploy.MarkNativeVerifiable("example")` |
| `deploy.NativeOptionLister` | Динамические параметры списка | поле catalog `options_source` |
| `deploy.NativePlanner` | Чтение сайтов/объектов перед пакетным развёртыванием | `deploy.MarkNativePlannable("example")` |

1. Создайте `providers/deploy/<id>/plugin.go` и `catalog/deploy/<id>.json` с `fields` и `deploy_fields`;
2. создайте `i18n/providers/deploy/<id>/zh-CN.json` и `en-US.json`, выполните `make manifest` для `tools/helpers/deploy-helper/imports_gen.go`;
3. добавьте модульные тесты `httptest`.

- Для URL, управляемых пользователем, используйте `outbound.ValidateURL` / `outbound.ValidateWebhookURL`, а для запросов — `outbound.NewClient`, не `http.DefaultClient`.
- `Verify` / `Plan` / `ListOptions` не должны записывать сертификаты, создавать постоянные объекты или выполнять reload; сообщения об ошибках не содержат token/secret. Исторические ID сопоставляются с каноническими целями через `deprecated_deploy_types` и `deploy_migrations` в `catalog/migrations.json`.

## Разработка канала уведомлений

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

| Функция | Назначение |
|---|---|
| `text` / `number` | Текст / число |
| `password` | Пароль/ключ |
| `selectField` + `opt` | Раскрывающийся список |
| `textarea` | Многострочный текст |

Каждый канал — отдельный файл, автоматически регистрируется через `init`, общий список менять не нужно. Отправка использует `notifyJSON` / `notifyForm` / `notifyHTTP` и `outbound`.

## Сборка

```bash
# все объединённые пакеты
make bundle

# отдельный вариант DNS
make bundle-lego
make bundle-native
make bundle-lego-v5
```

```bash
CGO_ENABLED=0 go build -trimpath \
  -tags "bundle_dns_all_native,nethttpomithttp2,http2legacy" \
  -ldflags "-s -w -X main.buildVersion=<version>" \
  -o lactssl-dns-all-native ./tools/helpers/dns-helper
```

- `-trimpath` удаляет локальные пути, `-s -w` — символы и DWARF; `bundle_dns_all_lego` / `bundle_dns_all_native` выбирают группу DNS, `nethttpomithttp2,http2legacy` обходят проблемы Go 1.27 `net/http` с `x/net/http2`.
- lego v5 использует `urfave_cli_no_template,ozzo_validation_no_template,grpcnotrace,validator_novalidatefn` для удаления кода шаблонов.

## Упаковка и индексация

```bash
make plugin KIND=dns ID=all-native GOOS=linux GOARCH=amd64
```

```text
dist/
├── lactssl-dns-all-native_<version>_linux_amd64.tar.gz
└── lactssl-dns-all-native_<version>_linux_amd64.meta.json
```

```text
plugin.json
lactssl-dns-all-native
LICENSE
icons/<provider>.<svg|png|webp>
```

`plugin.json` содержит версию, протокол, bundle, providers, variant, conflicts, sdk и ссылки на значки. Основная программа больше не использует подписанный индекс плагинов.

### Подпись

Релизы защищены `checksums.txt` и подписью Ed25519; для загруженных helper проверяются `sha256` из manifest и запись установки. Индекс плагинов генерировать и публиковать не нужно.

## Политика версий

- `all-lego` / `all-lego-v5` следуют версии lego в `go.mod`; патч `all-native`, `deploy/all`, `notify/all` растёт по отпечатку содержимого. Изменения `protocol/`, `sdk/`, `i18n/`, `outbound/`, `catalog/` и `go.mod` повышают версии всех связанных пакетов, изменения кода provider влияют только на его пакеты. Публикация выполняется по tag; без tag используется внутренняя версия.

## Процесс совместной работы

### Ветки и PR

```text
main
  ↑
короткая ветка (1–3 дня)
  ↑
PR → lint / generated / test / bundle / native → CODEOWNER review
  → merge queue → CI gate → слияние
  → после слияния bot обновляет генерируемые файлы
```

- Прямой push в `main` запрещён; обязательны PR и `CI gate` с Merge Queue. Один PR — один provider / цель / канал; генерируемые файлы записывает bot, вручную конфликты не разрешаются.

### CODEOWNERS

`.github/CODEOWNERS` определяет владельцев по умолчанию; после расширения команды замените их на GitHub Team по областям (DNS native/lego, deploy, notify, infra). `protocol` / `sdk` / `catalog schema` / CI требуют проверки основных сопровождающих.

### Многоуровневый CI

| Уровень | Триггер | Содержание |
|---|---|---|
| lint / generated | каждый PR / merge queue | gofmt, go.mod/go.sum, `make manifest` и проверка генерируемых файлов |
| test | изменения Go | `go vet ./...`, `go test ./...` |
| bundle | изменения DNS/deploy/notify | сборка 5 helper, согласованность manifest, smoke-тест упаковки |
| native / nightly | изменения DNS/SDK; ежедневно | `check_native.sh`; `go test -race`, полная сборка, govulncheck |

### Merge Queue

- Защитить `main`, Require status checks `CI gate`, Require linear history; включить Merge Queue (параллельность 5, тайм-аут 5 минут), запретить прямой push и включить «Require branches to be up to date before merging».

## Локальная отладка

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

## Требования к тестированию

- Модульные тесты используют `httptest` и не обращаются к реальной сети: DNS — записываемое значение, очистка только текущего, идемпотентность и отсутствие зоны; развёртывание — успех, сбой аутентификации, отсутствие записи в Verify/Plan; уведомления — конфигурация, тело и код ответа. Количество и поля manifest проверяются в CI; для native-пакета запускается `./scripts/check_native.sh`.

## Проверки перед коммитом

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
