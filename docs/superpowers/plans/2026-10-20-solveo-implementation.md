# Solveo Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Реализовать Windows x64 Solveo с подготовкой интервью, текстовым/визуальным чатом, единственным адаптером Codex и закреплением окна.

**Architecture:** Wails владеет основным React-окном; отдельный Go/Win32 host обслуживает трей, хоткеи, selector, лог и просмотрщик. Go контролирует чат, архив и операции; Codex находится за небольшим LLM-контрактом. Frontend создаётся и обслуживается через Vite+.

**Tech Stack:** Go, Wails v2.17.0, React, TypeScript, Vite+, pnpm, Win32/WebView2; Go testing и Vitest через Vite+.

**Spec:** `docs/specs/solveo-design.md` — согласован пользователем 2026-10-20, включая §9.1/9.2 и always-on-top.

## Global Constraints

- Работать в `/home/dmitry/my_projects/solveo`; источник переноса `/home/dmitry/my_projects/screen-codex` не изменять.
- Репозиторий приватный. Не создавать worktree, коммиты, push или релизы без отдельного разрешения.
- Windows x64. Минимальная клиентская область 520x400; максимум очереди 20; максимум JSON чата 32 МиБ.
- Архив `solveo-data` рядом с EXE, WebView profile в LocalAppData; mutex на фактический archive directory.
- Сначала архивировать ход/изображения, затем запускать LLM; ответ сохранить до очистки исходников.
- Преднастройка и обычное сообщение — разные функции. Настройка не запускает модель.
- Реализовать только Codex; Claude Code, local runtimes и аудио остаются будущими возможностями.
- Все top-level окна получают WDA_EXCLUDEFROMCAPTURE до показа; главный Wails window начинает скрытым.
- Не показывать скрытое окно и не перехватывать фокус при получении ответа; always-on-top не отменяет hide.
- Пользователь проверяет реальные capture/Zoom сценарии. Cross-build не означает подтверждённый runtime.
- Node/vp/dev server нужны только разработчику; production UI embedded, без CDN/remote Markdown images.
- По явному указанию пользователя frontend использует только pnpm: pnpm-lock.yaml, установка через vp install --frozen-lockfile; без package-lock.json/yarn.lock.
- Go methods повторно проверяют состояние независимо от frontend; одна request/check/selection операция за раз.
- Тесты для изменённых правил и сбойных путей, без зеркальных тестов CSS/setters. Существующие полезные тесты переносить.
- После каждого задания оставлять проверяемый результат; команды commit из типовых workflows здесь не применять.

## Review Focus

- Быстрый capture при 19/20 снимках и сетевом сбое: не потерять снимок и не отправить чужой composer — Task 6.
- Глобальный submit во время IME, reload или задержки bridge: не отправить устаревший текст — Tasks 6/7.
- Отмена/timeout после принятого CLI-хода: сохранить неопределённость попытки, не запускать автоматический retry — Tasks 4/5.
- Повреждённый legacy chat и опасные PNG paths: открыть здоровую историю, не читать вне архива — Task 3.
- Закреплённое скрытое окно и открытый selector/viewer: не показать окно самовольно и не перекрыть инструмент — Tasks 2/10, Windows acceptance.

## Файлы и порядок зависимостей

Root: `go.mod`, `go.sum`, `wails.json`, `Makefile`, `.gitignore`,
`main_windows.go`, `main_bindings.go`, `assets_windows.go`, `app.go`.
`app.go` — Wails facade, без бизнес-логики. Windows main создаёт зависимости;
bindings main только вызывает Wails bindings generation, не открывает архив/окна.

Go packages: `internal/chat`, `internal/llm`, `internal/llm/codex`,
`internal/history`, `internal/settings`, `internal/app`, `internal/capture`,
`internal/instance`, `internal/platform/windows`.
Общие Event/ViewImage DTO лежат в portable `internal/platform/types.go`;
platform/windows реализует их без обратного импорта app. App не импортирует
Windows-only packages, поэтому его тесты работают в WSL.

Frontend: `frontend/src/app`, `frontend/src/bridge`, `frontend/src/chat`,
`frontend/src/history`, `frontend/src/settings`, `frontend/src/ui`;
генерируемые `frontend/wailsjs`. Не добавлять FSD/Redux/DI framework автоматически.

