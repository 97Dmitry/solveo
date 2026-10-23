// @vitest-environment jsdom
import { expect, it, vi } from "vite-plus/test";
const state = vi.hoisted(() => ({
  listener: undefined as ((v: unknown) => void) | undefined,
  ack: vi.fn(),
}));
vi.mock("../../wailsjs/runtime/runtime", async (original) => {
  const actual = await original<typeof import("../../wailsjs/runtime/runtime")>();
  return {
    ...actual,
    EventsOn: (_name: string, cb: (v: unknown) => void) => {
      state.listener = cb;
      return () => {
        state.listener = undefined;
      };
    },
  };
});
vi.mock("../../wailsjs/go/main/App", async (original) => {
  const actual = await original<typeof import("../../wailsjs/go/main/App")>();
  return { ...actual, CompleteAudioInsert: state.ack };
});
import { onAudioInsert } from "./wails";
it("acknowledges a live insertion and refuses expired requests", () => {
  const insert = vi.fn(() => true);
  const off = onAudioInsert(insert);
  const event = { id: "request", chat_id: "chat", text: "fresh", expires_at: Date.now() + 2000 };
  state.listener?.(event);
  expect(insert).toHaveBeenCalledWith("chat", "fresh");
  expect(state.ack).toHaveBeenLastCalledWith("request", "");
  insert.mockClear();
  state.listener?.({ ...event, id: "expired", expires_at: Date.now() - 1 });
  expect(insert).not.toHaveBeenCalled();
  expect(state.ack).toHaveBeenLastCalledWith("expired", expect.stringContaining("Не удалось"));
  off();
  expect(state.listener).toBeUndefined();
});
