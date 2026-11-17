// Commands the detached window forwards to the parent; Go owns every resulting state.
export type AudioActions = {
  refresh: () => void;
  prepare: () => void;
  startModel: () => void;
  stopModel: () => void;
  startListening: () => void;
  stopListening: () => void;
  clear: () => void;
  send: (segmentIDs: string[]) => void;
  // sendUnsent sends every interviewer phrase after the last sent one; Go waits for a phrase in progress.
  sendUnsent: () => void;
  insert: (text: string) => void;
  selectProvider: (id: string) => void;
  setKey: (provider: string, key: string) => void;
  deleteKey: (provider: string) => void;
  setDictionary: (phrases: string[]) => void;
};
export const sourceLabel = { system: "Собеседник", mic: "Микрофон" } as const;

// unsentCount counts interviewer phrases after the last sent phrase, like Go's «send new phrases».
export function unsentCount(
  segments: readonly { track: string; sent?: boolean | undefined }[],
): number {
  let start = 0;
  segments.forEach((s, i) => {
    if (s.sent === true) start = i + 1;
  });
  return segments.slice(start).filter((s) => s.track === "system").length;
}
