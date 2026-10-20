# Continuous integration

- `workflows/ci.yml` runs on push, pull requests and manual dispatch. Linux checks frontend formatting/types/tests and Go race tests; Windows runs native Go tests and builds the application only after Linux succeeds.
- Use the official setup-vp action with working-directory frontend. It resolves the pinned Vite+ catalog and manages Node/pnpm. Install with --frozen-lockfile; do not introduce npm/yarn installs or manually regenerate the lockfile in CI.
- Pin third-party action references to commit SHAs and keep version comments accurate. Use read-only contents permission, timeouts and cancellation of superseded runs. No secrets, real Codex requests or pull_request_target execution.
- Keep the Windows Wails CLI version/build flags synchronized with Makefile. Generate bindings natively before compiling frontend; frontend output must exist before vet/build of the root application. Avoid requiring Make on the Windows runner.
- Upload only build/bin/Solveo.exe after successful checks, with finite retention. Never upload solveo-data, profiles or the entire workspace. This workflow does not publish Releases or verify recorder/UI behavior.
- Validate workflow syntax with actionlint and run the relevant local build/check commands. Keep CI maintenance details here rather than in the product README.
