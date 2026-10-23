# Question card state machine

- Pure bounded state under the caller's mutex; no I/O, goroutines or scheduling here. Only new system finals trigger analysis. Microphone finals provide context; system partials block automatic send.
- Snapshot token invalidates late responses on edit/skip/navigation/disable; ordinary new finals remain a tail after the accepted snapshot. A saved manual version is frozen and bypasses freshness checks.
- Card owns copies of source text. Never depend on the live transcript or discard unprocessed conditions on overflow. Accepted versions persist with chat messages in the application.
- Validate model IDs against snapshot/current sources. No-question results advance the boundary and preserve an existing card. Tests: go test ./internal/questionbuilder.

- Card.Origin is automatic after an accepted analysis, manual after an edit; it persists with accepted submissions. Empty origin is legacy/unknown. ValidateSources caps cumulative evidence at 80 rows / 16000 Unicode code points; reject over-budget updates before advancing processed, preserving old card and the unprocessed tail. Manual salvage remains available.

- Draft is a completed analysis result awaiting continuation or explicit review, not a running request. CanConfirm requires a nonempty fresh ready=false card with no system partial/pending final, analysis, edit, error or gap. Confirm freezes exactly that version with manual readiness semantics, without changing Origin: unedited automatic text must retain source grounding. Late speech remains a tail; no timeout may invent readiness.
