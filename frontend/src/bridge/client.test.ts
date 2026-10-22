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
    setDraft: vi.fn<Transport["setDraft"]>(async () => {}),
    submit,
    streamSnapshot: async () => ({
      revision: 3,
      blocks: [
        {
          chat_id: "first",
          operation_id: "op",
          message_id: "m",
          attempt_id: "a",
          block: "message",
          text: "до перезагрузки",
        },
      ],
    }),
  } satisfies Transport;
  return { client: new SolveoClient(api), listeners, submit, setDraft: api.setDraft };
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

describe("answer stream", () => {
  it("recovers previews after connect and accepts only newer revisions", async () => {
    const { client, listeners } = setup();
    const off = client.connect();
    await vi.waitFor(() => {
      const state = client.getState();
      expect(state.status === "ready" && state.stream.revision).toBe(3);
    });
    const block = {
      chat_id: "first",
      operation_id: "op",
      message_id: "m",
      attempt_id: "a",
      block: "message",
    };
    listeners.get("solveo:stream")?.({ revision: 2, blocks: [{ ...block, text: "старое" }] });
    listeners.get("solveo:stream")?.({ revision: 5, blocks: [{ ...block, text: "новое" }] });
    const state = client.getState();
    expect(state.status === "ready" && state.stream.blocks[0]?.text).toBe("новое");
    listeners.get("solveo:stream")?.({ revision: 6, blocks: [{ ...block, block: "bogus" }] });
    const after = client.getState();
    expect(after.status === "ready" && after.stream.revision).toBe(5);
    expect(after.status === "ready" && after.error).toContain("Ошибка обмена");
    off();
  });
});

describe("follow-up editor", () => {
  it("keeps a selected answer through edit and submit without rewriting history", async () => {
    const { client, listeners, submit, setDraft } = setup();
    const off = client.connect();
    await vi.waitFor(() => expect(client.getState().status).toBe("ready"));
    const fixture = snapshotFixture();
    const answer = {
      id: "answer",
      role: "assistant",
      text: "original answer",
      status: "complete",
      attachment_ids: [],
      attempts: [],
      config_revision: 1,
    };
    listeners.get("solveo:state")?.({ ...fixture, revision: 2, messages: [answer] });
    expect(client.prepareFollowup("answer", "", "Короче")).toBe(true);
    client.edit("Короче, пожалуйста");
    await client.flushDraft();
    expect(setDraft.mock.calls.at(-1)?.[0]).toMatchObject({
      text: "Короче, пожалуйста",
      reply_to_id: "answer",
    });
    await client.submit();
    expect(submit.mock.calls[0]?.[0]).toMatchObject({
      text: "Короче, пожалуйста",
      reply_to_id: "answer",
      reply_to_kind: "",
    });
    const state = client.getState();
    expect(state.status === "ready" && state.snapshot.messages).toEqual([answer]);
    expect(client.prepareFollowup("answer", "", "Подробнее")).toBe(false);
    expect(client.getState()).toMatchObject({ editor: { text: "Короче, пожалуйста" } });
    off();
  });
  it("preserves input typed while submit is awaiting its acknowledgement", async () => {
    const { client, submit, listeners } = setup();
    const off = client.connect();
    await vi.waitFor(() => expect(client.getState().status).toBe("ready"));
    let finish: ((id: string) => void) | undefined;
    submit.mockImplementation(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    client.edit("sent");
    const pending = client.submit();
    client.edit("next question");
    finish?.("op");
    await pending;
    listeners.get("solveo:state")?.({
      ...snapshotFixture(),
      revision: 5,
      draft: { text: "", revision: 2 },
    });
    expect(client.getState()).toMatchObject({ editor: { text: "next question" } });
    off();
  });
});
