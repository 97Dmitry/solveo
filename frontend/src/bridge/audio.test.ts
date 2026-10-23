import { describe, expect, it, vi } from "vite-plus/test";
import { SolveoClient, type Transport } from "./client";
import { audioFixture, snapshotFixture } from "./test-fixture";
function setup(audio: unknown = audioFixture()) {
  const listeners = new Map<string, (data: unknown) => void>();
  const api = {
    snapshot: async () => snapshotFixture(),
    ready: async () => {},
    on: (name: string, cb: (v: unknown) => void) => {
      listeners.set(name, cb);
      return () => {
        listeners.delete(name);
      };
    },
    setDraft: vi.fn<Transport["setDraft"]>(async () => {}),
    submit: vi.fn<Transport["submit"]>(async () => "op"),
    streamSnapshot: async () => ({ revision: 0, blocks: [] }),
    audioSnapshot: vi.fn<Transport["audioSnapshot"]>(async () => audio),
    submitTranscript: vi.fn<Transport["submitTranscript"]>(async () => "op"),
  } satisfies Transport;
  return { client: new SolveoClient(api), listeners, api };
}
function segment(id: string, text: string, final = true) {
  return { id, track: "system", started_at: "2026-10-23T10:00:00Z", text, final };
}
async function ready(client: SolveoClient) {
  await vi.waitFor(() => {
    const s = client.getState();
    expect(s.status === "ready" && s.audio.revision).toBe(5);
  });
}
describe("audio bridge", () => {
  it("reads AudioSnapshot on connect and accepts only newer events", async () => {
    const { client, listeners, api } = setup();
    const off = client.connect();
    await ready(client);
    expect(api.audioSnapshot).toHaveBeenCalledTimes(1);
    listeners.get("solveo:audio")?.({ ...audioFixture(), revision: 4, model: "ready" });
    let s = client.getState();
    expect(s.status === "ready" && s.audio.model).toBe("stopped");
    listeners.get("solveo:audio")?.({ ...audioFixture(), revision: 6, model: "ready" });
    s = client.getState();
    expect(s.status === "ready" && s.audio.model).toBe("ready");
    off();
  });
  it("rejects an invalid audio event without losing state", async () => {
    const { client, listeners } = setup();
    const off = client.connect();
    await ready(client);
    listeners.get("solveo:audio")?.({ ...audioFixture(), revision: 9, model: "exploded" });
    const s = client.getState();
    expect(s.status === "ready" && s.audio.revision).toBe(5);
    expect(s.status === "ready" && s.error).toMatch(/Ошибка обмена/);
    off();
  });
  it("inserts transcript text after the live editor text and keeps the reference", async () => {
    const { client } = setup();
    const off = client.connect();
    await ready(client);
    expect(client.insertTranscript("Вопрос с созвона")).toBe(true);
    let s = client.getState();
    expect(s.status === "ready" && s.editor.text).toBe("Вопрос с созвона");
    client.edit("мой текст");
    client.insertTranscript("ещё фраза");
    s = client.getState();
    expect(s.status === "ready" && s.editor.text).toBe("мой текст\nещё фраза");
    client.setComposing(true);
    expect(client.insertTranscript("во время IME")).toBe(false);
    off();
  });
  it("submits selected segment ids for the active chat with a fresh request id", async () => {
    const { client, api } = setup({
      ...audioFixture(),
      segments: [segment("a:1", "Первая"), segment("a:2", "Вторая")],
    });
    const off = client.connect();
    await ready(client);
    expect(await client.submitTranscript(["a:2", "a:1"])).toBe(true);
    const input = api.submitTranscript.mock.calls[0]?.[0];
    expect(input?.chat_id).toBe("first");
    expect(input?.segment_ids).toEqual(["a:2", "a:1"]);
    expect(input?.request_id).toMatch(/^[0-9a-f-]{36}$/);
    await client.submitTranscript(["a:1"]);
    expect(api.submitTranscript.mock.calls[1]?.[0]?.request_id).not.toBe(input?.request_id);
    expect(api.setDraft).not.toHaveBeenCalled();
    off();
  });
});
