# Установка и запуск
Готовые к продакшену шаги установки. При первом развёртывании сначала прочитайте [Быстрый старт](getting-started.md); можно сразу перейти к Docker Compose.

## Требования к среде
| Пункт | Требование |
| --- | --- |
| Операционная система | Linux amd64 / arm64; также поддерживаются пакеты для macOS и Windows |
| Контейнеры | Docker и Docker Compose v2 (рекомендуется) |
| Сборка из исходного кода | Go 1.27+, Node.js 22.12+, npm |
| Ресурсы хоста | См. [Архитектура развёртывания](architecture.md); для начала 2 vCPU / 1 GB, для личного сценария с одним доменом достаточно 1 vCPU / 1 GB |
| Сеть | Точка входа управления через HTTPS-обратный прокси; исходящий доступ к DNS API, CA, SMTP и целям развёртывания |
| Домены | Один домен управления и TLS-сертификат; для сервера ACME опционально отдельный домен |

Внешняя база данных, Redis и очередь сообщений не нужны: всё состояние находится в `data/`.

!!! tip "Быстрое развёртывание"
    Запустите Docker Compose, завершите инициализацию и включите встроенные плагины. Для панелей BaoTa, 1Panel и подобных включите нужную цель развёртывания; для автопродления нужен DNS API, поэтому используйте отдельную субучётную запись. Обновление программы и загрузка helper вызывают кратковременный пик памяти — не ограничивайте память контейнера объёмом простоя.

## Вариант 1: Docker Compose (рекомендуется)
### Подготовка каталогов
```bash
git clone https://github.com/AXmishell/LACTSSL.git
cd LACTSSL
mkdir -p config data
cp config.yaml.example config/config.yaml
chmod 600 config/config.yaml
chmod 700 data
```
`docker-compose.yml` по умолчанию использует образ GHCR:
```yaml title="docker-compose.yml"
--8<-- "docker-compose.yml"
```
По умолчанию используется внешняя сеть `1panel-network` от 1Panel. Если 1Panel нет, удалите `networks` в сервисе и блок `networks` в конце файла либо создайте сеть:
```bash
docker network create 1panel-network
```
### Запуск
```bash
docker compose pull
docker compose up -d
docker compose logs -f lactssl
```
!!! note "Образ и сеть"
    Образ GHCR доступен с первого официального Release; если `docker compose pull` сообщает, что образ не существует, соберите его локально, как описано ниже. По умолчанию используется `latest`, следующий за последним стабильным выпуском. Compose по умолчанию публикует 8080 только на `127.0.0.1`; для доступа из локальной сети явно измените `ports` и поставьте перед сервисом TLS-обратный прокси.

Монтирования по умолчанию: `./config/config.yaml` — конфигурация; `./data` — SQLite, плагины, журналы и резервные копии обновления. Контейнер всегда читает `/app/config/config.yaml` и `/app/data`; входной скрипт от root исправляет владельца каталогов, затем понижает права до пользователя `lactssl`. Compose включает корневую файловую систему только для чтения, `no-new-privileges`, белый список capabilities и `pids_limit`.

### Фиксация версии
По умолчанию используется `latest`; для продакшена зафиксируйте версию в `docker-compose.yml`:
```yaml
image: ghcr.io/axmishell/lactssl:1.2.3
```
Стабильные теги: `<version>`, `<major>.<minor>`, `<major>` и `latest`; для предварительных версий публикуется только полный тег версии.

