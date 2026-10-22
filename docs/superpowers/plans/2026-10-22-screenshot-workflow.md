# Работа со скриншотами — Implementation Plan

> **For agentic workers:** использовать superpowers:executing-plans, реализация в текущем checkout без commits/push/PR.

**Goal:** выполнить этап 5 roadmap: импорт, вставка, перестановка и независимые копии изображений истории.
**Architecture:** Go проверяет и нормализует изображения, владеет очередью и persistence; renderer передаёт байты одного файла и показывает подтверждённый порядок. Существующий Submit остаётся единственным путём запроса.
**Tech Stack:** Go stdlib, Wails v2, React/TypeScript, существующие UI controls; без новых зависимостей.
**Spec:** [утверждённое описание](../specs/2026-10-22-screenshot-workflow-design.md).

## Global Constraints

- Текущий checkout; без commits/push/PR. Пользователь утвердил описание и реализацию.
- PNG/JPEG, 32 МиБ входа и PNG, 1…32768 по стороне, максимум 40 000 000 пикселей; очередь 20.
- Архивные оригиналы не изменяются; копии имеют новые ID. Новые ограничения применяются к новым поступлениям.
- I/O вне controller mutex, один владелец операции, workers учитываются в Close.
- Generated bindings только через make bindings; ошибки UI по-русски; fake providers в тестах.

## Review Focus

1. Асинхронное чтение File после переключения чата: исходный chatID не теряется.
2. Ошибка записи state после Commit: файл и очередь остаются доступны, WorkError виден.
3. Архивный снимок, который ещё остался в очереди после ошибки запроса: новая копия не разделяет удаляемый источник.
4. Изменённая/усечённая PNG-структура и маленький файл с огромными dimensions: отказ до больших аллокаций.
5. Быстрый захват при подготовленном тексте/ссылке и переставленных вложениях: отправляет только новый кадр.

### Task 1: Валидация и безопасная запись PNG

**Files:** новый internal/attachments/{image.go,orientation.go,image_test.go,AGENTS.md,CLAUDE.md}; internal/workstate/{store.go,store_test.go}.
**Interfaces:** attachments.Normalize([]byte) ([]byte,error), attachments.ValidatePNGFile(string) error; workstate.Store.WriteImage(id string, data []byte) (string,error).

- [x] Написать image tests: PNG/JPEG → PNG; пустой/битый/неизвестный формат, APNG, byte/pixel/dimension limits, oversized output.
- [x] Запустить `go test ./internal/attachments`, убедиться в RED.
- [x] Реализовать bounded чтение, DecodeConfig перед Decode, PNG chunk validation/APNG rejection и нормализацию JPEG с ограниченным writer.
- [x] Добавить WorkStore.WriteImage через os.Root(work), O_EXCL временный файл, Sync/Commit; ошибки убирают только собственный временный файл.
- [x] Проверить обычную запись и symlink escape тестами; `go test ./internal/attachments ./internal/workstate` → PASS.

### Task 2: Команды контроллера и общий capture validation

**Files:** internal/app/{attachments.go,attachments_test.go,capture.go,operation.go,types.go,AGENTS.md}; app.go.
**Interfaces:** Controller.ImportAttachment(chatID,label,dataBase64 string) error; ReuseAttachment(chatID,id string) error; ReorderAttachments(chatID string,ids []string) error. WorkStore получает WriteImage.

- [x] Написать controller tests для импорта/подписи, busy/stale/full, перестановки/permutation validation и рестарта, независимости повторов истории, state-save failure и provider order.
- [x] Запустить тесты, убедиться в RED.
- [x] Реализовать общую операцию подготовки вложения, mutex guards и worker lifecycle. Ограничить base64 до DecodeString; историю разрешать по metadata сохранённого сообщения, не по renderer пути.
- [x] Reorder сохраняет точную перестановку синхронно; ошибку persistence сообщает существующий WorkError. Import/reuse копируют результат в WorkStore, затем flush очереди.
- [x] Capture валидирует новый PNG перед Commit; Submit проверяет <=20; сохранить reserve-slot и quick-only behavior.
- [x] Expose методы в App; `go test -race ./internal/app ./internal/history ./internal/workstate ./internal/attachments` → PASS.

### Task 3: Bridge и интерфейс

**Files:** frontend/src/bridge/{wails.ts,attachments.ts,attachments.test.ts}; frontend/src/chat/{Composer.tsx,AttachmentStrip.tsx,ChatView.tsx,attachments.test.tsx}; frontend/src/app/App.tsx; frontend/src/styles.css (фактический owning stylesheet уточнить по imports).
**Interfaces:** actions.importImage(chatID, File), actions.reuseImage(chatID,id), actions.reorder(chatID,ids). Файл читается внутри client.run, исходный chatID фиксируется до чтения.

