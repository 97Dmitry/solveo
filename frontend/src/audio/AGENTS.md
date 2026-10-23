# Speech recognition panel

- `AudioPanel` renders Go's `AudioView` (readiness checks, model and listening controls, sources, transcript) and keeps only the phrase selection locally; selections of vanished or still-partial phrases are dropped on every render. It never decides readiness itself.
- Partial phrases are visible but not selectable. Отправить passes segment IDs in transcript order (`client.submitTranscript`, Go builds the text, the draft is untouched); В поле ввода joins the selected texts with newlines into the composer (`client.insertTranscript`). Nothing is sent automatically.
- The header «Распознавание речи» button opens a detached Wails renderer; no audio panel is mounted in the chat. AudioWindow reuses AudioPanel, polls validated parent state sequentially every 200 ms and shows the target chat. Closing hides the renderer without stopping recognition. The parent owns all state. Check window layout manually; jsdom does not lay out.
- Tests: `vp test run src/audio` plus the dictionary test in `src/settings/AppSettings.test.tsx`.
- Provider-specific wording comes from `audio.provider` (start/stop labels, key label, limits, `local`); the panel shows the privacy line, the provider selector (only while nothing runs) and the key form for providers that need a key. The saved key is never shown: the field is write-only and clears after saving.

- window-main.tsx/audio.html are the separate frontend entry. Child commands go only through bridge/wails.ts; send/insert carry the displayed ChatID. Insert is acknowledged by the main renderer to protect its live draft/IME. Do not add a second SolveoClient connection in AudioWindow.

- AudioWindow header toggles dockLeft/float locally in the child. Show docked state and window_error separately from STT errors; mode controls remain available while the main LLM is busy. Docking never takes space from the chat.

- AudioHistory loads the current chat timeline only on user action (forChat, no Start/Stop picker), reloads on ChatID changes, guards stale requests with an epoch, pages archive rows at 300 and passes readonly archive pages to AudioPanel. STT controls remain live; archive rows cannot send/insert/clear. onArchive(undefined) restores live mode on Эфир/deletion. Clearing live rows never deletes an archive.

- Only current-chat history is exposed; no legacy list, per-part picker or assignment controls. Display the archive Notice for unreadable current-format parts even when no readable rows remain.

- Archive text is the primary content: metadata (dates, counts, errors, submissions, devices, dictionary and deletion) stays under a collapsed «Метаданные» details. Show pagination only for multiple pages; readonly archive rows retain full text contrast.

- Interview layout keeps listening/stop controls and transcript visible. Provider/key/model/Docker checks live in a collapsed settings details; devices and window explanations have separate collapsed details. Toggle state must survive 200 ms parent polling. Current source/controller/history errors stay outside collapsed sections.

- QuestionCard stays above the transcript in the detached renderer; Go owns card readiness/freshness. Editing immediately freezes the parent card; local text is keyed only by ChatID so first-card ID assignment and polling cannot erase typed text. Save carries the text explicitly. Rebuilding a nonempty manual version requires explicit in-panel confirmation. Model/effort selectors use the provider catalog inside collapsed settings; there is no default model or automatic send.

- Show draft as «Черновик вопроса», not a running analysis. can_confirm is parent-owned and enables explicit «Подтвердить этот текст» without editing/submitting; only analyzing says «Собираю вопрос…». Pending system speech/analysis/error must never be locally promoted by a timer. Polling still owns the accepted confirmation result.
