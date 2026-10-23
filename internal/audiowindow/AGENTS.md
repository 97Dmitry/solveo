# Detached audio renderer transport

- Parent owns all business state. The child renderer uses only the loopback endpoint; never open history/settings/STT in the child.
- Bind only 127.0.0.1, random per-session Bearer token via stdin; no tokens in argv/env/URLs/logs. Reject browser origins, limit bodies, allowlist commands. Client disables proxies and redirects and accepts loopback endpoints only.
- State contains AudioView and minimal target/theme metadata; never full chats, credentials or provider state. Commands carry the displayed ChatID for send/insert.
- Insert is acknowledged by the main renderer, which owns live/IME draft state. Pending acknowledgements are bounded by context and removed on every exit path.
- Tests: go test -race ./internal/audiowindow. No real windows/providers in portable tests.

- /health carries monotonic show_version; child Go polling must deliver reveal even while hidden WebView JS timers are throttled. ChildEnvironment removes inherited Wails development server settings; Assets maps index.html to audio.html and preserves shared chunks.

- DockState/LeftDock own only presentation state and physical geometry. Dock/float commands are handled in the child, never sent to the parent controller. Manual close and automatic hide must remain distinct. ClosePolicy requires HideWindowOnClose=false so Wails actually calls OnBeforeClose; explicit parent-loss exit must BeginQuit first.

- Explicit authenticated /chat-history/<ChatID> GETs read archived text through the parent controller. They are separate from frequent state polling, with bounded 64 MiB detail reads. Only deleteChatHistory is exposed for history mutation; no per-part browsing or assignment APIs. Delete is an explicit parent command; the child never opens archive files. History failure must not terminate the renderer or affect live STT.
