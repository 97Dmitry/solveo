# Secrets

- Credentials never go to settings.json, snapshots, events, errors or logs. Production uses Windows Credential Manager (generic credential, `CRED_PERSIST_LOCAL_MACHINE`, current user); tests and other systems use `NewMemory`.
- Names follow `stt.KeyName(provider)` = `Solveo/stt-<provider>`. Errors name the target only; Delete of an absent secret succeeds.
- Tests: `go test ./internal/secrets`; the real store test is Windows-only (`GOOS=windows go test -c`, run the exe on Windows).
