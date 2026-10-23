# Application settings storage

- Settings JSON schema_version 1 stores actual font pixels in the range 14..30, default 17. Unversioned files use the old 12..28 range: add 2 on read and write version 1 on the next save. Never apply the offset to versioned files or in the frontend.
- Reject unsupported schema versions rather than overwriting future settings. Validate before saving and preserve atomic replacement through internal/storage.
- Keep migration compatibility covered by `go test ./internal/settings`.

- `ui_scale` is an integer percentage (80..150, default 100). Missing fields in older settings default to 100; explicit invalid values are rejected. Persist it in the same settings.json as theme/font/pin, not browser storage or the working directory.
- The removed `experimental_streaming` field of older files is ignored on load and dropped on the next save.

- `global_instructions` is additive in schema v1, default empty, at most 16384 Unicode code points (valid UTF-8). Saving it never calls the provider; existing chat snapshots never change. Go and Zod count code points, not bytes/UTF-16 units.

- `hotkeys` is additive in schema v1: a missing field means `hotkeys.Defaults()`, missing actions get defaults, unknown (newer) actions are ignored on load. Values are canonical `Ctrl+Alt+Shift+Win+Key` text or "" (disabled); Load/Save reject invalid or duplicate shortcuts. `Normalize` canonicalizes user input before `Validate`.

- `audio.context_phrases` is additive in schema v1 (missing → empty, never null on the wire): the speech recognition dictionary. `Normalize` trims and drops empty/duplicate phrases; `Validate` allows ≤ 256 phrases, ≤ 128 code points each, no NUL/invalid UTF-8, ≤ 3800 bytes joined (the worker caps the full context, template included, at 4 KiB). Saving it never calls STT or the LLM; it applies at the next listening start.

- audio.builder_model/builder_reasoning are additive schema-v1 model preferences, empty by default. Enabled/card/analysis state is never persisted in settings. Preserve these fields through Audio normalization and unrelated settings saves.
