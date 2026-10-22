# Speech recognition panel

- `AudioPanel` renders Go's `AudioView` (readiness checks, model and listening controls, sources, transcript) and keeps only the phrase selection locally; selections of vanished or still-partial phrases are dropped on every render. It never decides readiness itself.
- Partial phrases are visible but not selectable. Отправить passes segment IDs in transcript order (`client.submitTranscript`, Go builds the text, the draft is untouched); В поле ввода joins the selected texts with newlines into the composer (`client.insertTranscript`). Nothing is sent automatically.
- The panel opens from the header «Распознавание речи» button (red dot while listening) above the composer, limited to 45 % of the height with its own scrolling; check the compact 520×400 layout manually, jsdom does not lay out.
- Tests: `vp test run src/audio` plus the dictionary test in `src/settings/AppSettings.test.tsx`.
- Provider-specific wording comes from `audio.provider` (start/stop labels, key label, limits, `local`); the panel shows the privacy line, the provider selector (only while nothing runs) and the key form for providers that need a key. The saved key is never shown: the field is write-only and clears after saving.