- [x] `make bindings` после Go методов; generated output не редактировать.
- [x] Тесты UI: выбор/отмена/reselect, paste image/text/multiple/oversized, номера/края кнопок, disabled/full, повтор истории; async File со старым chatID.
- [x] Запустить UI tests, убедиться в RED.
- [x] Реализовать file helper с ограничением размера до FileReader и переводом в base64; команды в wails.ts через client.run. Ошибки чтения идут по общему пути.
- [x] Composer: один file input и onPaste с сохранением обычной текстовой вставки; несколько файлов отклоняются явно.
- [x] AttachmentStrip: номера и кнопки порядка для очереди, отдельное действие добавления у архивных фото. App связывает очередь, историю и панель фото с командами.
- [x] Обновить локальные инструкции; `pnpm exec vp check`, `pnpm exec vp run typecheck`, `pnpm exec vp test run` → PASS.

### Task 4: Проверки, документация и ревью

**Files:** README.md, docs/windows-acceptance.md, docs/roadmap/2026-10-21-core-features.md, этот план.

- [x] Обновить описание источников/лимитов, Windows checklist: clipboard/file/viewer/reorder/restart/copy independence/520×400.
- [x] `make check`, `go test -race -count=1 ./internal/...`, `git diff --check` → PASS.
- [x] Независимое ревью всего diff; существенные findings воспроизвести тестом RED→GREEN, затем прогнать нужные gates.
- [x] Отметить реализацию в roadmap отдельно от нативной Windows-приёмки. Записать результаты и ограничения ниже.

## Результаты

- `make check` пройден: bindings, frontend check/typecheck, 60 frontend-тестов,
  Go tests, host/Windows golangci-lint (0 issues), Windows vet и сборка
  `build/bin/Solveo.exe`.
- Frontend check: 0 предупреждений/ошибок lint/types. Сборщик сохраняет
  прежнее предупреждение о chunk >500 kB; новых зависимостей не добавлено.
- Контроллер/хранилище/валидатор прошли targeted race suite; полный uncached
  race suite пройден до ревью. После исправления EXIF повторные `make check`
  и `go test -race -count=1 ./internal/...` также пройдены.
- Дополнительные проверки: Close дожидается импортирующего worker; поздняя
  копия не попадает в очередь; quick capture сохраняет порядок, текст и ссылку.
- Имя файла обрезается и по символам, и по существующему лимиту workstate
  200 байт; проблема длинного кириллического имени воспроизведена RED→GREEN.
- App integration test прошёл RED→GREEN при удалении/возврате callback
  повторного использования; первоначальная фикстура исправлена на валидный wire.
- Windows runtime не проверен: clipboard WebView2, системный выбор файла,
  нативный просмотр, фокус, размеры окна и actual capture требуют приёмки
  по `docs/windows-acceptance.md`, раздел «Этап 5».
- По отдельному разрешению пользователя изменения публикуются в ветке
  `feat/screenshot-workflow`, PR на `master` с последующим merge после CI.


## Независимое ревью и решения

- Один Important: JPEG терял отображаемый EXIF-поворот/отражение при PNG-нормализации.
  TestNormalizePreservesJPEGDisplayOrientation воспроизвёл дефект RED→GREEN;
  проверяет все восемь значений и оба порядка байтов TIFF, по пикселям и размерам.
  Ограниченный парсер IFD0 и read-only image view не добавляют зависимости
  и вторую bitmap; некорректная необязательная ориентация игнорируется.
- Ruling: системный chooser WebView2 не имеет собственной гарантии capture affinity;
  не распространять на него проверенные гарантии main/helper HWND. Это ограничение
  описано в README и явно включено в Windows acceptance. Ошибка этого решения
  означает видимость системного диалога при демонстрации экрана, а не доказанную
  проверками невидимость. Использовать Ctrl+V, если требуется избегать chooser.
- Ruling: реальный clipboard, focus/pin, 520×400 и пиковый RSS предельного JPEG
  остаются ручной Windows-приёмкой. Автоматические проверки подтверждают
  ограничения и порядок/владение, не runtime/UI и не измеренную производительность.
  Цена пропуска приёмки — неизвестные платформенные ограничения.
- Основание ориентации: [Microsoft PhotoOrientation](https://learn.microsoft.com/en-us/uwp/api/windows.storage.fileproperties.photoorientation).

Повторный `make check` после исправления EXIF пройден полностью,
включая host/Windows lint (0 issues), 60 frontend-тестов, Windows vet и
пересборку EXE. Существенных открытых findings после исправления нет;
повторное независимое ревью не выполнялось, исправление проверено RED→GREEN.

Статус: реализация и автоматическая проверка этапа 5 завершены.
Windows/Wails runtime-приёмка остаётся невыполненной; checklist — раздел «Этап 5»
в docs/windows-acceptance.md.
