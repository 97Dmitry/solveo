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
  insert: (text: string) => void;
  selectProvider: (id: string) => void;
  setKey: (provider: string, key: string) => void;
  deleteKey: (provider: string) => void;
};
export const sourceLabel = { system: "Собеседник", mic: "Микрофон" } as const;
