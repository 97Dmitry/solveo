// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vite-plus/test";
import { snapshotFixture } from "./test-fixture";
const native = vi.hoisted(() => ({
  importImage: vi.fn<(...args: string[]) => Promise<void>>(async () => {}),
  events: new Map<string, (data: unknown) => void>(),
}));
vi.mock("../../wailsjs/go/main/App", async () => {
  const { snapshotFixture } = await import("./test-fixture");
  return {
    RendererReady: async () => snapshotFixture(),
    Ready: async () => {},
    SetDraft: async () => {},
    Submit: async () => "operation",
    StreamSnapshot: async () => ({ revision: 0, blocks: [] }),
    ImportAttachment: native.importImage,
  };
});
vi.mock("../../wailsjs/runtime/runtime", () => ({
  EventsOn: (name: string, cb: (data: unknown) => void) => {
    native.events.set(name, cb);
    return () => {
      native.events.delete(name);
    };
  },
  ClipboardSetText: async () => true,
  BrowserOpenURL: () => {},
}));
import { actions, client } from "./wails";
afterEach(() => vi.restoreAllMocks());
it("keeps original chat ID during asynchronous file reading and blocks a second import", async () => {
  const disconnect = client.connect();
  try {
    await vi.waitFor(() => expect(client.getState().status).toBe("ready"));
    const readers: FileReader[] = [];
    vi.spyOn(FileReader.prototype, "readAsDataURL").mockImplementation(function (this: FileReader) {
      readers.push(this);
    });
    const imported = actions.importImage("first", new File(["data"], "one.png"));
    expect(await actions.importImage("other", new File(["data"], "two.png"))).toBe(false);
    native.events.get("solveo:state")?.({
      ...snapshotFixture(),
      revision: 999,
      active_chat_id: "other",
    });
    const reader = readers[0];
    if (!reader) throw new Error("FileReader was not started");
    Object.defineProperty(reader, "result", { value: "data:image/png;base64,ZGF0YQ==" });
    reader.dispatchEvent(new Event("load"));
    expect(await imported).toBe(true);
    expect(native.importImage).toHaveBeenCalledExactlyOnceWith("first", "one.png", "ZGF0YQ==");
  } finally {
    disconnect();
  }
});