Зависимости: 1 → 2; 1 → 3 → 4 → 5 → 6 → 7 → 8/9; 2 + 6/9 → 10; все → 11.
Tasks 8 и 9 логически разделимы, но последовательное выполнение упрощает проверку.

## Task 1: Воспроизводимый Wails/Vite+ skeleton

**Files:** Create root files из карты; `frontend/` через vp; `build/` Wails metadata;
`internal/instance/lock_windows.go`, `lock_windows_test.go` из исходного проекта;
`internal/config/options.go`, `options_test.go` для существующих CLI-параметров.

**Interfaces:**
- Root `App` пока содержит только `Snapshot() BootSnapshot`; `BootSnapshot{Ready bool}` — временный binding smoke DTO, удаляется в Task 7.
- `instance.Acquire(dir string) (release func(), err error)` сохранить.
- `make bindings`, `make frontend-check`, `make frontend-test`, `make test`, `make vet`, `make build`, `make check`, `make dev`.

- [ ] Создать module `github.com/97Dmitry/solveo`; Wails закрепить на v2.17.0. Минимум Go определить из зависимостей, не копировать старый go 1.23: Wails go.mod требует 1.25.0, локально доступен go1.27.1.
- [ ] В root выполнить `vp create vite --directory frontend --package-manager pnpm --no-interactive --no-git --no-hooks --no-agent --no-editor -- --template react-ts`. Не использовать approve-builds вслепую; проверить полученные scripts/dependencies и закрепить pnpm/Node средствами Vite+. Один `pnpm-lock.yaml`.
- [ ] Проверить, что сгенерирован именно Vite+ toolchain, настроить `vp dev`, `vp build`, `vp check`, `vp test run`. Включить type checking; если нужны TS project references, добавить `typecheck` script с `tsc -b`, запускаемый через `vp run typecheck`.
- [ ] Создать `main_windows.go` с tag `windows && !bindings`, `assets_windows.go` с тем же tag, `main_bindings.go` с tag `bindings`, `app.go` с tag `windows || bindings`. Bindings main вызывает `wails.Run(options.App{Bind: []interface{}{&App{}}})`; runtime hooks не инициализируются. Assets embed только production build, поэтому bindings не требуют готового dist.
- [ ] `make bindings` выполняет host-native `go run -tags bindings .`. Не экспортировать GOOS=windows на этот target или его prerequisites. Проверить генерацию `frontend/wailsjs` без GTK/WebKit и без открытия GUI.
- [ ] Настроить wails.json: install `vp install --frozen-lockfile`, build `vp build`, dev watcher `vp dev --host 127.0.0.1`, serverUrl auto, frontend dir `frontend`, output `Solveo.exe`. Wails CLI той же версии запускать через pinned `go run .../cmd/wails@v2.17.0` либо локальный version-checked binary.
- [ ] В Makefile bindings предшествуют frontend typecheck/build. Windows build: Wails `build -platform windows/amd64 -skipbindings -s -o Solveo.exe` после явной frontend-сборки. Выход `build/bin/Solveo.exe`. `.DEFAULT_GOAL := help`; Windows env применяется только vet/build, не Go tests/bindings.
- [ ] Перенести mutex с тестами, исключив несогласованную смену namespace при чтении общей папки. Root main хранит release до выхода; ошибки не теряют HWND handles.
- [ ] Перенести параметры `-codex`, `-timeout`, `-prompt-file`, deprecated `-keep`; чтение и валидация prompt-file выполняются только при обычном запуске, не при bindings generation. `config.Parse(args []string) (Options,error)` использует отдельный FlagSet, сохраняет timeout fallback и не вызывает os.Exit из parser. Табличный test проверяет пустой prompt-file, timeout<=0 и custom binary; `go test ./internal/config` → PASS.
- [ ] Проверить: `make bindings`, `make frontend-check`, `make build`, `go test ./internal/instance` на Windows. Из WSL — `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o /tmp/solveo-instance.test.exe ./internal/instance`, без попытки выполнить PE.

**Результат:** frontend создан vp, bindings генерируются на host, Windows EXE собирается. Ни архива пользовательских данных, ни реальных LLM-запросов на этом шаге.

## Task 2: Windows host и безопасный lifecycle

