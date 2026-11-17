# Meeting Recording Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Локальная запись встречи, файловая транскрибация Gemini и summary с сохранением результатов.

**Architecture:** `internal/meeting` владеет файловым архивом, записью и заданиями с внедряемыми Capturer/Processor. `internal/meeting/gemini` реализует Files/Interactions HTTP; controller сериализует захват с live ASR. Аудиоокно получает список/детали через существующий authenticated IPC; файлы проигрываются через ограниченный local handler.

**Tech Stack:** Go stdlib, WASAPI, Gemini REST v1beta, React/TypeScript, Radix Tabs; без новых зависимостей.

**Spec:** `docs/superpowers/specs/2026-11-17-meeting-recording-design.md`

## Global Constraints

- Текущий checkout; без commit/push/release и без изменения пользовательских AGENTS.md/CLAUDE.md.
- Запись не зависит от Gemini; один capture mode, одна обработка, без автоматического resume.
- PCM16 WAV mono native rate; части 5 минут; overlap запроса 2 секунды.
- Model IDs: `gemini-3.5-transcribe`, `gemini-3.8-flash`; существующий Credential Manager key, `store:false`.
- Ограничения первой версии: 8 часов / 8 GiB на встречу, 2 источника, 192 kHz максимум; 64 MiB manifest/results. Переполнение останавливает запись с явной ошибкой. Очередь 100 фреймов на дорожку; ошибка вместо потери данных.
- Все сохранённые даты UTC; длительности/смещения — секунды. I/O не под controller/audio mutex.
- Fake HTTP и PCM; реальный Windows/Gemini runtime отдельно.

## Review Focus

- Переключение чата/скрытие аудиоокна не меняет владельца записи (Task 4/5).
- Отказ одного устройства/переполнение writer не оставляет ложный статус записи (Task 2).
- Повтор после сетевой ошибки сохраняет готовые части, cancel/exit не публикуют поздний результат (Task 3/4).
- Неполная/повреждённая запись не выдаётся за полный транскрипт и summary (Task 1/3).
- Истёкшие file IDs, недопустимые пути и неизвестные speaker IDs не раскрывают произвольные файлы и не объединяют людей (Task 1/3/5).

## Task 1: Архив и контракт встречи

**Files:** create `internal/meeting/{types,store,wav}.go`, corresponding `_test.go`, `AGENTS.md`, `CLAUDE.md`.
**Interfaces:** `Store.New(title,chatID string,tracks []string) (Document,error)`, `Load(id string) (Document,error)`, `Save(Document) error`, `List() ([]Info,error)`, `OpenAudio(id,file string) (*os.File,error)`, `Recover(id string) (Document,error)`; `Document` includes Info, Parts, Segments, Summary. `Processor.Transcribe(context.Context, AudioInput) ([]Word,error)`, `Summarize(context.Context, []Segment) (Summary,error)`, `Cleanup(context.Context) error`.

- [x] Write tests for atomic roundtrip, traversal/symlink/corrupt/newer document protection, WAV PCM clipping/downmix, unfinished WAV recovery.
- [x] Run `go test ./internal/meeting` and observe missing implementation.
- [x] Implement versioned bounded archive using os.Root; list metadata without full transcripts; deterministic WAV names, no renderer paths; recovery only explicit. Immutable meeting identity, part/segment reference validation.
- [x] Run `go test -race ./internal/meeting` → PASS.

## Task 2: Независимая запись

**Files:** create `internal/meeting/{recorder,manager}.go`, tests; modify `internal/audio/audio.go` only if timing contract requires it.
**Interfaces:** `NewManager(*Store,audio.Capturer,Processor) *Manager`, `Start(ctx,title,chatID string,tracks []string) error`, `Stop() error`, `List() ([]Info,error)`, `Load(id string) (Document,error)`, `State() State`, `Recover(id string) error`, `Close() error`.

- [x] Test sample-exact rotation/native rates/source offsets, nonblocking bounded intake, device failure, close/restart and byte/time limits using synthetic capture.
- [x] Run relevant tests RED; implement async writer per source, incremental manifest checkpoints after rotation, recoverable open WAV, idempotent stop, monotonic source start offsets, no restart at chunk boundaries.
- [x] Propagate writer failure through one cancellation path and retain readable files; never auto-continue missing source.
- [x] Run `go test -race ./internal/meeting ./internal/audio` → PASS.

## Task 3: Gemini и задания обработки

**Files:** create `internal/meeting/{processing,export}.go`, `internal/meeting/gemini/{client,transcribe,summary}.go`, tests and module guides.
**Interfaces:** `gemini.New(key func()(string,bool,error)) *Client`; implements Processor; manager adds `Process(id,kind string,partial bool) error`, `Cancel()`, `Rename(id,title string) error`, `Export(id string) error`; State exposes recording/processing ID, stage, progress, error.

