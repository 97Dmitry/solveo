# Speech recognition panel

- `AudioPanel` renders Go's `AudioView` (readiness checks, model and listening controls, sources, transcript) and keeps only the phrase selection locally; selections of vanished or still-partial phrases are dropped on every render. It never decides readiness itself.
- Partial phrases are visible but not selectable. Отправить passes segment IDs in transcript order (`client.submitTranscript`, Go builds the text, the draft is untouched); В поле ввода joins the selected texts with newlines into the composer (`client.insertTranscript`). Nothing is sent automatically.
- The panel opens from the header «Распознавание речи» button (red dot while listening) above the composer, limited to 45 % of the height with its own scrolling; check the compact 520×400 layout manually, jsdom does not lay out.
- Tests: `vp test run src/audio` plus the dictionary test in `src/settings/AppSettings.test.tsx`.
