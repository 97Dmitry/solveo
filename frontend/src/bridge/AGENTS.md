# Wails bridge

- Only `wails.ts` imports generated Wails APIs/runtime. `state.ts` validates unknown input and compiler-checks schema keys against generated Go DTOs; handwritten UI code consumes inferred validated types.
- Reasoning effort IDs come from the provider catalog, not a static enum. Validate token shape, then enforce model/effort membership in the settings form. Validate catalog results before exposing them to components.
- `client.ts` owns renderer subscriptions, snapshot revisions, live editor revisions and submit intents. Preserve cleanup, IME handling and single-use intent checks; never send a stale backend draft as a fallback.
- Regenerate bindings from the repository root with `make bindings` when exposed Go types/methods change. Do not hand-edit `wailsjs/`.
- `solveo:stream` previews are validated by `streamSchema`, accepted only with a newer revision and re-read via `StreamSnapshot` after connect. `streamText` shows a preview only for the active chat, current operation and still-requesting attempt; parallel blocks use their own status, since the solution may fail while the approach still streams.
- `Snapshot.work_error` is a persistent banner separate from `error`; `Snapshot.recovered` is already filtered by Go to interrupted attempts of the active chat.
- Additive optional Message.answers/request_config fields support old archives. Wire explicitly permits undefined for optional generated fields to match Zod output under exactOptionalPropertyTypes; required fields remain required. Answer statuses/kinds/timings are validated before presentation.

- settings.global_instructions is required on the current wire; globalInstructionsSchema accepts at most 16384 Unicode code points and rejects lone UTF-16 surrogates. Older disk files are normalized by Go, not the renderer.

- SolveoClient preserves replyToID/replyToKind with the live editor revision through SetDraft/Submit and snapshots. prepareFollowup/reuseQuestion require an empty editor, no reference, idle submit eligibility and no IME; never replace unfinished work. submitting allows edits while the Submit RPC acknowledgement is pending; other run actions still lock edits to protect navigation.

- Profiles are required Snapshot fields (`profiles`, `profiles_error`) validated against generated ProfileView. Config's additive interview strings are normalized by Go when reading old archives. Profile commands use client.run; NewChatFromProfile flushes the live draft before navigation just like NewChat. Keep stored-profile creation separate from applying the editable form.

- Image imports capture chatID before async FileReader; read inside client.run so navigation/duplicate imports remain serialized. attachments.ts bounds File.size before base64, while Go validates bytes/format/dimensions. Do not pass renderer paths to Go. Reuse and reorder use confirmed Go snapshots; preserve the live text/reference.
