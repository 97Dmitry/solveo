# Chat archive

- Keep archive attachment metadata as validated slash-separated relative paths; use `os.Root` for access and preserve atomic document replacement.
- `ImagePath` loads the chat and delegates to `AttachmentPath`, which resolves already loaded metadata without re-reading the document. Both return a native resolved path for providers/viewers. Resolve both the archive directory and the image before checking their relative path: Windows short names and directory aliases can change the spelling of the root. Preserve rejection of links escaping the archive.
- Cover path handling with `go test ./internal/history` and the native Windows CI suite. An archive reached through a directory alias must remain usable; external image links must remain rejected.
- Parallel answer blocks are additive fields in v2 user messages, with frozen request_config and independent attempt IDs/statuses. Preserve them during atomic saves; on load mark only requesting blocks interrupted, retaining completed approaches. Do not assume answers are always separate assistant messages. Run parallel_test.go for crash recovery coverage.

- `global_instructions` is additive in v2; missing means empty, never filled from current settings. Validate the 16384 code-point limit on load/save and preserve the snapshot on every write.
