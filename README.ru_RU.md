[English](/README.md) | [Русский](/README.ru_RU.md)

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="./media/3ax-ui-dark.png">
    <img alt="3ax-ui" src="./media/3ax-ui-light.png">
  </picture>
</p>

<p align="center"><b>3AX-UI для Pterodactyl</b> — панель 3ax-ui, упакованная в <a href="https://pterodactyl.io">Pterodactyl</a>-egg, которая работает <b>полностью без привилегий</b>: без root, без <code>/dev/net/tun</code>, без дополнительных capabilities.</p>

[![Go Version](https://img.shields.io/github/go-mod/go-version/iWebbIO/3ax-ui-pterodactyl.svg)](#)
[![License](https://img.shields.io/badge/license-GPL%20V3-blue.svg?longCache=true)](https://www.gnu.org/licenses/gpl-3.0.en.html)

> [!IMPORTANT]
> Проект предназначен только для личного использования. Не используйте его в противозаконных целях.

---

## Что это

Этот репозиторий — **форк [3ax-ui](https://github.com/coinman-dev/3ax-ui)**, единственная цель которого — **запуск панели в Pterodactyl**. Контейнеры игровой панели работают без привилегий: нет root, доступна для записи только `/home/container`, а порты выдаются из аллокаций. Этот форк специализирует панель именно под такую среду.

Всё работает как один управляемый процесс внутри собственного Docker-образа («yolk»), где все исполняемые файлы (панель, Xray-core, geo-данные, `mtg`/`mtg-multi`) **вшиты в образ**. Узел ничего не скачивает при установке — он лишь готовит том сервера.

Если вам нужна обычная установка на VPS (systemd, `install.sh`, AmneziaWG на уровне ядра) — используйте вышестоящий проект [coinman-dev/3ax-ui](https://github.com/coinman-dev/3ax-ui). Этот путь здесь унаследован, но **не** является целью данного форка.

---

## Поддержка протоколов в Pterodactyl

| Протокол / возможность | Статус | Примечания |
|---|---|---|
| Веб-панель + подписки | ✅ Доступно | Панель слушает основную аллокацию сервера |
| **VLESS, VMess, Trojan, Shadowsocks** | ✅ Доступно | Все транспорты Xray: TCP, WS, gRPC, HTTPUpgrade, XHTTP, mKCP, QUIC — плюс **REALITY** и **XTLS** |
| **SOCKS5 и HTTP** прокси | ✅ Доступно | Полная инфраструктура по пользователям (трафик, квоты, срок, лимит IP) |
| **MTProto** (Telegram FakeTLS) | ✅ Доступно | `mtg-multi` (мультипользовательский) на amd64/arm64, одиночный `mtg` на прочих |
| **AmneziaWG** (1.x и 2.0) | 🚧 Фаза 2 | Движок на netstack в userspace — обфускация сохранена, без root. См. план ниже |
| **нативный WireGuard** | 🚧 Фаза 2 | Тот же userspace-движок |
| Нативный публичный IPv6 без NAT (NDP-прокси) | ❌ Не в Pterodactyl | Требует `CAP_NET_ADMIN` + маршрутизируемый префикс на хосте; трафик идёт через NAT |
| Проброс портов по клиенту (iptables) | ❌ Не в Pterodactyl | Требует root/iptables внутри контейнера |
| fail2ban | ❌ Отключён | Требует root/iptables |

Всё на базе Xray и MTProto работает уже сейчас без привилегий. AmneziaWG / нативный WireGuard появятся в Фазе 2 через userspace-движок (дизайн в [`.ai/PTERODACTYL_EGG_PLAN.md`](.ai/PTERODACTYL_EGG_PLAN.md)).

---

## Быстрый старт

Полное руководство оператора: **[`pterodactyl/README.md`](pterodactyl/README.md)**.

**1. Соберите и запушьте образ** (из корня репозитория):

```bash
docker buildx build -f pterodactyl/Dockerfile \
  --platform linux/amd64,linux/arm64 \
  -t ghcr.io/iwebbio/3ax-ui-pterodactyl:latest --push .
```

**2. Импортируйте egg:** админка Pterodactyl → **Nests → Import Egg** → загрузите [`pterodactyl/egg-3ax-ui.json`](pterodactyl/egg-3ax-ui.json).

**3. Создайте сервер:** выделите **основную аллокацию** под панель плюс **по одному свободному порту на каждый inbound**, который планируете запускать. Из интернета доступны только выделенные порты (TCP+UDP).

**4. Войдите:** запустите сервер; когда в консоли появится `3AX-UI online — panel listening on port <N>`, откройте `http://<node-ip>:<primary-port>/`.

---

## Заметки по протоколам

### MTProto — прокси для Telegram (FakeTLS)
FakeTLS-прокси для Telegram, запускается как отдельный сайдкар `mtg` / `mtg-multi` (не Xray) и управляется со страницы **Inbounds**, как любой другой протокол.

- Выберите публичный (выделенный) порт и **домен прикрытия FakeTLS** — соединение маскируется под TLS 1.3 к этому домену; кнопка **↻** подставляет случайный домен из подобранного списка.
- Секрет FakeTLS генерируется автоматически и выдаётся как **deep-link `tg://proxy`** + QR.
- **Мультипользовательский режим** на amd64/arm64 через [mtg-multi](https://github.com/dolonet/mtg-multi): много клиентов на одном порту, у каждого свой UID, имя, секрет, ссылка/QR, трафик, квота, срок и статус онлайн. На прочих архитектурах — откат на одиночный [mtg](https://github.com/9seconds/mtg).
- Опция **Route through Xray**: mtg ходит в Telegram через loopback-SOCKS-мост, внедрённый в конфиг Xray, поэтому исходящий трафик подчиняется маршрутизации Xray (полезно, когда Telegram заблокирован на самом узле).

### Прокси SOCKS5 и HTTP с инфраструктурой по пользователям
Inbound'ы `mixed` (SOCKS5) и `http` в Xray используют тот же VLESS-подобный стек, что VLESS/VMess/Trojan/Shadowsocks: раскрывающаяся таблица клиентов с трафиком, сроком, квотой, лимитом IP и переключателем; автогенерация учётных данных (с возможностью пересоздать); статистика по пользователям через стандартные ключи трафика Xray, поэтому задания по квотам/сроку работают автоматически. Имя пользователя редактируется после создания без сброса счётчиков трафика.

### AmneziaWG и нативный WireGuard (Фаза 2)
AmneziaWG — это WireGuard с обфускацией пакетов, из-за которой трафик неотличим от случайного шума (обходит DPI в России/Иране/Китае). На обычном хосте форк управляет им через ядро (`awg-quick` + iptables + NDP) — что невозможно в непривилегированном контейнере Pterodactyl.

В Фазе 2 это заменяется **userspace-движком на netstack**: `amneziawg-go` / `wireguard-go` работают в самом процессе с сетевым стеком gVisor в userspace (без TUN-устройства, без capabilities), а встроенный форвардер выполняет NAT клиентских потоков в интернет — при желании через маршрутизацию Xray. Обфускация полностью сохраняется; компромисс — выдача нативного публичного IPv6 деградирует до NAT-исхода. Прогресс и дизайн — в [`.ai/PTERODACTYL_EGG_PLAN.md`](.ai/PTERODACTYL_EGG_PLAN.md).

---

## Чем отличается от обычной установки 3ax-ui

| Область | Обычная (VPS) | Этот форк (Pterodactyl) |
|---|---|---|
| Супервизор процесса | systemd + меню `x-ui.sh` | Один процесс `/app/x-ui` под управлением Wings; `^C` = мягкая остановка |
| Хранение | `/etc/x-ui`, `/var/log/x-ui`, `bin/` | Всё под `/home/container` через `XUI_DB_FOLDER` / `XUI_LOG_FOLDER` / `XUI_BIN_FOLDER` |
| Порты | любые, включая привилегированные | Только **аллокации** сервера (высокие порты); панель на основной |
| Бинарники | скачивает `install.sh` | Вшиты в Docker-образ |
| WireGuard/AmneziaWG | ядро (`awg-quick`, TUN, iptables) | Userspace netstack, без root (Фаза 2) |
| fail2ban | включён | отключён (нет root) |
| TLS | ACME HTTP-01 на :80/:443 | Загрузка сертификатов в `/home/container/cert` или DNS-01 |

Укажите **внешний адрес** (IP узла или домен) в настройках панели, чтобы клиентские ссылки и URL подписок формировались правильно — контейнер не может сам определить публичный адрес узла.

---

## Структура репозитория (специфика Pterodactyl)

```
pterodactyl/
├── Dockerfile        — непривилегированный (uid 988) yolk; вшивает панель + Xray + mtg
├── entrypoint.sh     — маппит XUI_* на /home/container, порт/админ при первом старте, запуск панели
├── fetch-bins.sh     — вшивает сайдкары MTProto по архитектуре
├── egg-3ax-ui.json   — Pterodactyl-egg (PTDL_v2)
└── README.md         — руководство оператора
.ai/PTERODACTYL_EGG_PLAN.md — полный план портирования, решения и статус фаз
```

Исходники панели (`web/`, `xray/`, `awg/`, `wg/`, `mtproto/`, `sub/`, …) не изменены, кроме небольших адаптаций под Pterodactyl, отражённых в плане.

---

## Совместимые клиенты

| Протокол | Клиент | Платформы |
|----------|--------|-----------|
| VLESS / VMess / Trojan / Shadowsocks | v2rayN, v2rayNG, Nekoray, sing-box, Streisand и др. | Все платформы |
| SOCKS5 / HTTP | любой стандартный прокси-клиент | Все платформы |
| MTProto | Telegram (встроенная поддержка прокси) | Все платформы |
| AmneziaWG (Фаза 2) | AmneziaVPN — [amnezia.org](https://amnezia.org) | Android, iOS, Windows, macOS, Linux |
| нативный WireGuard (Фаза 2) | Официальный WireGuard | Все платформы |

> Стандартные клиенты WireGuard **несовместимы** с AmneziaWG — они не поддерживают параметры обфускации.

---

## Основано на

- **[coinman-dev/3ax-ui](https://github.com/coinman-dev/3ax-ui)** — прямой upstream (добавляет AmneziaWG, нативный WireGuard, MTProto).
- **[MHSanaei/3x-ui](https://github.com/MHSanaei/3x-ui)** — панель, которую форкает сам 3ax-ui (VLESS, VMess, Trojan, Shadowsocks, Xray, подписки, Telegram-бот).
- Сайдкары MTProto: **[9seconds/mtg](https://github.com/9seconds/mtg)** (одиночный) и **[dolonet/mtg-multi](https://github.com/dolonet/mtg-multi)** (мультипользовательский).

## Благодарности

- [MHSanaei](https://github.com/MHSanaei/) — автор оригинального 3x-ui
- [alireza0](https://github.com/alireza0/) — автор оригинального x-ui
- [coinman-dev](https://github.com/coinman-dev/3ax-ui) — форк 3ax-ui, на котором основан этот проект
- [9seconds/mtg](https://github.com/9seconds/mtg) и [dolonet/mtg-multi](https://github.com/dolonet/mtg-multi) — сайдкары MTProto
- [Iran v2ray rules](https://github.com/chocolate4u/Iran-v2ray-rules) (GPL-3.0) · [Russia v2ray rules](https://github.com/runetfreedom/russia-v2ray-rules-dat) (GPL-3.0)

---

## Лицензия

Распространяется под той же лицензией, что и оригинальный 3x-ui — [GNU GPL v3](LICENSE).
