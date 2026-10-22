# Local Qwen provider (solveo-stt container)

- `Manager` drives the docker CLI through `Runner` (`ExecRunner` hides the console window on Windows). Tests use a scripted fake runner; `manager_smoke_test.go` (build tag `dockersmoke`, env `SOLVEO_STT_MODELS`) is the opt-in real Docker/GPU check.
- Only a local engine: `DOCKER_HOST` unset or the context endpoint `npipe://`/`unix://`. Status never changes anything; GPU is checked only once the image exists.
- The image tag is `worker.Tag()` (content hash of the embedded build context), labelled `solveo.component=stt`; Prepare builds it from a tar of `worker.Context` on stdin and downloads the model into `ModelsDir/Qwen3-ASR-1.7B-7278e1e` through the image's `download` command (`REVISION` is written last and marks completion).
- The container is named `solveo-stt` and removed only when it carries Solveo's label; a foreign container of that name is an error and is never touched. No `--rm`: a container that dies while loading keeps its log for the error message; Start/Stop remove it.
- Start publishes only on 127.0.0.1 with a random port and a fresh 64-hex token, mounts the model read-only and waits up to 240 s for `/health`, failing fast with the last 30 log lines when the container stops.
- Run `go test -race ./internal/stt/qwen`.
