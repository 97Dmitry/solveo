import { describe, expect, it } from "vite-plus/test";
import { acceptSnapshot, parseSnapshot } from "./state";
import { snapshotFixture } from "./test-fixture";
describe("authoritative state", () => {
  it("rejects malformed wire data", () => {
    expect(() => parseSnapshot({ revision: 1 })).toThrow();
  });
  it("ignores duplicate and out of order events", () => {
    const a = snapshotFixture();
    const b = { ...a, revision: a.revision + 1, title: "new" };
    expect(acceptSnapshot(b, a)).toBe(b);
    expect(acceptSnapshot(b, b)).toBe(b);
  });
  it("replaces active chat only from newer state", () => {
    const a = snapshotFixture();
    const b = { ...a, revision: 2, active_chat_id: "second" };
    expect(acceptSnapshot(a, b)?.active_chat_id).toBe("second");
  });
  it("rejects unknown operation phases", () => {
    expect(() =>
      parseSnapshot({
        ...snapshotFixture(),
        operation: { id: "", kind: "", phase: "typo", message: "" },
      }),
    ).toThrow();
  });
});

it("rejects malformed parallel answer metadata", () => {
  expect(() =>
    parseSnapshot({
      ...snapshotFixture(),
      messages: [
        {
          id: "question",
          role: "user",
          text: "solve",
          status: "complete",
          attachment_ids: [],
          config_revision: 1,
          attempts: [],
          answers: [
            {
              kind: "typo",
              status: "complete",
              text: "wrong",
              error: "",
              attempt_id: "attempt",
              model: "light",
              reasoning: "none",
              elapsed_ms: -1,
            },
          ],
        },
      ],
    }),
  ).toThrow();
});

it("validates global instructions by Unicode code points", () => {
  const snapshot = snapshotFixture();
  snapshot.settings.global_instructions = "😀".repeat(16384);
  expect(() => parseSnapshot(snapshot)).not.toThrow();
  snapshot.settings.global_instructions += "a";
  expect(() => parseSnapshot(snapshot)).toThrow();
  snapshot.settings.global_instructions = "\uD800";
  expect(() => parseSnapshot(snapshot)).toThrow();
});
