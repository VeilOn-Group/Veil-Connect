# VeilOn для Android

Kotlin + Jetpack Compose, VpnService, ядро sing-box через libbox (gomobile).
Вся проверка подписей, валидация профиля и генерация конфига sing-box идут
через общий Go-код `veil-shared/mobile` (тот же генератор, что у десктопа),
поэтому правила zero-log, UDP/IPv6 и split-туннелирования одинаковы на всех
платформах.

Сборка закреплена в `toolchain.lock` (ADR-0061): Go 1.24.7, OpenJDK 17,
NDK 28.0.13004108, platform и build-tools 35, форк gomobile
`github.com/sagernet/gomobile` v0.1.12, Gradle 8.11.1, sing-box v1.13.21.
Все шаги идут в контейнере, собранном из этого файла:

```sh
scripts/lock-toolchain.sh        # один раз на доверенной машине с сетью:
                                 # digest образа Go, SHA-256 cmdline-tools и Gradle
scripts/build-aar.sh --docker    # app/libs/veilon-core.aar (+ .sha256)
scripts/docker.sh gradle --no-daemon :app:assembleRelease \
  -Pveilon.rootKeys=root-1=<BASE64_PUBKEY> \
  -Pveilon.apiBaseUrls=https://api.example -Pveilon.apiPins=<PIN1>,<PIN2>
```

libbox и мост `veil-shared/mobile` собираются **одним** вызовом gomobile в
один AAR (`net.veilon.core.libbox.*`, `net.veilon.core.mobile.*`, нативная
библиотека `libveilcore.so`): два отдельных AAR от gomobile несут каждый
свою копию Go-рантайма и классов `go.Seq` и конфликтуют при сборке.
Граф модулей закреплён в `corebind/go.mod`; `go.sum` проверяется по
sum.golang.org при первой сборке с сетью и коммитится (CI это требует).

Kill switch на Android включает система: «Постоянная VPN» и «Блокировать
соединения без VPN» (сервис объявляет `SUPPORTS_ALWAYS_ON`). Это надёжнее
любого прикладного решения: блокировку держит ОС, даже если процесс VeilOn
завершён.

Командный gRPC-сервер libbox (`CommandServer.start()`) не запускается:
у других приложений нет IPC-входа в ядро. Экспортированы только
лаунчер-активити и плитка быстрых настроек (вызывает только система).

**[ПРОВЕРИТЬ]** Gradle/AGP в среде разработки не запускались (нет доступа
к Maven). Проверено: Java-API, которое gobind генерирует для закреплённого
sing-box, и `vpn/LibboxEngine.kt` + `vpn/LibboxPlatform.kt` компилируются
kotlinc против этого API и android.jar (API 35), см.
`docs/verification/android-bind-api.txt`. Go-часть (`veil-shared/mobile`)
покрыта тестами.
