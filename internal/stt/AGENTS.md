# Speech recognition client

- `provider.go` is the application boundary: `Provider` (Describe/Status/Prepare/Start/Stop) and `SessionAPI`. A provider owns everything specific to its recognizer (installation, keys, endpoints, wording of checks and buttons); the controller must stay generic. `*Session` (protocol v1) is the local worker's session.

- `stt` speaks protocol v1 to the `solveo-stt` worker (`internal/stt/worker`); `qwen` (`internal/stt/qwen`) is the local provider and owns the container. Keep the wire format in sync with `worker/solveo_stt/server.py`.
- All outgoing messages share one ordered queue, so `Flush`/`CloseStream` stay behind frames queued earlier. `Push` never blocks: with 50 frames (≈ 5 s) queued it returns `ErrBackpressure` without consuming a sequence number, so the worker never sees a gap; control messages wait for space instead of being dropped. `Open`/`Flush` wait for their answer at most 10 s.
- `Events` delivers `Update`, `StreamError`, then one `error` when the session ends, then closes. The reader blocks on a full buffer, so the consumer must drain it until it closes. `Close` drops the connection without a close handshake.
- Server messages are limited to 256 KiB. Tests use an `httptest` fake worker; never a real container. Run `go test -race ./internal/stt/...`.
