# Codex adapter

- Requests use `codex exec` with separate arguments, `--ignore-user-config` and the read-only sandbox. Resume must preserve the exact session ID; uncertain failures retain partial state and never trigger automatic resubmission.
- Model discovery uses a separate short-lived `codex app-server` stdio connection: initialize, initialized, paginated model/list. It must never call thread/start or turn/start. Pin model_provider to openai to match the exec adapter. App-server does not support exec's --ignore-user-config flag and may return its cached/bundled catalog; do not claim a guaranteed fresh upstream list.
- Bound discovery time, output and pagination, preserve advertised effort order/defaults and reject malformed metadata. Do not return raw stderr/account data. Reuse subprocess configuration so Windows cancellation terminates launcher children.
- Tests use fake executable scripts. `go test ./internal/llm/codex` exercises discovery, arguments and session handling; the Python-backed discovery tests run only on non-Windows hosts.
