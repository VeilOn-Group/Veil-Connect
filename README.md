# veilon-app: Veil Connect (VeilOn VPN client)

Лицензия: **GPL-3.0-or-later** (`LICENSE`, полный текст в `COPYING`).
Сторонние компоненты: `THIRD-PARTY-LICENSES.md`.

| Каталог | Что внутри |
|---|---|
| `veilond/` | служба клиента: супервизор sing-box и Xray (VLESS+REALITY+XHTTP по умолчанию, Vision), Hysteria 2, NaiveProxy (in-tree и внешний), kill switch (nftables, pf, Windows Firewall), локации, раздельное туннелирование (.ru, .рф, geosite category-ru), выбор DNS (DoH: Quad9 по умолчанию, Cloudflare, Google, AdGuard, OpenDNS, свой), автопереключение, тихие обновления через `veilon-updater`, оплата, поддержка с диагностикой, автоподключение при старте, очередь отчётов о сбоях (только с согласия), `veilonctl` |
| `desktop/` | UI (Wails v3 + React), запуск при входе в систему (`--hidden`), передача своего падения в veilond |
| `android/` | Android (Kotlin, Compose, VpnService, libbox, Xray), подключение после загрузки, отчёты о сбоях JVM и Go |
| `packaging/` | systemd, LaunchDaemon, MSI (wixl), примеры конфигов |
| `tools/package/` | сборка deb, rpm, msi, pkg и проверка тихого обновления |

## Репозитории (ADR-0074)
Нужен `veil-shared` (MPL-2.0) рядом: `veilond/go.mod`, Android-мост и
фронтенд десктопа ссылаются на `../veil-shared`. Закреплённые версии ядер:
`veil-shared/build/*.lock`. Проверка тихого обновления берёт
`veilon-keygen` из соседнего `veil-api` (`API_DIR`).

```
workspace/veil-shared/
workspace/veilon-app/     этот репозиторий
workspace/veil-api/       только для tools/package/test-linux-update.sh и e2e
```

## Сборка и проверка
```bash
make build test lint nolog nodash repro
make desktop                  # Go >= 1.26, GTK4 + WebKitGTK 6.0 + libsoup 3
make android                  # закреплённый тулчейн в контейнере (ADR-0061)
SINGBOX_BIN=... XRAY_BIN=... make packages
```

Конфиг службы: `runtime_dir`, `state_dir`, `log_dir`, `singbox{path, sha256, extra_files}`,
`xray{path, sha256}`, `naive{mode, path, sha256}`, `api{base_urls, spki_pins}`,
`trust.root_keys`, `local_api{socket, allowed_groups}`, `update{inbox}`,
`billing{payment_hosts}`. Секретов в конфиге нет. veilond не запускается, если
конфиг или бинарники может изменить кто-то кроме root и учётной записи службы.

Скриншоты: `docs/screenshots/d_*.png`. Решения и политика данных:
репозиторий `veil-api`, каталог `docs/`.
