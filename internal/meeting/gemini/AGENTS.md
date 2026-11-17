# Gemini file processing

- Files resumable upload + Interactions v1beta: gemini-3.5-transcribe (verbatim, speaker diarization/word timestamps, no vocabulary); gemini-3.8-flash for structured summary. Always store:false, no tools/chat instructions/provider state.
- Read the existing Solveo/stt-gemini key per request; key in header only. Reject redirects and upload URLs outside the configured API origin. Expose sanitized HTTP status errors, never response bodies.
- Journal remote file names (not URIs, text or credentials) for cleanup after errors/restarts. Cleanup uses a fresh bounded context; expired 404/410 files count as deleted. Do not claim deletion of all Google-side processing data.
- Test with httptest only: go test -race ./internal/meeting/gemini. Model access, quota and actual audio accuracy require explicit real-provider acceptance.
- Cleanup stops issuing DELETE requests after its context expires and preserves pending IDs. Caller presents cleanup failures as a separate deferred-cleanup Notice, never as the primary ASR/summary error.
- File API HTTP 429 and 503 map to meeting.RetryableError with bounded, structured Retry-After/RetryInfo parsing and optional daily QuotaFailure classification for 429. HTTP 503 is temporary service unavailability with a 15s default delay, not a credential/quota error. Never reflect vendor error.message. No automatic POST replay: manager pauses and user explicitly continues from archive checkpoints. DELETE 429 stays a separate cleanup warning.
