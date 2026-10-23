import type { AudioState } from "../bridge/state";
import { sourceLabel } from "./actions";

// Current window, controller, source and history failures stay visible above every tab.
export function AudioAlerts({ audio, window }: { audio: AudioState; window: string }) {
  const sources = audio.sources.filter((source) => source.error);
  if (!window && !audio.error && !audio.history_error && sources.length === 0) return null;
  return (
    <div className="audio-alerts">
      {window && (
        <p className="audio-error" role="alert">
          {window}
        </p>
      )}
      {audio.error && (
        <p className="audio-error" role="alert">
          {audio.error}
        </p>
      )}
      {sources.map((source) => (
        <p className="audio-error" role="alert" key={source.track}>
          {sourceLabel[source.track]}: {source.error}
        </p>
      ))}
      {audio.history_error && (
        <p className="audio-error" role="alert">
          История: {audio.history_error}
        </p>
      )}
    </div>
  );
}