### Локальная сборка образа
Dockerfile только копирует Linux-бинарник, собранный вне Docker; резервное копирование и восстановление баз данных реализованы нативно на Go, поэтому `pg_dump`/`mysqldump` не требуются. Подготовьте фронтенд и бинарник, затем соберите образ:
```bash
VERSION=1.2.3 make docker-context
docker build --build-arg VERSION=1.2.3 -t lactssl:1.2.3 .
docker run -d --name lactssl \
  -p 127.0.0.1:8080:8080 \
  -v "$PWD/data:/app/data" \
  -v "$PWD/config:/app/config" \
  --restart unless-stopped \
  lactssl:1.2.3
```
## Вариант 2: бинарный файл + systemd
### Установка
```bash
sudo useradd --system --home /opt/lactssl --shell /usr/sbin/nologin lactssl
sudo mkdir -p /opt/lactssl/data
sudo cp lactssl /opt/lactssl/
sudo cp config.yaml.example /opt/lactssl/config.yaml
sudo chown -R lactssl:lactssl /opt/lactssl
sudo chmod 600 /opt/lactssl/config.yaml
sudo chmod 700 /opt/lactssl/data
```
!!! note "Нативное резервное копирование"
    Логическое резервное копирование и восстановление PostgreSQL/MySQL реализованы в Go-коде LACTSSL; клиенты `pg_dump`, `pg_restore`, `mysqldump` и `mysql` больше не требуются. SQLite по-прежнему использует снимок `VACUUM INTO`.

### Модуль systemd
```ini title="/etc/systemd/system/lactssl.service"
[Unit]
Description=LACTSSL certificate manager
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=lactssl
Group=lactssl
WorkingDirectory=/opt/lactssl
ExecStart=/opt/lactssl/lactssl -config /opt/lactssl/config.yaml
Restart=on-failure
RestartSec=5
LimitNOFILE=65535
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/opt/lactssl/data

[Install]
WantedBy=multi-user.target
```
```bash
sudo systemctl daemon-reload
sudo systemctl enable --now lactssl
sudo journalctl -u lactssl -f
```
Если журналы пишутся в `/opt/lactssl/data/logs/`, `ReadWritePaths` уже это покрывает; для другого каталога разрешите запись дополнительно. Для портов 80/443 используйте обратный прокси, а не повышение прав бинарного файла.

### Скрипт установки одной командой
`scripts/install.sh` загружает бинарный файл Linux amd64/arm64 из GitHub Release, создаёт пользователя и каталог данных, устанавливает службу systemd и генерирует конфигурацию обратного прокси Nginx. По умолчанию TLS не включается: бинарный файл слушает `127.0.0.1:8080`, Nginx слушает 80 и перенаправляет на этот порт.
```bash
sudo ./scripts/install.sh --port 8080 --domain ssl.example.com
```
Скрипт можно выполнить без клонирования репозитория; в архивах релизов Linux также прилагается `install.sh`:
```bash
curl -fsSL https://raw.githubusercontent.com/AXmishell/LACTSSL/main/scripts/install.sh \
  | sudo bash -s -- --port 8080 --domain ssl.example.com
```
Общие параметры:

| Параметр | Описание |
| --- | --- |
| `--port PORT` | Порт бинарного файла, по умолчанию `8080`, должен быть >= 1024 |
| `--domain DOMAIN` | `server_name` Nginx, например `ssl.example.com`; по умолчанию `_` |
| `--version TAG` | Версия, например `v1.2.3`; по умолчанию последняя стабильная |
| `--no-nginx` / `--no-tls` | Только systemd без Nginx / отключить существующий TLS в Nginx |
| `--tls-cert` / `--tls-key` | Включить HTTPS в Nginx и перенаправление 80 → 443 |
| `--read-write-path DIR` | Дополнительный каталог для записи, можно повторять (для локального развёртывания) |
| `--skip-checksum` | Пропустить проверку SHA-256 (не рекомендуется) |

Пример включения HTTPS:
```bash
sudo ./scripts/install.sh \
  --domain ssl.example.com \
  --port 8080 \
  --tls-cert /etc/nginx/certs/ssl.example.com.pem \
  --tls-key  /etc/nginx/certs/ssl.example.com.key
```
Скрипт проверяет `checksums.txt` в Release. При повторном запуске заменяется только бинарный файл и перезапускается служба, существующие `config.yaml` и `data/` сохраняются; `--port` синхронно обновляет `server.addr`, `server.host`, `server.port`. Без `--domain`/`--tls-cert`/`--tls-key` используются домен и сертификат из существующей конфигурации Nginx, чтобы не вернуться к сайту по умолчанию и не отключить TLS; для отключения TLS явно укажите `--no-tls`.

