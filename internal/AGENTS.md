# Solveo Go backend

- `app.Controller` serializes operation ownership, including selection and persistence. Only idle permits a new operation. Workers and snapshots must not share mutable slices or maps.
- Submit carries live text/revision. Acknowledge the draft atomically before launching; completion may clear only that revision. Keep quick capture independent of the ordinary draft.
- Persist input and PNG copies before provider calls; archive replies before source cleanup. Save failures retain answers and sources, block conflicting operations and expose explicit recovery. Retry saving never calls the provider; discard must preserve queued PNGs.
- History v2 uses local chat IDs and opaque provider state. Read legacy v1 without eager rewriting. Validate metadata before snapshots and skip malformed list entries. Confine paths with `os.Root`, including symlink/reparse escapes, and preserve atomic JSON replacement.
- Preserve image order and the 20-image limit. Reserve capacity before selection. Quick capture sends only the new image and queues it on failure.
- `llm.Provider` is application-owned; Codex-specific state stays in its adapter. Pass CLI arguments separately without shell interpolation, preserve read-only sandboxing and ignore user config. Retain partial state on uncertain outcomes; never silently restart a failed resume.
- Windows cancellation terminates the launcher process tree using `taskkill /T`, including `codex.cmd` children. Connection checks are explicit, bounded and independent of chat/queue state.
- After state/persistence/provider changes run `go test -race ./internal/app ./internal/history ./internal/settings ./internal/llm/...`. Portable suite: `go test ./internal/...`. Fake CLIs only. Windows vet/build: `make check`; this does not verify native runtime behavior.
