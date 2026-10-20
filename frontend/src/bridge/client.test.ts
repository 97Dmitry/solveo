import { describe, expect, it, vi } from "vite-plus/test";
import { SolveoClient, type Transport } from "./client";
import { snapshotFixture } from "./test-fixture";
function setup() {
  const listeners = new Map<string, (data: unknown) => void>();
  const fixture = snapshotFixture();
  const submit = vi.fn<Transport["submit"]>(async () => "op");
  const api = {
    snapshot: async () => fixture,
    ready: async () => {},
    on: (name: string, cb: (v: unknown) => void) => {
      listeners.set(name, cb);
      return () => {
        listeners.delete(name);
      };
    },
    setDraft: async () => {},
    submit,
  } satisfies Transport;
  return { client: new SolveoClient(api), listeners, submit };
}
describe("bridge lifecycle", () => {
  it("sends live text for native intent and ignores duplicates", async () => {
    const { client, listeners, submit } = setup();
    const off = client.connect();
    await vi.waitFor(() => expect(client.getState().status).toBe("ready"));
    client.edit("latest text");
    const intent = {
      id: "intent",
      chat_id: "first",
      draft_revision: 0,
      expires_at: Date.now() + 3000,
    };
    listeners.get("solveo:submit")?.(intent);
    await vi.waitFor(() => expect(submit).toHaveBeenCalledTimes(1));
    expect(submit.mock.calls[0]?.[0]?.text).toBe("latest text");
    listeners.get("solveo:submit")?.(intent);
    await Promise.resolve();
    expect(submit).toHaveBeenCalledTimes(1);
    off();
    expect(listeners.size).toBe(0);
  });
  it("does not send during IME or for another chat", async () => {
    const { client, listeners, submit } = setup();
    const off = client.connect();
    await vi.waitFor(() => expect(client.getState().status).toBe("ready"));
    client.setComposing(true);
    listeners.get("solveo:submit")?.({
      id: "ime",
      chat_id: "first",
      draft_revision: 0,
      expires_at: Date.now() + 3000,
    });
    client.setComposing(false);
    listeners.get("solveo:submit")?.({
      id: "wrong",
      chat_id: "other",
      draft_revision: 0,
      expires_at: Date.now() + 3000,
    });
    await Promise.resolve();
    expect(submit).not.toHaveBeenCalled();
    off();
  });
});
