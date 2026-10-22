# Speech recognition client

- `stt` speaks protocol v1 to the `solveo-stt` worker (`internal/stt/worker`); `sttdocker` (`internal/stt/docker`) owns the container. Keep the wire format in sync with `worker/solveo_stt/server.py`.
- `Push` never blocks: a full queue (50 frames ≈ 5 s) returns `ErrBackpressure` without consuming a sequence number, so the worker never sees a gap. `Open`/`Flush` wait for their answer at most 10 s.
- `Events` delivers `Update`, `StreamError`, then one `error` when the session ends, then closes. The reader blocks on a full buffer, so the consumer must drain it until it closes. `Close` drops the connection without a close handshake.
- Server messages are limited to 256 KiB. Tests use an `httptest` fake worker; never a real container. Run `go test -race ./internal/stt/...`.