**Files:** Create `internal/platform/windows/{host_windows,affinity_windows,tray_windows,hotkeys_windows,selection_windows,theme_windows}.go`,
`internal/platform/types.go` для общих Event/ViewImage,
`internal/capture/{capture_windows,region}.go`, `region_test.go`; modify `main_windows.go`.
Переносить из `internal/overlay` избирательно, не копировать прежний Window с RichEdit.

**Interfaces:**
- `Host.Start(ctx context.Context, onEvent func(Event)) error`, `Host.Close() error`.
- `Host.BeginSelection(operationID string) error`, `Host.ShowLog()`, `Host.ShowImages(images []ViewImage, selected int)` — последние два реализуются в Task 10.
- `Event{Kind, OperationID string; Region image.Rectangle; Err error}`; kinds для capture/add/full/send/toggle/exit/log/theme/pin и завершения selection.
- Event и ViewImage принадлежат portable package `platform`; Host callback и consumer NativeHost используют эти же типы, не копии и не types из Windows-only package.
- `EnsureExcluded(hwnd uintptr) error`; `FindMainWindow(className string, pid uint32) (uintptr,error)`.
- Перенесённые `capture.SavePrimary(path string) error`, `capture.SaveRegion(path string, region image.Rectangle) error`.

- [ ] Перенести region geometry tests и выполнить `go test ./internal/capture`; подтвердить прежние проверки нулевой/внешней области.
- [ ] Host создаёт скрытое top-level окно на одном `runtime.LockOSThread` goroutine; хранит callback pointers весь lifecycle; команды идут через posted messages. Не использовать message-only HWND для Explorer broadcast.
- [ ] Перенести tray/hotkeys/selection/theme, сохранить прежние Ctrl+Alt+S/A/F/Enter/O. Частичный startup failure снимает уже зарегистрированные ресурсы и завершается с ошибкой.
- [ ] При selection success отправлять OperationID и bounds после закрытия selector; отмена не создаёт PNG и не запускает анализ. Callback из host не выполняет файловый I/O/LLM под Win32 обработчиком.
- [ ] Главный Wails window StartHidden + ContentProtection + уникальный WindowClassName. В OnDomReady найти его по class/PID, проверить affinity, дождаться готовности frontend и tray, затем разрешить show. Не менять WndProc Wails.
- [ ] На shutdown остановить host loop и закрыть все его окна. TaskbarCreated восстанавливает tray; неудача даёт явный путь выхода. Крестик главного окна скрывает его.
- [ ] Проверить `make build`; на Windows пройти startup/tray/hotkeys/selection/hide/exit и отказ tray/affinity. Реальный capture path проверяет пользователь отдельно; недоступный runtime обозначить в отчёте, не выдумывать результат.

**Результат:** рабочая оконная инфраструктура без Codex. Если API v2 не позволяет lifecycle без private hooks, зафиксировать blocker до дальнейшего переноса.

## Task 3: Независимый чат и schema v2

**Files:** Create `internal/chat/{chat,config,message}.go`, `internal/history/{store,legacy,images}.go`,
`internal/settings/store.go` и соответствующие `*_test.go`.

**Interfaces:**
- `chat.Chat{ID,ProviderID,Title string; ProviderState json.RawMessage; Config Config; ConfigRevision uint64; Messages []Message; Attachments []Attachment; CreatedAt,UpdatedAt time.Time}`.
- `Config{Context,Instructions,ResponseLanguage,CodeLanguage,Detail,Model,Reasoning string}`; initial RU/auto/brief, Codex defaults empty.
- `Message{ID,Role,Text,Status string; AttachmentIDs []string; ConfigRevision uint64; Attempts []Attempt}`. `Attempt{ID,Status string; ConfigRevision uint64; Error string}`. Provider-specific параметры сверх Codex reasoning сейчас не вводить.
- `Attachment{ID,Path string}`; Path относительный в persisted state. `history.SaveInput{Chat chat.Chat; Sources []Source}`; `Source{AttachmentID,TempPath string}` — только internal, не Wails DTO.
- `history.Open(dir string) (*Store,error)`, `Store.Save(input SaveInput) (chat.Chat,error)`, `Load(id string) (chat.Chat,error)`, `List() ([]Summary,error)`, `ImagePath(chatID,attachmentID string) (string,error)`.
- `settings.Config{Theme string; FontSize int; AlwaysOnTop bool}`; `Load() (Config,error)` и `Save(Config) error` на Store из `settings.Open(dir string)`.

