# Veil Connect: сторонние компоненты

Клиент распространяется под GPL-3.0-or-later. Ниже компоненты, которые входят
в сборки клиента, и обязанности при распространении. Полная матрица
лицензий ведётся в серверной документации.

| Компонент | Лицензия | Использование | Где | Обязанности |
|---|---|---|---|---|
| sing-box | GPL-3.0-or-later + доп. условие [VERIFIED, LICENSE в релизе 1.13.21]: «In addition, no derivative work may use the name or imply association with this application without prior consent.» | Отдельный процесс (десктоп, нода); in-process libbox (Android) | client, node | Распространение бинарников → предоставить полный соответствующий исходный код (включая наши патчи и build-скрипты/теги), текст лицензии, уведомления; не использовать имя «sing-box» в бренде/не подразумевать аффилиацию (доп. условие) |
| sing-quic, sing-* библиотеки SagerNet | GPL-3.0 / по репозиторию | транзитивно в sing-box | client, node | как sing-box |
| cronet / Chromium (Naive) | BSD-3-Clause + лицензии third_party Chromium | libcronet в составе клиента | client | Атрибуция: текст BSD-3 + сводный файл лицензий Chromium third_party в «О программе» и в дистрибутиве |
| NaiveProxy (только fallback-режим) | BSD-3-Clause | внешний бинарник | client | Атрибуция |
| quic-go | MIT | транзитивно (Hysteria 2 в sing-box через sing-quic/quic-go форк) | client, node | Атрибуция (copyright + текст MIT) |
| Wintun | собственная лицензия на редистрибуцию подписанного DLL | DLL в установщике Windows | client | Соблюдение условий: только неизменённый подписанный wintun.dll |
| Wails | MIT | десктоп-UI | client | Атрибуция |
| React, Vite, Tailwind, lucide | MIT / ISC (lucide) | UI | client, web | Атрибуция в собранном бандле (license-plugin) |
| AndroidX / Jetpack Compose | Apache-2.0 | Android UI | client | NOTICE |
| Xray-core v26.9.30 | MPL-2.0 [VERIFIED, LICENSE в релизе] | официальный неизменённый бинарник, отдельный процесс | node, client (desktop, Android) | Текст MPL-2.0 и ссылка на исходники релиза; изменений файлов нет, поэтому раскрывать нечего |
| geosite category-ru (SagerNet/sing-geosite, ветка rule-set) | данные v2fly/domain-list-community, MIT | встроенный файл `.srs` | client | Атрибуция в «О программе» и THIRD_PARTY_NOTICES |
| veil-shared | MPL-2.0 | Go-модуль, дизайн-токены, бренд, rule-set | client | Исходники файлов MPL доступны в репозитории veil-shared; изменения этих файлов публикуются под MPL |
