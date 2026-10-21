# Codex app-server connection

- `Conn` owns one `codex app-server` stdio process: one stdout reader, serialized stdin writes bounded by the caller's context (a write that does not finish kills the process: Windows pipes have no deadlines, and `Close` never waits behind it), monotonically increasing request IDs and a pending table that is closed on failure. Never copy a `Conn`; share it by pointer.
- Notifications route by `threadId` to the single awaited turn of that thread; other turn IDs (late events of an earlier turn) are ignored and bounded. Notifications may arrive before the `turn/start` response.
- Limits: 8 MiB per line, 1 MiB per answer item. Malformed JSON or an oversized line fails the connection and kills the process: a lost `turn/completed` must not leave a turn waiting forever.
- Outcomes: only `turn/completed` ends a turn. The final text is the completed `final_answer` item, else the last completed non-commentary item; deltas are previews only and `commentary` is never shown. A lost connection, unconfirmed interrupt or failed `turn/start` transport is `llm.ErrOutcomeUnknown`; a confirmed interrupt returns `ctx.Err()`. Cancelling one turn sends `turn/interrupt` and normally keeps the process.
- `turn/start` on a thread with an active turn adds input to that turn. Therefore `turn/start` always waits for its turn ID (cancellation becomes an interrupt), and an unanswered `turn/start` or unconfirmed interrupt kills the connection: neighbouring turns become unknown instead of a stale turn absorbing the next question.
- Server requests (approvals) are refused: Solveo runs read-only with approval `never`.
- Tests use the scripted fake in `appservertest` by re-executing the test binary (works on Windows). Run `go test -race ./internal/llm/codex/...`.

- The fake persists developerInstructions per thread and explicit user-turn preference updates across process restarts; `[show-instructions]` returns effective preferences without a real model. Keep resume from silently overriding loaded instructions.
