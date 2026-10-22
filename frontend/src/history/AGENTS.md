# History panel

- Go owns order (pinned first), search, deletion and export; the panel only renders validated snapshots and injected actions.
- Search is debounced (250 ms) and ignores answers for older queries; results refresh when history changes. Hits for chats no longer in history are hidden.
- Deletion always goes through one inline confirmation, also for a single chat; the active chat is allowed (Go switches first). Show the per-chat report from DeleteResult; do not assume all-or-nothing.
- Export shows the absolute path and opens the folder only by export name through Go. Run `vp test run src/history`.
