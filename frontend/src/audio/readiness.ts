import type { AudioState } from "../bridge/state";

// Whether every readiness check passed; the list itself comes from Go.
export const checksPassed = (audio: AudioState) =>
  audio.checks.length > 0 && audio.checks.every((c) => c.state === "ok");
// The recognizer cannot be started from the header and needs the settings tab.
export const needsSetup = (audio: AudioState) =>
  audio.available &&
  audio.busy === "" &&
  audio.model !== "ready" &&
  audio.checks.length > 0 &&
  !checksPassed(audio);
