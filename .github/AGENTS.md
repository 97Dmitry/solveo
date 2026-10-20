# Continuous integration

- `workflows/ci.yml` runs on pull requests, pushes to `master` and manual dispatch (no duplicate push runs for PR branches). Linux checks frontend formatting/types/tests, Go race tests and `make lint` (golangci-lint version pinned in Makefile, host and Windows targets; `.tools` is cached by Makefile/go.mod hash). Windows runs native Go tests and builds the application in parallel with Linux to keep wall time short; both job names are required status checks on `master`.
- Use the official setup-vp action with working-directory frontend. It resolves the pinned Vite+ catalog and manages Node/pnpm. Install with --frozen-lockfile; do not introduce npm/yarn installs or manually regenerate the lockfile in CI.
- Pin third-party action references to commit SHAs and keep version comments accurate. Use read-only contents permission, timeouts and cancellation of superseded runs. No secrets, real Codex requests or pull_request_target execution.
- Keep the Windows Wails CLI version/build flags synchronized with Makefile. Generate bindings natively before compiling frontend; frontend output must exist before vet/build of the root application. Avoid requiring Make on the Windows runner.
- Upload only build/bin/Solveo.exe after successful checks, with finite retention. Never upload solveo-data, profiles or the entire workspace. This workflow does not publish Releases or verify recorder/UI behavior.
- Validate workflow syntax with actionlint and run the relevant local build/check commands. Keep CI maintenance details here rather than in the product README.
