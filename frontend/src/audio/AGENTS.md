# Speech recognition panel

- `AudioPanel` renders Go's `AudioView` (readiness checks, model and listening controls, sources, transcript) and keeps only the phrase selection locally; selections of vanished or still-partial phrases are dropped on every render. It never decides readiness itself.
- Partial phrases are visible but not selectable. Отправить passes segment IDs in transcript order (`client.submitTranscript`, Go builds the text, the draft is untouched); В поле ввода joins the selected texts with newlines into the composer (`client.insertTranscript`). Nothing is sent automatically.
- The header «Распознавание речи» button opens a detached Wails renderer; no audio panel is mounted in the chat. AudioWindow reuses AudioPanel, polls validated parent state sequentially every 200 ms and shows the target chat. Closing hides the renderer without stopping recognition. The parent owns all state. Check window layout manually; jsdom does not lay out.
- Tests: `vp test run src/audio` plus the dictionary test in `src/settings/AppSettings.test.tsx`.
- Provider-specific wording comes from `audio.provider` (start/stop labels, key label, limits, `local`); the panel shows the privacy line, the provider selector (only while nothing runs) and the key form for providers that need a key. The saved key is never shown: the field is write-only and clears after saving.

- window-main.tsx/audio.html are the separate frontend entry. Child commands go only through bridge/wails.ts; send/insert carry the displayed ChatID. Insert is acknowledged by the main renderer to protect its live draft/IME. Do not add a second SolveoClient connection in AudioWindow.

- AudioWindow header toggles dockLeft/float locally in the child. Show docked state and window_error separately from STT errors; mode controls remain available while the main LLM is busy. Docking never takes space from the chat.

- AudioHistory loads the current chat timeline only on user action (forChat, no Start/Stop picker), reloads on ChatID changes, guards stale requests with an epoch, pages archive rows at 300 and passes readonly archive pages to AudioPanel. STT controls remain live; archive rows cannot send/insert/clear. onArchive(undefined) restores live mode on Эфир/deletion. Clearing live rows never deletes an archive.

- Legacy records are a collapsed fallback loaded on demand. Attach requires an explicit action naming the target chat; never guess ownership from date or source. Display the archive Notice even when no readable rows remain.
