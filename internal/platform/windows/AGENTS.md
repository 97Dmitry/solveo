# Native Windows host

- `Host` owns a locked OS thread, hidden top-level host window, message loop, tray, hotkeys and helper windows. Route commands through `Host`; workers/controller must not manipulate helper HWNDs directly.
- Apply and verify `WDA_EXCLUDEFROMCAPTURE` before displaying helper windows; keep hidden on failure. The root Wails entrypoint coordinates main-window affinity/readiness.
- Route viewer/log messages before the host handler. Closing a helper must not exit the application. Preserve Escape with child focus and release GDI/pixel resources on destroy.
- Preserve Explorer tray recovery, hotkey unregistering and an accessible exit path. Main-window close hides; explicit Exit shuts down through the controller.
- The tray uses embedded transparent `trayicon.png`, independently of the EXE icon (`build/windows/icon.ico`). Its HICON is owned: remove the tray before destroying it on startup failure or shutdown; retain it across Explorer recovery. Wails does not regenerate an existing `icon.ico` when `build/appicon.png` changes, so update both application assets together.
- Selector/viewer must remain usable above a pinned main window. Pin changes never reveal a hidden window. Use the shared theme path.
- Acknowledge image rebind/close before source deletion. On failure retain source files and report the error; posting alone does not establish cleanup ordering.
- Verify message-loop, capture, tray, DPI, focus and pin behavior on Windows using `docs/windows-acceptance.md`. Portable geometry tests and cross-builds do not verify those behaviors.

- Initialize and verify shared GDI theme resources on the host thread before creating windows. A zero brush/pen makes background and owner-drawn button painting fail silently. `theme_windows_test.go` verifies actual fills when run on Windows.
- The main window is tray-only: `TrayOnly` (called in `OnDomReady`, before the first show and after capture affinity) sets `WS_EX_TOOLWINDOW`, clears `WS_EX_APPWINDOW` and removes the minimize box, then verifies the style. Wails sets `WS_EX_APPWINDOW` itself and has no option for this. Do not use a cross-thread owner window instead: it couples the Wails and host message queues.
