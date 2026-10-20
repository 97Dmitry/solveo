# Solveo frontend

- Run frontend commands here. Use Vite+ with pnpm only; preserve `pnpm-lock.yaml` and the package manager version declared in `package.json`.
- Only `src/bridge/wails.ts` imports generated Wails APIs/runtime. Treat incoming events as `unknown`, validate with Zod and compiler-check schema keys against generated DTOs. Go owns business state.
- Preserve strict TypeScript and type-aware lint: no handwritten `any`, non-null assertions, unchecked wire casts or duplicated DTO contracts. Isolate generated code at the bridge. `erasableSyntaxOnly=false` accommodates generated Wails namespaces; keep all safety checks enabled.
- Regenerate `wailsjs/` using `make bindings` from the repository root after exposed Go contracts change. Never edit generated files by hand.
- Preserve monotonic snapshot revisions, subscription cleanup and renderer reload handling. Submit carries current text/revision; native intents are short-lived and single-use. Respect IME composition and retain text typed during requests.
- Preconfiguration saves locally without an LLM request. Ordinary text submission and quick capture are separate actions; quick capture preserves the existing draft and queued images.
- Markdown must not render raw HTML or remote images. Open validated external links through the desktop bridge. Preserve compact layouts at 520×400, scrolling, themes and font settings.
- Validate with `vp check`, `vp run typecheck`, `vp test run`, `vp run build`. Production uses the build script including `tsc -b`; `vp build` alone skips that script. Browser checks do not verify native Wails/Windows behavior.

<!--VITE PLUS START-->

# Using Vite+, the Unified Toolchain for the Web

This project is using Vite+, a unified toolchain built on top of Vite, Rolldown, Vitest, tsdown, Oxlint, Oxfmt, and Vite Task. Vite+ wraps runtime management, package management, and frontend tooling in a single global CLI called `vp`. Vite+ is distinct from Vite, and it invokes Vite through `vp dev` and `vp build`. Run `vp help` to print a list of commands and `vp <command> --help` for information about a specific command.

Docs are local at `node_modules/vite-plus/docs` or online at https://viteplus.dev/guide/.

## Built-in Commands vs Scripts

`vp <name>` runs a built-in command. `vp run <name>` runs a `package.json` script or a `vite.config.ts` task. Scripts cannot overwrite built-ins, so `vp dev` and `vp run dev` may do different things. Check `package.json` and `vite.config.ts` first, and run `vp run <name>` when the project defines a script or task with that name.

## Tool Versions

Run `vp toolchain` to show versions and relationships in the active Vite+
release. Add a tool name to select part of the graph. For example, run
`vp toolchain vite`. Use `--global` to ignore the local `vite-plus` package. Use
`vp why <package>` to show the package-manager dependency graph.

## Review Checklist

- [ ] Run `vp install` after pulling remote changes and before getting started.
- [ ] Run `vp check` and `vp test` to format, lint, type check and test changes.
- [ ] Check if there are `vite.config.ts` tasks or `package.json` scripts necessary for validation, run via `vp run <script>`.
- [ ] If setup, runtime, or package-manager behavior looks wrong, run `vp env doctor` and include its output when asking for help.

<!--VITE PLUS END-->
