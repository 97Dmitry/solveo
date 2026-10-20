# Application controller

- Keep operation transitions and shared documents under the controller mutex; release it before I/O or provider work. Deep-copy documents transferred to workers.
- Model discovery is independent of chat operation ownership. Register discovery in the worker lifecycle under the same mutex used by Close, and use the controller context so shutdown cancels it. Discovery must not change active chat, drafts, queue or configuration.
- Preserve the persistence/recovery and live-draft contracts in the parent guide. Run the controller tests under the race detector after changing concurrency or lifecycle behavior.
- Use the phase/kind constants in `types.go` and `chat.Status*`/`chat.Role*`; the frontend validates these values with Zod enums.
- CLI log output goes only to the native log window (`NativeHost.SetLog`). Do not add it to `Snapshot` or publish snapshots per log chunk.
- Resolve images of the active/just-saved chat with `Archive.AttachmentPath` from in-memory metadata; `ImagePath` re-reads the whole chat document and is only for chats not in memory.
- Error texts returned to the UI are Russian; the UI shows them verbatim.
