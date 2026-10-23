# STT providers: a uniform boundary and Grok as the second provider — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The application talks to speech recognition only through `stt.Provider`; the local Qwen worker and xAI Grok STT are two providers chosen in settings, and each provider owns its own readiness, preparation and connection.

**Architecture:** `internal/stt` gains `Provider`, `Description`, `Check` and the `Session` interface (today's `STTSession`). `internal/stt/docker` becomes `internal/stt/qwen` (provider = container manager + protocol v1 session). New `internal/stt/grok` implements the same interfaces over xAI's streaming WebSocket. The controller keeps one audio service; it asks the selected provider for checks, preparation and a session, and never knows Docker or xAI details. The UI renders checks and wording from the provider description.

**Tech Stack:** Go 1.27.1, `github.com/coder/websocket` v1.8.15 (already used), Windows Credential Manager through `golang.org/x/sys/windows` (`CredWriteW`/`CredReadW`/`CredDeleteW`), React/TypeScript/Zod.

**Spec:** user decisions of 2026-10-23: keep the application uniform, the provider solves its own problems, Grok is the second provider. xAI reference: [STT guide](https://docs.x.ai/developers/model-capabilities/audio/speech-to-text), [voice API](https://docs.x.ai/developers/rest-api-reference/inference/voice). Existing behavior: [audio MVP plan](2026-10-23-audio-mvp.md).

## Global Constraints

- One provider is active at a time; it is changed only while recognition is stopped. Default `qwen`; an unknown stored ID falls back to `qwen` on load.
- Cloud providers are visibly marked in the panel: «Облако: звук отправляется в xAI». Local stays «Локально: звук не покидает компьютер».
- The xAI key is a secret: stored only in Windows Credential Manager (target `Solveo/xai-stt`), never in settings.json, snapshots, logs, errors or the native log; the UI only knows `configured: true/false`.
- Grok streaming: `wss://api.x.ai/v1/stt`, `Authorization: Bearer <key>`, query `model=grok-voice-transcribe-2.0&encoding=pcm&sample_rate=<native>&interim_results=true&endpointing=800`, one `keyterm` per dictionary phrase; binary frames are PCM16 LE mono, 100 ms; `{"type":"Finalize"}` forces the utterance final; `{"type":"audio.done"}` ends a stream. Allowed sample rates 8000, 16000, 22050, 24000, 44100, 48000.
- Grok limits: ≤ 100 key terms, each ≤ 50 characters; a dictionary over the limit is an error shown for the source, never silently trimmed.
- The worker protocol, transcript rules, `SubmitTranscript` and capture are unchanged. Tests never reach xAI, Docker or audio devices.

## Review Focus

1. Grok closes a stream on its own (timeout, error): only that source shows the error; the other keeps listening (Task 3 `TestServerCloseIsStreamError`).
2. A device rate xAI does not accept (e.g. 96 kHz): the stream fails with a clear message instead of garbage audio (Task 3 `TestUnsupportedRateFailsStream`).
3. The key leaks into an error or log through a URL or header dump (Task 2 `TestKeyNeverInErrors`, Task 3 `TestDialErrorHidesKey`).
4. Switching provider while listening or while the model runs (Task 4 `TestProviderChangeRequiresStop`).
5. `speech_final` text either repeats locked chunks or contains only the tail: the final must equal the whole utterance without duplicates (Task 3 `TestFinalWithAndWithoutLockedPrefix`).

---

### Task 1: Provider boundary and the Qwen provider

**Files:** create `internal/stt/provider.go`; move `internal/stt/docker` → `internal/stt/qwen` (package `qwen`), add `qwen/provider.go`; modify `internal/app/audio.go`, `audio_test.go`, `audio_session_test.go`, `main_windows.go`, AGENTS files.

**Interfaces — produces (package `stt`):**
- `type Check struct{ ID, Label, State, Message string }` (states `ok|missing|error|unknown`, JSON snake case).
- `type Description struct{ ID, Label string; Local bool; Preparable bool; StartLabel, StopLabel string; MaxPhrases, MaxPhraseRunes int }`.
- `type Session interface{ Open(ctx, streamID, track string, phrases []string) error; Push(streamID string, rate, channels int, samples []float32) error; Flush(ctx, streamID string) error; CloseStream(streamID string) error; Events() <-chan any; Close() error }` — `*stt.Session` (protocol v1) satisfies it.
- `type Provider interface{ Describe() Description; Status(ctx) []Check; Prepare(ctx, progress func(string)) error; Start(ctx, progress func(string)) (Session, error); Stop(ctx) error }`.
- `qwen.New(models string, getenv func(string) string) *qwen.Provider` — Status = today's checks with labels; Start = container start + `stt.Dial`; Stop = container stop. Description: `{ID:"qwen", Label:"Qwen3-ASR (локально)", Local:true, Preparable:true, StartLabel:"Запустить модель", StopLabel:"Выгрузить модель", MaxPhrases:256, MaxPhraseRunes:128}`.

**Controller:** `AudioDeps{ Providers []stt.Provider; Capturer audio.Capturer }`; the active provider is `settings.Audio.Provider`. `AudioView` gains `provider stt.Description` and `providers []{id,label,local}`; `Checks` become `[]stt.Check`; `Model` keeps its states (meaning "connection" for cloud).

- [ ] Move tests with the package; existing app tests switch to a fake `stt.Provider` (runtime + session merged). Add `TestProviderStatusAndLabelsReachTheView`, `TestStartUsesSelectedProvider`.
- [ ] RED → implement → `go test -race ./internal/stt/... ./internal/app` and `make lint` GREEN. Commit `refactor(stt): introduce the provider boundary`.

### Task 2: xAI key storage

**Files:** create `internal/secrets/secrets.go` (`type Store interface{ Get(name) (string, bool, error); Set(name, value string) error; Delete(name string) error }`), `credman_windows.go` (Credential Manager, generic credential, `CRED_PERSIST_LOCAL_MACHINE`), `memory.go` (tests/other OS); tests.

- [ ] Tests: `TestMemoryStoreRoundTrip`, `TestKeyNeverInErrors` (errors carry the target name only), Windows-only `TestCredentialManagerRoundTrip` (writes and deletes `Solveo/test-<random>`).
- [ ] Controller methods `SetSTTKey(provider, key string) error` (trims, rejects empty/over 512 bytes, no network), `DeleteSTTKey(provider) error`; `AudioView.KeyConfigured bool`. Tests: `TestKeyIsNeverPublished` (snapshot, events and log never contain the key). Commit `feat(stt): store provider keys in Windows Credential Manager`.

### Task 3: Grok provider

**Files:** create `internal/stt/grok/{provider,session}.go`, tests with an `httptest` fake xAI server.

**Interfaces:** `grok.New(keys func() (string, bool, error)) *grok.Provider`; Description `{ID:"grok", Label:"Grok STT (облако xAI)", Local:false, Preparable:false, StartLabel:"Подключить", StopLabel:"Отключить", MaxPhrases:100, MaxPhraseRunes:50}`. Status: `key` (missing/ok), `network` — WebSocket handshake to `/v1/stt` closed immediately (401 → «Ключ отклонён xAI»; network error → «Нет доступа к api.x.ai»). Prepare = Status refresh. Start returns a session; Stop closes it.

Session: one WebSocket per stream, dialled on the first `Push` (the device rate is known only then) by a per-stream goroutine with a 50-frame queue (`ErrBackpressure` beyond); waits for `transcript.created`; frames are downmixed to mono and converted to PCM16 (no resampling). Event mapping per stream with utterance counter: first text of an utterance → `started`; `is_final=false` → `partial` with locked + interim text; `is_final=true, speech_final=false` → append to locked text, `partial`; `speech_final=true` → `final` with the whole utterance (do not duplicate when the event already starts with the locked text). `Flush` sends `Finalize` and waits up to 3 s for `speech_final`, returning nil on timeout; `CloseStream` sends `audio.done` and closes. `error` events and unexpected closes become `stt.StreamError` (message without the key); the session itself ends only on `Close`.

- [ ] Tests: `TestDialSendsKeyAndQuery` (all query parameters, one `keyterm` per phrase), `TestDictionaryOverLimitFailsOpen`, `TestFramesArePCM16MonoAtNativeRate`, `TestUnsupportedRateFailsStream`, `TestInterimAndLockedChunksBuildPartials`, `TestFinalWithAndWithoutLockedPrefix`, `TestFlushSendsFinalizeAndWaits`, `TestIdleFlushTimesOut`, `TestServerCloseIsStreamError`, `TestDialErrorHidesKey`, `TestBackpressure`.
- [ ] Commit `feat(stt): add the Grok streaming provider`.

### Task 4: Settings, bridge and panel

**Files:** `internal/settings/store.go` (`audio.provider`, default `qwen`), `app.go`, `frontend/src/bridge/{state,client,wails,test-fixture}.ts`, `frontend/src/audio/AudioPanel.tsx`, `frontend/src/settings/AppSettings.tsx`, tests.

- [ ] Go: `SetAudioProvider(id string) error` (rejects unknown IDs and changes while the model runs or listening: «Сначала остановите распознавание»), persisted through settings; `TestProviderChangeRequiresStop`, `TestUnknownStoredProviderFallsBack`.
- [ ] UI: provider selector in the panel (disabled while running), privacy line from `Local`, button texts from `StartLabel/StopLabel`, «Подготовить» only when `Preparable`; checks rendered with provider labels. Settings: «API-ключ xAI» password field with «Сохранить»/«Удалить» and the configured state; the dictionary hint shows the active provider's limits. Tests: `provider switch disabled while listening`, `cloud warning shown for grok`, `key field never shows the saved key`, `start label comes from the provider`.
- [ ] `make bindings`, frontend checks, commit `feat(ui): choose the speech recognition provider`.

### Task 5: Benchmark runner, docs, acceptance

- [ ] `tools/audio-bench/audio_bench/run_grok.py`: REST `POST https://api.x.ai/v1/stt` per corpus file with `keyterm` from the manifest, `XAI_API_KEY` from the environment, same JSONL schema (offline only, streaming fields empty) so `report.py` compares it with Qwen. Unit test of the multipart body with a fake HTTP server.
- [ ] README (provider choice, what goes to xAI, key storage, pricing link), `docs/windows-acceptance.md` (Grok section: key, connect, interview, error on revoked key, offline network), AGENTS updates, `make check`. Commit `docs: describe speech recognition providers`.
- [ ] With the user's key: run the benchmark on the 30 recordings and one live session; record results and the open protocol questions (locked chunk assembly, Finalize without speech, idle timeouts) in the evaluation report.

## Rulings against earlier documents

- The earlier plan's «нет облачного fallback, удалённых STT API и отправки звука» is replaced by the user's decision: a cloud provider is allowed when chosen explicitly and marked in the UI. There is still no automatic fallback between providers.
- Grok receives native-rate PCM16 mono converted in Go (downmix and sample format only, no resampling); rates outside xAI's list fail the stream.
