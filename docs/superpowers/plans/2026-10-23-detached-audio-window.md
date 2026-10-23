# Отдельное окно аудио

Согласовано: всю AudioPanel вместе с транскриптом вынести из основного окна.
Размер чата и редактора не меняется. Кнопка микрофона открывает/поднимает окно;
закрытие скрывает его, распознавание продолжается. При выходе из Solveo окно
завершается. Окно tray-only, исключено из захвата до первого показа.

Архитектура: Wails v2 имеет одно окно на процесс. Второй renderer запускается
тем же EXE с отдельным режимом до singleton/history initialization. Он не
открывает хранилища и не создаёт STT/LLM. Главный процесс владеет одним
контроллером и запускает не более одного дочернего renderer. Существующая
AudioPanel используется повторно через отдельную frontend HTML entry.

Транспорт: HTTP на случайном порту строго 127.0.0.1, без CORS, с 256-bit
случайным Bearer token. Endpoint и token передаются дочернему процессу через
stdin, не argv/env/files/logs. Только ограниченные аудиокоманды и минимальный
snapshot (AudioView, активный чат, доступность отправки, тема). Состояние
опрашивается последовательно раз в 200 мс. При потере родителя renderer
завершается; при нормальном выходе родитель закрывает endpoint и child.

Отправка реплик привязана к показанному ChatID и отклоняется при смене чата.
Вставка в редактор проходит через текущий основной renderer с подтверждением,
чтобы не перезаписать несохранённый ввод или IME. Пауза/скрытие окна не означает
StopListening. Секретный ключ доступен только через write-only команду.

- [x] Portable server/client: auth, ограничения тела, allowlist, command errors,
  snapshot без чатов/ключей, stale-chat и подтверждение вставки; fake tests.
- [x] Child lifecycle + отдельный Wails entry, readiness/capture affinity,
  повторное открытие, скрытие и parent death.
- [x] Frontend shell повторно использует AudioPanel, показывает целевой чат,
  ошибки и тему; main заменяет inline panel на open-window action. Tests.
- [x] make check, race tests, документация и отдельная Windows-приёмка.

Не входит: автоматический анализ вопросов/отправка, новые STT, миграция Wails.
Ручная Windows-приёмка обязательна: фактическая независимость окон, фокус,
DPI/мониторы, capture exclusion, закрытие/повторное открытие и exit.


Реализация и проверки:
- Один дочерний процесс, основной controller владеет всеми командами и STT.
  Минимальный AudioWindowTarget не клонирует историю при polling.
- Отдельный Go polling /health (200 мс) проверяет родителя и доставляет
  монотонный show_version даже при замедлении JS timers скрытого WebView.
  Проверено readiness/устаревшие версии через go test -race -tags bindings .
- Просроченные вставки отклоняются в основном renderer; pending ack удаляются
  при успехе/отмене. Новые frontend tests проверяют send target, отсутствие
  inline panel и expiry/ack; portable tests — auth, allowlist, limits, ack,
  show intent и выбор embedded audio entry.
- Независимое ревью выявило конфликт наследуемого Wails dev server. Исправлен
  ChildEnvironment, покрыт тестом; child использует embedded assets, без HMR.
  Cross-build root с -tags dev — PASS (не runtime-доказательство).
- Ручная Windows-приёмка нового окна остаётся невыполненной, см.
  docs/windows-acceptance.md. Она не заменяется успешной сборкой.

Итоговая автоматическая проверка: make check — PASS (90 frontend tests,
40 worker tests, Go suite, host/Windows lint, Windows vet и EXE);
go test -race ./internal/audiowindow ./internal/app — PASS;
go test -race -tags bindings . — PASS.