Если Nginx не установлен, скрипт попытается установить его через системный менеджер пакетов (`--no-install-deps` отключает это, `--no-nginx` пропускает настройку Nginx). В службе включён `ProtectSystem=strict`, поэтому самообновление со страницы программы может быть недоступно; для обновления повторно запустите скрипт. Для записи в локальные каталоги вроде `/etc/nginx` несколько раз укажите `--read-write-path` и проверьте права пользователя `lactssl`.

Конфигурация по умолчанию:
```yaml
server:
  host: "127.0.0.1"
  port: 8080
  trusted_proxies: ["127.0.0.1", "::1"]
security:
  public_url: "http://ssl.example.com"
```
Nginx пишется в `/etc/nginx/conf.d/lactssl.conf` с `client_max_body_size 256m` и тайм-аутами чтения/записи 600 секунд. После развёртывания завершите инициализацию через защищённый вход из журнала запуска.

### Сборка из исходного кода
```bash
git clone https://github.com/AXmishell/LACTSSL.git
cd LACTSSL
cp config.yaml.example config.yaml && chmod 600 config.yaml
cd web && npm install && cd ..
make build
./bin/lactssl
```
`make build` собирает фронтенд, копирует его в `internal/webui/dist` и компилирует `bin/lactssl` со встроенными ресурсами. `make backend` собирает только бэкенд, но артефакты фронтенда нужно подготовить самостоятельно.

## Первичная инициализация
Журнал запуска выводит защищённую точку входа и одноразовый токен:
```text
Защищённая точка входа: http://127.0.0.1:8080/K7mQ2pX9vR4tY8nL6cWz/
Токен инициализации (действителен только для этого запуска): ...
```
1. Откройте страницу через защищённую точку входа.
2. Создайте администратора по подсказкам.
3. При восстановлении импортируйте резервную копию на `/install` и укажите токен.
4. После инициализации токен сразу становится недействительным.

До инициализации на `/install` можно изменить поля из белого списка: адрес и доверенные прокси, `public_url` и сеансы, ACME (прослушиватель, внешний адрес, TLS/mTLS), журналы, обновление, переключатели плагинов и ограничения ресурсов. После сохранения перезапустите службу.

Конфиденциальные поля (`jwt_secret`, `salt`, точка входа, каталог данных, файлы БД и журналов, каталог плагинов, репозиторий программы и публичный ключ подписи) на странице не отображаются — редактируйте `config.yaml` на сервере. Точку входа можно сменить в «Настройки → Защищённая точка входа».

> Защищённая точка входа не является границей безопасности. В продакшене обязательны HTTPS, надёжный пароль, TOTP/ключи доступа и сетевой контроль доступа.

## Обратный прокси и HTTPS
Основная программа слушает только HTTP; TLS завершается на Nginx, Caddy, Traefik, балансировщике или WAF.

### Обязательная конфигурация
```yaml
server:
  host: "127.0.0.1"
  port: 8080
  trusted_proxies: ["127.0.0.1"]

security:
  public_url: "https://ssl.example.com"
```
- `trusted_proxies` должен содержать точный IP прокси или минимальный CIDR, иначе IP клиента, ограничение частоты и `Secure` Cookie определяются неверно.
- `public_url` используется для ссылок сброса пароля; нельзя полагаться на Host запроса.
- При завершении TLS на прокси обеспечьте передачу `X-Forwarded-Proto: https`.

### Пример Nginx
```nginx
server {
    listen 443 ssl;
    server_name ssl.example.com;

    ssl_certificate     /etc/nginx/certs/ssl.example.com.pem;
    ssl_certificate_key /etc/nginx/certs/ssl.example.com.key;
    ssl_protocols       TLSv1.2 TLSv1.3;

    client_max_body_size 256m;  # восстановление резервных копий и загрузка плагинов

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        # Режим перезаписи: отбрасываем XFF клиента и оставляем только реальный адрес
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
    }
}
```
Это рекомендуемый **режим перезаписи**: LACTSSL доверяет только этому Nginx. В режиме добавления (`$proxy_add_x_forwarded_for`) при нескольких прокси в `trusted_proxies` нужно включить все прокси, которые могут появиться в XFF. Различия для CDN, Docker/Kubernetes и L4-балансировщиков — в разделе о обратном прокси в [Конфигурации](configuration.md).

