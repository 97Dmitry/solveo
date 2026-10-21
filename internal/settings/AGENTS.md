# Application settings storage

- Settings JSON schema_version 1 stores actual font pixels in the range 14..30, default 17. Unversioned files use the old 12..28 range: add 2 on read and write version 1 on the next save. Never apply the offset to versioned files or in the frontend.
- Reject unsupported schema versions rather than overwriting future settings. Validate before saving and preserve atomic replacement through internal/storage.
- Keep migration compatibility covered by `go test ./internal/settings`.

- `ui_scale` is an integer percentage (80..150, default 100). Missing fields in older settings default to 100; explicit invalid values are rejected. Persist it in the same settings.json as theme/font/pin, not browser storage or the working directory.
- The removed `experimental_streaming` field of older files is ignored on load and dropped on the next save.

- `global_instructions` is additive in schema v1, default empty, at most 16384 Unicode code points (valid UTF-8). Saving it never calls the provider; existing chat snapshots never change. Go and Zod count code points, not bytes/UTF-16 units.
