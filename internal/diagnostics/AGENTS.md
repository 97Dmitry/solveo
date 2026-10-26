# Session diagnostics log

- One `session-<UTC stamp>-<pid>.log` per main process in `solveo-data/logs` (`slog.TextHandler`, UTC times). The audio child never opens a log: the parent keeps its stderr tail (`Tail`) and receives renderer errors over IPC.
- Logging never blocks: records go through a bounded queue to one writer goroutine; overflow is counted and reported as `log records dropped`. Callers may log under their own mutexes, never wait for the writer.
- Size: after MaxBytes only errors pass (one marker record), after the reserve nothing; the `session end` record always passes and marks a clean exit for the next start. Retention keeps the newest Keep sessions, deleting only regular files that match the exact name pattern.
- Crash output (`debug.SetCrashOutput`) is process-global: only production passes `CrashOutput`; tests must not. An empty crash file is removed on Close; a non-empty one of the previous session becomes `PreviousCrash`.
- Content-free by default: callers log IDs, kinds, counts, lengths, durations and errors. Private text is logged only through the controller's `content` gate (settings `diagnostics.include_content`). `Redact` masks credential-like values in every string and error as a second line of defense; never rely on it alone.
- Archive writes `exports/diagnostics-<stamp>.zip` via a temporary name, with `summary.txt` and the newest `ArchiveSessions` sessions; it never includes chats, images, settings or keys.
- Tests: `go test -race ./internal/diagnostics`.