- [ ] Перенести старые storage tests под новые типы, добавить failing cases `TestPreparedChatWithoutSessionSurvivesRestart`, `TestLegacyImagesRemainChatLevel`, `TestSettingsRoundTrip`. Сначала `go test ./internal/chat ./internal/history ./internal/settings` → FAIL отсутствующих типов/функций.
- [ ] Реализовать модель и store с atomic JSON replace, PNG copy-before-commit, rollback partial copies и path validation. Sources не сериализуются. Лимит document 32<<20 bytes, schema=2; UUID для local chat не зависит от внешней session.
- [ ] Reader v1 сохраняет Role/Text (старые ключи с исходным case), CLI thread ID переносит в Codex ProviderState, attachments остаются chat-level. Миграция не меняет оригинал до явного Save; corrupted entries не скрывают здоровые.
- [ ] Legacy message/attachment IDs получать детерминированно из chat ID и индекса/path: повторный Load без Save возвращает те же ID. Отдельный test сравнивает IDs двух Load, не только текст.
- [ ] Проверки тестов: saved prepared chat имеет пустые Messages/ProviderState; legacy PNG не присвоен выдуманному message; `../../secret.png`, absolute Windows paths и symlink/reparse escape не читаются; old JSON остаётся прежним при провале второй PNG-copy; JSON >32 МиБ отклоняется до replace.
- [ ] Сохранить config revision и interrupted attempts после рестарта; общие настройки — отдельный settings.json. Тест принудительного сбоя записи через некорректную структуру каталогов/инъекцию writer, не через chmod, который ненадёжен под root.
- [ ] `go test ./internal/chat ./internal/history ./internal/settings` → PASS. Повторить native Windows history/settings tests при наличии среды.

**Результат:** подготовленный чат и старый архив читаются независимо от Codex/UI.

## Task 4: LLM boundary и единственный адаптер Codex

**Files:** Create `internal/llm/provider.go`; перенос `internal/codex/*` в `internal/llm/codex/` с существующими полезными тестами;
новые `provider_test.go`, `prompt_test.go`, `session_unix_test.go`.

**Interfaces:**
- `llm.Provider`: `Describe() Descriptor`, `Generate(context.Context, Request, func(Event)) (Result,error)`, `Check(context.Context, chat.Config, func(Event)) (CheckResult,error)`.
- `Descriptor{ID,Label string; Capabilities Capabilities}`; `Capabilities{Images,Streaming,Reasoning bool}`.
- `Request{History []chat.Message; Turn chat.Message; Config chat.Config; ConfigRevision uint64; State json.RawMessage; Images []Image}`; `Image{ID,Path string}` — trusted resolved paths only.
- `Result{Text string; State json.RawMessage}`; `Event{Kind,Text string}`, `CheckResult{Text string}`.
- `codex.New(binary,prompt string, timeout time.Duration) *Client`; `Client` реализует Provider, state JSON имеет валидируемый session ID и version.

- [ ] Сначала адаптировать tests и расширить fake CLI до start/resume с 0/1/N images. Запустить `go test ./internal/llm/...` → FAIL, пока нового контракта нет.
- [ ] До построения argv проверить `codex exec --help` и `codex exec resume --help` установленного CLI; это не authenticated request. Ни один test не обращается к настоящему аккаунту.
- [ ] Реализовать adapter поверх прежних process/session helpers; первый text-only запрос разрешён, resume принимает новые картинки. Не передавать history повторно в живую session; нет неявного restart/fallback.
- [ ] Формировать prompt из общей инструкции + config/revision + Turn.Text. Тест `TestPromptSeparatesContextAndUserText` проверяет наличие инструкции и вопроса, отсутствие coding-first навязывания и отсутствие передачи пользовательского текста в shell command.
- [ ] Fake CLI tests проверяют аргументы по отдельности: no ephemeral, sandbox read-only, ignore-user-config, ordering image flags, model/reasoning отдельны; changed config revision в следующем stdin, wrong returned session ID → ошибка.
- [ ] Проверить cancellation/timeout/missing final event/no answer, продолжить прежние parser chunking tests. Process cancellation на Windows сохраняет taskkill /T и WaitDelay; не заменять одним Process.Kill.
- [ ] Контракт partial result: при error адаптер может вернуть известный State вместе с Result, но не успешный Text. Ошибка с неопределённым внешним исходом оборачивает sentinel `llm.ErrOutcomeUnknown`, сохраняя errors.Is для context cancellation/timeout. Fake test подтверждает сохранение полученного session ID после отменённого первого хода и отсутствие автоповтора.
- [ ] Check не получает attachments или chat state и ограничен одной минутой. `go test ./internal/llm/...` → PASS, Windows vet adapter → PASS.