- [x] Fake HTTP tests: resumable Files upload, key header/store:false, word annotation parsing, speaker scoping, cleanup on failure/cancel, quota/sanitized error, invalid timestamps/truncated output, structured summary ID validation.
- [x] Tests RED; implement 2 s neighboring PCM extraction, sequential transcribe/checkpoint with 30-min preflight, completed-status and bounds validation; preserve original audio. Persist uploaded file IDs for cleanup/retry; never store keys/raw requests. Disallow redirects.
- [x] Summary uses all validated segments; structured schema, source IDs required for decisions/tasks. Bound leaf groups by text size (48k UTF-8 bytes), persist leaf summaries then combine; no silent tail dropping, preserve prior summary on errors. Limit hierarchy to bounded meeting size and fail explicitly if a final reducer exceeds its input budget.
- [x] Implement cancellation/job ownership, recovery and retry of incomplete parts, partial-summary label, Markdown export written by application.
- [x] Run `go test -race ./internal/meeting/...` → PASS.

## Task 4: Controller, lifecycle и native wiring

**Files:** modify `internal/app/{controller,types,audio}.go`, create `internal/app/meetings.go`, tests; modify `main_windows.go`, `app.go`.
**Interfaces:** `Dependencies.Meetings *meeting.Manager`; controller `MeetingCommand(MeetingCommand) error`, `MeetingState() meeting.State`, `MeetingList() ([]meeting.Info,error)`, `MeetingDetail(id string) (meeting.Document,error)`, `OpenMeetingAudio(id,file string) (*os.File,error)`. Command action/id/title/tracks/partial. Main App exposes recording status through existing snapshots.

- [x] Test active recording excludes live start and vice versa, shutdown drains recording/jobs, chat switch preserves meeting ChatID, no c.mu held during capture/storage.
- [x] RED → implement shared capture ownership mutex, start/stop lifecycle, closed checks; wire production store/key/HTTP client; include recording indicator in main Snapshot and publish changes.
- [x] Run `go test -race ./internal/app ./internal/meeting/...` and Windows build checks.

## Task 5: IPC и UI

**Files:** modify `internal/audiowindow/transport.go`, `audio_window_windows.go`, `app_audio_window.go`; create meeting asset handler; modify frontend `bridge/{wails,state}.ts`, `audio/AudioWindow.tsx`, create `audio/Meetings.tsx` and tests/styles; main header indicator.
**Interfaces:** authenticated `/meetings`, `/meeting/<id>` reads; allowlisted `meeting` command; detail bridge validates schema. AudioWindowApp delegates list/detail and file bytes through parent, never reads disk itself. Embedded asset handler accepts `/meeting-audio/<id>/<file>` only, reads authenticated parent via bounded HTTP Range proxy; no arbitrary paths.

- [x] Test auth/path/range access and command rejection, then implement IPC and regenerate bindings with `make bindings`.
- [x] UI test start/stop, explicit cloud processing, cancellation, partial warning, retry, tab/chat switching, rename/export, source playback and evidence display. Implement Radix meetings tab with preserved draft name/source settings, list refresh on state change, explicit detail fetch independent of 200ms polling.
- [x] Run frontend checks/tests/build and Go IPC tests → PASS.

## Task 6: Documentation, full verification and review

**Files:** README.md, docs/windows-acceptance.md and affected local module guides; this plan and spec.

- [x] Document actual storage/volume/key/model usage/privacy/limits and recovery; add Windows meeting acceptance scenarios.
- [x] Run `make check`, affected race tests, `git diff --check`; fix failures and repeat only affected checks.
- [x] Independent whole-change review; address correctness findings and rerun relevant tests.
- [x] Update task checkboxes and final report with evidence and unverified real-device/API limits. No Git publication.


## Execution evidence

- Выполнено в текущем checkout без commit/push. Native execution; независимое ревью и адресная повторная проверка замечаний.
- Архив/WAV, ротация, временные метки/паузы, overflow callback, завершение lifecycle, recovery пустого хвоста, Gemini fake HTTP/cleanup, retry и long-summary reduction проверены автоматическими тестами.
- Дополнительный случай выхода: Close ждёт completion notifications всех workers, даже после снятия активного recording/job ID.
- Основные модули проверены под race detector. `make check` включает полный frontend suite (116 tests), Go/worker tests, host+Windows lint, Windows vet и Solveo.exe.
- Реальные WASAPI endpoints/QPC, качество RU/EN Gemini, доступ/квота моделей и native WebView layout не проверены в WSL; чеклист добавлен в docs/windows-acceptance.md. Искусственные тесты не заменяют эти проверки.
