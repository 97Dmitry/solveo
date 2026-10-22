# Settings forms

- Preconfiguration is a local, explicit save. Never call Generate/Submit when loading or saving this form.
- Load model metadata through the injected `loadModels` bridge function. Keep the returned effort order and only offer efforts for the selected model; changing models resets the effort to its default. Empty strings mean provider defaults.
- Preserve saved values and context when discovery fails; show the failure and allow retry. Reject obsolete selections after successful discovery. Ignore async results after unmount.
- Language selectors preserve existing custom values from older chats. Do not silently replace saved configuration on opening the form.
- Font sizes are actual CSS pixels. Backend settings migrate the old range once; do not add frontend offsets. The slider commits on pointer/key release or blur.
- Validate with `vp test run src/settings/ChatSettings.test.tsx` and the frontend checks described in the parent guide.

- Interface scale is independent of text size and persisted by Go as ui_scale. Apply it to the document so layout and modal dialogs scale together; keep compact-window settings reachable across 80..150%. Do not use browser storage for settings.
- Parallel answers are explicitly opt-in and cost two requests. Main and fast model/effort selectors use the same discovered catalog independently; changing either model resets only its effort. Validate fast selections only while enabled; preserve them when disabling the mode.
- Connection settings always show the Solveo Codex account: status loads through the injected `loadAccount` in an effect keyed by login state; the device-code prompt (URL, code, state, error) comes from Go's `Snapshot.login`, links open through `onOpenLink`.

- Global instructions have an explicit save button and “Применяется к новым чатам” help text. They affect no existing chat, including empty chats. Preserve unsaved text across unrelated settings snapshots; validate with globalInstructionsSchema before sending.

- ProfileControls manages a separate library via injected actions; save/update take current form fields without SaveChatConfig, while NewChatFromProfile takes the saved ID/revision. Selecting a profile immediately fills the form; no separate load action. Track field edits against the last loaded baseline, so switching unchanged profiles needs no confirmation. Cancelled replacement preserves the previous selection/name/revision and text. Profile selection never changes the chat title; navigation separately protects an edited title. Confirm dirty-field replacement and deletion inline; keep unsaved text after storage/catalog errors and ignore completion after unmount. Library operations require can_switch (also available in legacy read-only chats); applying form settings still requires can_edit_config. Never add sample experience or persist the library in browser storage.

- HotkeySettings records physical keys (`KeyboardEvent.code`) so shortcuts are layout-independent; it pauses global hotkeys for the whole recording (one suspend/resume pair) and resumes on blur/unmount. Local duplicate detection mirrors Go; Go remains authoritative for conflicts with other applications (`hotkey_errors`). Defaults come from Go `DefaultHotkeys`, labels match `internal/hotkeys`.