**Результат:** один реальный provider=codex. Ни Claude/local stubs в UI, ни STT interfaces не создавать.

## Task 5: Контроллер операции, сохранение и recovery

**Files:** Create `internal/app/{controller,operation,snapshot,persistence}.go`, `controller_test.go`, `persistence_test.go`.

**Interfaces:**
- `New(deps Dependencies) *Controller`; deps: Store, SettingsStore, Provider, Capturer, NativeHost, TempDir, Emit. Для tests использовать узкие consumer-owned interfaces по реально вызываемым методам, без DI framework.
- `Controller.Snapshot() Snapshot`, `NewChat() (Snapshot,error)`, `OpenChat(id string) (Snapshot,error)`, `SaveChatConfig(input ConfigInput) (Snapshot,error)`.
- `ConfigInput{ChatID,Title string; Config chat.Config; ExpectedRevision uint64}`.
- `Controller.Submit(input SubmitInput) (operationID string, err error)`, `Cancel(operationID string) error`, `RetrySave() error`, `DiscardUnsaved() (Snapshot,error)`, `Close(context.Context) error`.
- `SubmitInput{RequestID,IntentID,ChatID,Text string; DraftRevision uint64; AttachmentIDs []string}`.
- Snapshot включает Revision, ActiveChatID, summaries истории, config/messages/queue выбранного чата, operation/Controls/settings/provider descriptor. Полные документы остальных чатов и PNG bytes в нём нет. Errors: ErrBusy, ErrQueueFull, ErrStale, ErrEmpty, ErrSavePending.

- [ ] Написать failing tests с временным store и controlled fake Provider: `TestConfigSaveNeverCallsProvider`, `TestPersistsBeforeGenerate`, `TestLateCompletionUsesOperationChat`, `TestSaveFailureKeepsAnswerAndSources`. `go test ./internal/app` → FAIL.
- [ ] Мутации состояния сериализовать mutex; external I/O/callbacks выполнять вне lock. Операция получает неизменяемые chat/config/input IDs и собственный cancel context; completion проверяет operation ID. Не привязываться к current React chat.
- [ ] До Generate сохранить user turn и PNG. Успешный Result сохранить с provider state, затем cleanup только отправленных temp files. Save failure держит ответ/state/sources для RetrySave, блокирует submit/open/new. Capture/manual remove также не обходят recovery.
- [ ] Cancel отменяет только requesting, не saving; не удаляет уже полученный ответ. Failed/canceled attempt остаётся, повтор только явно, та же UI user message и новый Attempt.ID. Если после ошибки нет достоверной session state, следующий ход не выдавать за проверенное продолжение; показать это состояние.
- [ ] При partial Result+error из Task 4 сохранить известный provider state вместе с failed/canceled attempt; ErrOutcomeUnknown отразить в статусе до явного retry. Ошибка не должна превращаться в успешный assistant message или терять известный session ID.
- [ ] DiscardUnsaved создаёт новый local chat, сохраняет прежний on-disk archive, переносит необходимые pending sources в новую очередь и не продолжает затронутую CLI session. Проверить, что viewer получает archived IDs до temp cleanup.
- [ ] У assertions: ProviderCalls=0 при config save/предварительном storage failure; successful Generate до Save never удаляет PNG; повтор completion того же operation не добавляет второй assistant; после RetrySave ровно один ответ; stale cancel не отменяет новую операцию.
- [ ] `go test -race ./internal/app` → PASS. Race test здесь относится к реальному shared controller, не Win32 вызовам.

**Результат:** backend умеет весь text/image lifecycle и recovery без UI.

## Task 6: Черновики, очередь и общий путь capture/submit

**Files:** Create `internal/app/{drafts,capture,controls,intents}.go` и соответствующие tests;
подключить `internal/platform/windows` Events к controller.

