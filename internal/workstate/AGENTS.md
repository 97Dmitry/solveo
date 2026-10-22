# Unfinished work storage

- Owns `solveo-data/work`: `state.json` (version 1, per chat draft text/revision, ordered queue items, submit marker), `stream.json` (answer checkpoints) and `queue/<id>.png`. It never stores completed chat documents; those stay in `internal/history`.
- Queue operations use `os.Root(work)` and relative `queue/...` paths; never open a root on `queue` or follow a replaced queue directory. `Path`/`CapturePath` reject linked queues. `Has` returns `(bool, error)`; only a confirmed missing file returns false without error. All document writes use `storage.Replace`; IDs are validated before they form paths. Keep limits: draft 1 MiB, 20 queued images per chat, unique image IDs, 8 checkpoint blocks.
- Read the version envelope before decoding the v1 schema: a future version may change any payload field. `Load` sets a malformed file aside as `state.json.bad-<UTC>` and returns a notice; a newer version or unreadable file returns an error and the caller must not save over it. Missing files are empty state.
- Captures are written to `<id>.png.tmp` and `Commit`ted (Sync + rename). `Prune` removes unreferenced PNGs, unfinished captures and leftover `.solveo-*` temporaries; call it only after a successful `Load`.
- Run `go test -race ./internal/workstate ./internal/app` after changes.

- Draft `reply_to_id`/`reply_to_kind` are optional additive v1 fields; preserve reference-only drafts even when text is empty. Validate identity/kind shape here; app checks target existence after restoration and clears confirmed unavailable targets with a notice. An archive read error preserves the reference for later recovery.

- WriteImage receives validated PNG bytes and creates its own temporary file with O_EXCL through os.Root(work), then uses Commit. A failed exclusive open must never remove or follow somebody else's temporary file/link. Imported/reused images follow the same recovery and cleanup ownership as captures.
