# Wails bridge

- Only `wails.ts` imports generated Wails APIs/runtime. `state.ts` validates unknown input and compiler-checks schema keys against generated Go DTOs; handwritten UI code consumes inferred validated types.
- Reasoning effort IDs come from the provider catalog, not a static enum. Validate token shape, then enforce model/effort membership in the settings form. Validate catalog results before exposing them to components.
- `client.ts` owns renderer subscriptions, snapshot revisions, live editor revisions and submit intents. Preserve cleanup, IME handling and single-use intent checks; never send a stale backend draft as a fallback.
- Regenerate bindings from the repository root with `make bindings` when exposed Go types/methods change. Do not hand-edit `wailsjs/`.
- `solveo:stream` previews are validated by `streamSchema`, accepted only with a newer revision and re-read via `StreamSnapshot` after connect. `streamText` shows a preview only for the active chat, current operation and still-requesting attempt; parallel blocks use their own status, since the solution may fail while the approach still streams.
- Additive optional Message.answers/request_config fields support old archives. Wire explicitly permits undefined for optional generated fields to match Zod output under exactOptionalPropertyTypes; required fields remain required. Answer statuses/kinds/timings are validated before presentation.