**Interfaces:**
- `SetDraft(input DraftInput) error`; `DraftInput{ChatID,Text string; Revision uint64}`.
- `BeginCapture(mode string) error`; modes `quick-region`, `queue-region`, `quick-full`.
- `RemoveAttachment(chatID,id string) error`, `ClearQueue(chatID string) error`.
- `RequestSubmit() (SubmitIntent,error)`; `SubmitIntent{ID,ChatID string; DraftRevision uint64; ExpiresAt time.Time}`.
- Capturer использует перенесённые SavePrimary/SaveRegion; app генерирует trusted paths внутри TempDir, не принимает их из React.

- [ ] Failing tests: `TestQuickFailureWithNineteenAttachments`, `TestFullQueueRejectsBeforeSelection`, `TestQuickShotLeavesComposerUntouched`, `TestDraftIsolation`, `TestSubmitIntentExpires`. `go test ./internal/app` → FAIL для новых случаев.
- [ ] При queue=19 quick резервирует последнее место; при 20 не начинает selector. Busy включает selecting/capturing. Cancel selection освобождает резерв, не вызывает Provider. Quick отправляет только новый PNG; нормальный Submit фиксирует выбранные attachments и Text атомарно.
- [ ] На failure quick enqueue ровно один снимок, без duplicates; после success удаляются только отправленные. Порядок PNG соответствует queue, temp cleanup на Clear/Remove/Exit закрывает viewer удаляемого source.
- [ ] Черновики привязать к ChatID; stale revision игнорировать. Перед OpenChat bridge должен подтвердить latest SetDraft. In-memory drafts/unsent PNG живут до выхода, config — persisted.
- [ ] RequestSubmit emit intent; Submit принимает только подтверждённый current text с совпадающими ChatID/IntentID, неиспользованным RequestID и незавершённым timeout. Изменение выбора/reload инвалидирует intent. При timeout не отправлять cached text.
- [ ] Assertions: queue после quick failure =20, исходный draft text прежний; при queue=20 selectionCalls=0; duplicate RequestID → ProviderCalls=1; mismatched chat/expired intent → ProviderCalls=0. Использовать управляемые часы для timeout test, без sleep.
- [ ] `go test -race ./internal/app ./internal/capture` → PASS.

**Результат:** все кнопки и native хоткеи используют одни правила состояния.

## Task 7: Wails bridge и согласованное состояние React

**Files:** Modify root `app.go`, `main_windows.go`; create `frontend/src/bridge/{client,state}.ts`,
`frontend/src/bridge/state.test.ts`, `frontend/src/app/{App,AppProvider}.tsx`.

**Interfaces:**
- App exposes Controller methods из Tasks 5/6; Wails methods не принимают context/paths из JS.
- Events: `solveo:state` (полный Snapshot с Revision), `solveo:submit` (SubmitIntent), `solveo:log` (bounded batch).
- `useSolveo()` возвращает authoritative snapshot и typed bridge actions. DTO берутся из `frontend/wailsjs/go/models`, не переписываются вручную.

- [ ] Написать reducer/bridge tests: out-of-order snapshots, reload, active chat mismatch, IME submit и duplicate event. Запустить `vp test run src/bridge/state.test.ts` в frontend → FAIL.
- [ ] Подписаться на события до запроса начального Snapshot; принять только возрастающие revisions, удалить listeners при unmount. React StrictMode mount/unmount не создаёт повторную подписку или запрос.
- [ ] Последний текст editor держит в ref, изменения синхронизирует SetDraft. Обычная отправка передаёт current text явно; native intent также получает current text/ref. During composition не submit, после reload old intent не выполняется автоматически.
- [ ] RequestID генерируется один на пользовательское действие. Disabled controls отражают Go Controls, error returns показываются без создания новых optimistic messages. Frontend не управляет busy самостоятельно.
- [ ] `make bindings`, `make frontend-check`, `make frontend-test` → PASS. Tests assert current text sent, no stale cached fallback, subscription cleanup, no cross-chat draft. Удалить BootSnapshot из Task 1.

**Результат:** UI восстанавливается из Go state и не повторяет запрос при reload.

## Task 8: Читаемый чат и composer

**Files:** Create `frontend/src/chat/{ChatView,Message,Composer,AttachmentStrip,CodeBlock}.tsx`,
`frontend/src/ui/{Button,Drawer,Status}.tsx`, `frontend/src/app/theme.css`,
`frontend/src/chat/{markdown,composer}.test.tsx`; modify App.tsx.

