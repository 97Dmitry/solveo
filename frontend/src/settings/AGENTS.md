# Settings forms

- Preconfiguration is a local, explicit save. Never call Generate/Submit when loading or saving this form.
- Load model metadata through the injected `loadModels` bridge function. Keep the returned effort order and only offer efforts for the selected model; changing models resets the effort to its default. Empty strings mean provider defaults.
- Preserve saved values and context when discovery fails; show the failure and allow retry. Reject obsolete selections after successful discovery. Ignore async results after unmount.
- Language selectors preserve existing custom values from older chats. Do not silently replace saved configuration on opening the form.
- Font sizes are actual CSS pixels. Backend settings migrate the old range once; do not add frontend offsets. The slider commits on pointer/key release or blur.
- Validate with `vp test run src/settings/ChatSettings.test.tsx` and the frontend checks described in the parent guide.

- Interface scale is independent of text size and persisted by Go as ui_scale. Apply it to the document so layout and modal dialogs scale together; keep compact-window settings reachable across 80..150%. Do not use browser storage for settings.
