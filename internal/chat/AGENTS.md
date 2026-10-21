# Chat documents

- Config opt-in `ParallelAnswers` defaults false, including old archives. `Model`/`Reasoning` belong to the main request; `FastModel`/`FastReasoning` to the independent approach. Empty selections mean provider defaults, not a known fast model.
- A submitted user message freezes `RequestConfig` and `ConfigRevision`. Retry uses that snapshot, even after chat settings change. Legacy messages without it use current config on their first retry.
- Parallel user turns carry exactly two ordered Answers (approach, solution). Each block has an attempt ID, status, model/effort and elapsed CLI milliseconds. Main result controls the turn/attempt status; a complete approach does not complete the turn. Ordinary chats retain separate assistant messages.
- Never share Chat slices, raw provider state or config pointers between controller, workers and snapshots; use Clone at ownership boundaries. Persist UTC via Now.

- `Chat.GlobalInstructions` is the immutable creation-time snapshot supplied by app; missing archive fields mean empty. Keep chat independent of application settings. Retry uses the same snapshot plus the frozen RequestConfig.