**Interfaces:**
- Компоненты получают Snapshot/typed actions из useSolveo; нет прямых OS/CLI вызовов.
- Markdown rendering через react-markdown + remark-gfm, raw HTML выключен; локальная code highlighting библиотека подключается только после проверки поддерживаемых языков/размера bundle.

- [ ] Добавить failing interaction tests: `composer text-only send`, `Shift+Enter newline`, `IME no send`, `unsafe Markdown resources blocked`. Не писать тесты отступов и className.
- [ ] Создать аккуратные light/dark tokens, общий Button/Drawer, 520x400 layout из spec. Без дополнительной UI-framework зависимости только ради нескольких компонентов.
- [ ] Реализовать messages, Markdown, code copy, copy answer/latest code, text zoom, miniatures и single composer. Блоки кода scroll-x внутри, UI не расширяется от длинной строки.
- [ ] Disable raw HTML/remote images; external links не навигируют привилегированный документ. Attachments показываются по Go-generated local URLs. При новом ответе не сбрасывать scroll человека, читающего старый; показать переход вниз.
- [ ] Сохранять textarea после failed attempt и очищать только подтверждённый submitted draft; старый completion не очищает новый текст. Статусы различают requesting/saving/failed/save-pending; кнопка «Отмена» доступна только в cancellable phase.
- [ ] `make frontend-check`, `make frontend-test`, `make build` → PASS. На Windows проверить ввод/выделение/copy/resize/DPI/обе темы; скрытое окно не показывается от результата.

**Результат:** рабочий красивый text/image chat без настроек-пустышек.

## Task 9: Преднастройка, история и общие настройки

**Files:** Create `frontend/src/settings/{ChatSettings,AppSettings}.tsx`,
`frontend/src/history/HistoryDrawer.tsx`, `frontend/src/chat/ChatImages.tsx`,
`internal/app/settings.go`, tests для config/actions и form behavior.

**Interfaces:**
- `Controller.UpdateSettings(input settings.Config) (Snapshot,error)`.
- ChatSettings использует SaveChatConfig; история NewChat/OpenChat; проверка соединения `Controller.CheckConnection() (operationID string,error)`.
- Provider descriptor сейчас единственный Codex; reasoning показывается по capabilities.

- [ ] Failing tests: Save/Cancel config form, prepared chat remains after reopening, next turn sees config revision, Check leaves messages/queue/state unchanged.
- [ ] Развести отдельную форму преднастройки и composer. Подготовленный чат сохраняется до первого LLM-запроса. У model/reasoning labels не обещать значения из config.toml; состояние по умолчанию обозначить как выбор подключения.
- [ ] История показывает title/date/prepared state; создание нового не копирует старую настройку автоматически. На малой ширине только одна drawer, чат не превращается в узкую колонку.
- [ ] Реализовать общие theme/font-size настройки и live refresh native helpers через сообщения host. Check использует Provider.Check и общий operation slot, не состояние беседы.
- [ ] ProviderID Codex остаётся generic в архиве/UI; unknown provider archive открывается для чтения с понятным unavailable status, не переписывается на codex. Неработающие Claude/local пункты не добавлять.
- [ ] `go test ./internal/app ./internal/history ./internal/settings`, `make frontend-check`, `make frontend-test` → PASS.

**Результат:** подготовка интервью и обычный разговор независимы и сохраняются корректно.

## Task 10: Полный набор окон, лог и always-on-top

**Files:** Create/port `internal/platform/windows/{log_windows,image_windows,image_view,clipboard_windows}.go`,
`image_view_test.go`, `internal/app/{logs,images,window}.go`; modify AppSettings/App header и tray.

**Interfaces:**
- Реализовать ранее объявленные Host.ShowLog/ShowImages; `ViewImage{ID,Path,Label string}` — trusted internal DTO.
- `Controller.SetAlwaysOnTop(value bool) (Snapshot,error)`; root adapter применяет Wails WindowSetAlwaysOnTop, native helper flags меняет только host.
- `Controller.OpenImage(chatID,attachmentID string) error`, `ShowLog() error`.

