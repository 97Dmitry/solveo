# Application controller

- Keep operation transitions and shared documents under the controller mutex; release it before I/O or provider work. Deep-copy documents transferred to workers.
- Model discovery is independent of chat operation ownership. Register discovery in the worker lifecycle under the same mutex used by Close, and use the controller context so shutdown cancels it. Discovery must not change active chat, drafts, queue or configuration.
- Preserve the persistence/recovery and live-draft contracts in the parent guide. Run the controller tests under the race detector after changing concurrency or lifecycle behavior.
- Use the phase/kind constants in `types.go` and `chat.Status*`/`chat.Role*`; the frontend validates these values with Zod enums.
- Streaming previews live in `stream.go`, outside chat documents and the archive. Each Generate gets a sink bound to operation, attempt and block; closeStream on its result (and on the approach when the solution succeeds) drops later updates. `solveo:stream` carries the full preview set, coalesced to 100 ms; `StreamSnapshot` serves renderer reloads. Status changes only from Generate results. Login state (`LoginView`) is owned by the controller and published in `Snapshot`; the adapter may report completion before `StartLogin` returns, so an early result is held until the pending view exists.
- CLI log output goes only to the native log window (`NativeHost.SetLog`). Do not add it to `Snapshot` or publish snapshots per log chunk.
- Resolve images of the active/just-saved chat with `Archive.AttachmentPath` from in-memory metadata; `ImagePath` re-reads the whole chat document and is only for chats not in memory.
- Error texts returned to the UI are Russian; the UI shows them verbatim.
- Parallel answers remain one serialized controller operation with two child Generate calls. Only runParallel writes its document/archive; drain both results before idle or save-pending. Checkpoint early results, cancel/drain on storage failure, retain main continuation state and never let approach state replace it.
- Parallel retry uses the user turn's frozen RequestConfig and starts only solution; approach keeps its earlier attempt/status. Main success cancels an unfinished approach and ignores its late text. Main status governs cleanup, draft acknowledgement and user-turn status.
- Standalone approach receives bounded recent text excluding earlier approaches; text-only follow-ups receive the latest prior image set. No new provider call or retry occurs on renderer reconnect/RetrySave.