Дополнительно настройте белый список исходных IP, VPN, нулевое доверие или WAF. Журнал доступа прокси может служить независимым источником аудита, но не записывайте в него Cookie и `Authorization`.

### Сервер ACME (опционально)
```yaml
acme:
  enabled: true
  addr: ":14000"
  base_url: "https://pki.example.com:14000"
  tls:
    cert: "/etc/lactssl/acme.crt"
    key: "/etc/lactssl/acme.key"
```
- Пустой `addr` монтирует ACME под `/acme` основного порта; в продакшене рекомендуется отдельный порт.
- `base_url` должен быть адресом, по которому клиенты фактически обращаются; не полагайтесь на Host запроса.
- EAB, белый список доменов, mTLS и ограничения по источникам — в [Базовой безопасности](security.md).

## Минимальная конфигурация после установки
### Безопасность учётной записи
- Сразу включите TOTP или добавьте ключ доступа.
- Настройте контролируемую почту восстановления с MFA; закрепите учётную запись администратора за должностью и завершайте передачу при увольнении.
- В «Настройки → Сеансы» проверьте и отзовите неизвестные сеансы.

### Данные и резервные копии
```bash
# Сначала остановите запись, затем копируйте, или используйте зашифрованную резервную копию в интерфейсе
tar czf lactssl-backup-$(date +%F).tar.gz config.yaml data
chmod 600 lactssl-backup-*.tar.gz
```
В продакшене используйте «Системные настройки → Резервное копирование и восстановление» для зашифрованного ZIP и храните копии на отдельном носителе; пароль и `config.yaml` храните раздельно.

### Плагины
Официальные реализации DNS, развёртывания и уведомлений уже встроены, устанавливать или загружать их не нужно:

1. В «Плагинах» убедитесь, что нужная категория включена.
2. В списке включите нужных DNS-провайдеров, цели развёртывания или каналы уведомлений.

DNS-провайдеры по умолчанию отключены, категория развёртывания включена, категория уведомлений включает почту и типовые каналы. Сторонние helper по-прежнему можно загрузить, см. [Управление плагинами](plugins.md).

### Проверка
```bash
# Процесс и порты
curl -I http://127.0.0.1:8080/          # без Cookie защищённой точки входа должен вернуть 404, это ожидаемо
ss -lntp | grep 8080

# Проверка аутентификации с помощью API-ключа
curl -H "Authorization: Bearer xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" \
  https://ssl.example.com/api/status
```
Затем в интерфейсе создайте и проверьте учётную запись DNS, тестовую задачу, тестовое уведомление и мониторинг сайта; один раз скачайте и расшифруйте резервную копию.

## Обновление и удаление
Перед обновлением обязательно сделайте резервную копию; шаги обновления — в [Обновление и изменения](upgrade.md).

Docker:
```bash
docker compose pull
docker compose up -d
```
Бинарный файл:
```bash
sudo systemctl stop lactssl
sudo cp lactssl /opt/lactssl/lactssl
sudo chown lactssl:lactssl /opt/lactssl/lactssl
sudo systemctl start lactssl
```
Перед удалением экспортируйте резервную копию. Удаление `data/` навсегда лишит вас базы данных, плагинов и зашифрованных учётных данных; удаление `config.yaml` лишит `salt` и `jwt_secret`.

## Частые проблемы при установке
| Симптом | Причина | Решение |
| --- | --- | --- |
| `docker compose up` не находит внешнюю сеть | Зависимость от `1panel-network` | Удалите блоки `networks` или создайте сеть |
| Корневой путь возвращает 404, Cookie и ограничение частоты работают неверно | Запрос не через точку входа; не настроены доверенные прокси | Используйте адрес из журнала, настройте `trusted_proxies` и `public_url` |
| Загрузка резервной копии/плагина не удалась, ключ доступа недоступен | Мало `client_max_body_size`; нет HTTPS или доступ по IP | Увеличьте лимит, используйте HTTPS-домен или localhost |

Больше об устранении неполадок — в [Мониторинге и устранении неполадок](troubleshooting.md).