- [ ] Перенести независимые image_view geometry tests; дописать failing app test `TestPinDoesNotShowWindow`, bounded log test и archived attachment rebinding test. Fake window adapter считает ShowCalls.
- [ ] Перенести отдельный лог без taskbar entry, bounded lines, cancel operation ID; после hiding main остаётся доступен из tray. Не передавать полные prompts/PNG/secrets в лог.
- [ ] Перенести native image viewer fit/1:1/zoom/pan/next/previous/Esc; HWND owned lifetime и routing не закрывает приложение. Перед temp cleanup заменить источники archived PNG, либо закрыть viewer при manual delete.
- [ ] Реализовать toggle pin в header и tray, persist settings.json, apply до первого show. Не вызывать show/focus из pin. Viewer/selector остаются доступны поверх pinned main; не менять Wails WndProc. Отказ сохранения настройки показать отдельно от изменения режима окна.
- [ ] `go test ./internal/app` и перенесённый portable image_view test → PASS, `make build` → PASS.
- [ ] Windows manual acceptance: включить pin → hide → получить ответ → окно остаётся скрытым; открыть → pin сохранён; region selector доступен; viewer/log работают; Explorer restart восстанавливает меню и доступный выход; перезапуск сохраняет pin.

**Результат:** функциональная полнота старой версии плюс закрепление главного окна.

## Task 11: Интеграционная проверка и передача Windows-сборки

**Files:** Finalize `Makefile`, `README.md`, `AGENTS.md`, `docs/windows-acceptance.md`;
при необходимости fixture-based tests `internal/app/integration_test.go`, `internal/history/testdata/legacy-chat.json`.

**Interfaces:** Финальные команды `make check` и `make dev`, build/bin/Solveo.exe.

- [ ] Пройти spec coverage: каждый §1–13 сопоставить с Tasks 1–10; исправить пропуски без новых несогласованных функций. AGENTS описывает native/Wails thread boundary, provider contract, storage lifecycle и vp команды, не копирует весь spec.
- [ ] Интеграционный fixture test: config save без LLM → text-only first turn → 2-image continuation → смена config → failure/retry → archive restart. Проверить один session state, порядок image IDs и отсутствие дублированного user turn.
- [ ] Отдельные fault fixtures: fail-save до Generate, fail-save после Result, interrupted restart, recovery в новый чат; источники PNG остаются доступны, previous JSON валиден. После успешного recovery нет auto-request.
- [ ] Запустить `make check`, `go test -race ./internal/app ./internal/llm/... ./internal/history ./internal/settings`, `git diff --check`. Для untracked файлов также проверить формат напрямую — diff не покрывает их до stage. Никакого git add ради этой проверки.
- [ ] Native Windows tests для mutex/history/platform и пользовательская acceptance checklist: startup, themes/DPI, text-only/config, screenshots/hotkeys, pin, cancel, archive/recovery, tray restart, viewer/log, missing Runtime. Runtime результаты в отчёте разделены: выполненные исполнителем и подтверждённые пользователем.
- [ ] README короткий: назначение, Codex/WebView2 prerequisites, vp/Wails build/dev, hotkeys, archive transfer, privacy. Указать, что Claude/local/audio пока не реализованы. Не добавлять release automation или публиковать артефакты без запроса.
- [ ] Передать локальный путь EXE и результаты проверок. Код остаётся uncommitted; список непроверенных Windows сценариев явный. Возможности аудио и иных адаптеров остаются в spec, без заглушек в production UI.

**Результат:** полная локальная реализация и проверяемая Windows-сборка, готовая к пользовательскому runtime-тесту.

## Самопроверка плана

- Spec coverage: преднастройка/чат Tasks 3/5/8/9; queue/capture 2/6; Windows 2/10; provider/audio boundary 4/9; архив 3/5; vp 1; acceptance 11.
- Межпакетные типы определены в Interfaces; internal paths не передаются через Wails.
- Future providers/audio не разбиты на текущие implementation tasks: реализуется только граница LLM и typed text-input path.
- Отдельный headless bindings entrypoint устраняет зависимость Windows cross-build от Linux GUI runtime; проверяется до переноса приложения.
- Все пять Review Focus имеют тест/ручной runtime шаг в owning task.
- Коммиты, remote CI и публикация не являются частью этого плана.

## Проверенные технические ссылки

- https://wails.io/docs/reference/project-config/
- https://wails.io/docs/reference/runtime/window/
- https://github.com/wailsapp/wails/blob/v2.17.0/v2/internal/app/app_bindings.go
- https://github.com/wailsapp/wails/blob/v2.17.0/v2/go.mod
- https://viteplus.dev/guide/create
- Installed `vp v1.3.0`: create/install/check/run help проверены при составлении плана.
