# Audio capture

- `Capturer.Start` opens the default render endpoint in WASAPI loopback (`System`) or the default capture endpoint (`Mic`) in shared mode with the endpoint's mix format; device choice is not supported yet. Frames stay in the native rate/channels (≤ 100 ms, owned copies); the speech worker downmixes and resamples. No DSP here.
- Each source runs on its own locked OS thread with `CoInitializeEx(COINIT_MULTITHREADED)`; COM objects never leave that goroutine. `sink`/`fail` are called from it and must not block. `stop` is idempotent and waits for the thread.
- Loopback delivers no packets while nothing plays: `silenceFiller` adds zeros for gaps over 30 ms so the worker's VAD sees time pass. Never fill the microphone.
- Bindings are hand-written vtable calls (`call`): keep method indexes, GUIDs and the packed `WAVEFORMATEXTENSIBLE` layout (`SubFormat` at byte 24) exact. Only float32 and 16-bit PCM mix formats are accepted.
- Portable tests: `go test ./internal/audio`. Real devices: `GOOS=windows go test -c -o <windows-path>/audio.test.exe ./internal/audio`, run it on Windows (`TestCaptureStartsAndStops` skips without endpoints, e.g. on CI).
