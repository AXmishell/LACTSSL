---
hide:
  - navigation
  - toc
---

<section class="hero" markdown>
<div class="hero-badge">Self-hosted · один экземпляр · открытый код</div>

# LACTSSL

<p class="hero-sub">Выпуск сертификатов, автопродление, развёртывание и контроль сроков в одной self-hosted консоли. Разработчику достаточно одного небольшого хоста для полного цикла.</p>

[Быстрый старт](getting-started.md){ .md-button .md-button--primary }
[Установка](install.md){ .md-button }
</section>

<div class="feature-grid" markdown>

<div class="feature-card" markdown>
### :material-autorenew: Автопродление и развёртывание
Заявка, продление, доставка и контроль сроков ACME-сертификатов в одном месте вместо разрозненных скриптов и таблиц.
</div>

<div class="feature-card" markdown>
### :material-server-network: Частный CA и внутренний ACME
Создание или импорт корневого / промежуточного CA и стандартная конечная точка ACME v2 для certbot, lego и acme.sh.
</div>

<div class="feature-card" markdown>
### :material-file-export-outline: Доставка в несколько целей
Локальные каталоги и SSH/SFTP встроены; FTP, Webhook, облачные CDN, BaoTa, 1Panel и SafeLine WAF подключаются плагинами развёртывания.
</div>

<div class="feature-card" markdown>
### :material-monitor-eye: Контроль сроков сайтов
Периодическое чтение сертификатов внешних сайтов, проверка цепочки и имени хоста, уведомления об ошибках и скором истечении.
</div>

<div class="feature-card" markdown>
### :material-shield-check-outline: Аудит и self-hosted
Один бинарный файл, SQLite, без внешних зависимостей; защищённый вход, TOTP/ключи доступа, шифрование полей, журналы и зашифрованные копии.
</div>

<div class="feature-card" markdown>
### :material-bell-ring-outline: Уведомления и интеграции
Встроенная почта и типовые каналы; REST API и ключи с правами по ресурсам для CI/CD.
</div>

</div>

## Документация

<div class="doc-grid" markdown>

<div class="doc-card" markdown>
[:material-rocket-launch-outline: Быстрый старт](getting-started.md)
<span>Установка, инициализация и первый сертификат.</span>
</div>

<div class="doc-card" markdown>
[:material-download-outline: Установка](install.md)
<span>Docker, бинарный файл, обратный прокси и HTTPS.</span>
</div>

<div class="doc-card" markdown>
[:material-format-list-checks: Возможности](features.md)
<span>Сертификаты, задачи, частный CA, ACME и мониторинг.</span>
</div>

<div class="doc-card" markdown>
[:material-server-network: Архитектура](architecture.md)
<span>Топология, порты, ёмкость и доступность.</span>
</div>

<div class="doc-card" markdown>
[:material-shield-lock-outline: Безопасность](security.md)
<span>Идентификация, шифрование, аудит и чек-лист запуска.</span>
</div>

<div class="doc-card" markdown>
[:material-cog-outline: Конфигурация](configuration.md)
<span>Все параметры, обратный прокси и настройки плагинов.</span>
</div>

</div>

## Интерфейс

[![Панель LACTSSL](assets/images/dashboard.png){ .doc-screenshot .doc-screenshot--home }](assets/images/dashboard.png){ target="_blank" rel="noreferrer" }

<p class="home-note">LACTSSL — self-hosted инструмент с одним администратором и одним экземпляром. Многопользовательский RBAC, SSO, кластеры и HSM не поддерживаются. Полные границы — в разделе [Возможности](features.md).</p>
