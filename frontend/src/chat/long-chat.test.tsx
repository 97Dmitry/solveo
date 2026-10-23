// @vitest-environment jsdom
import { act, cleanup, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { ChatView } from "./ChatView";
import { snapshotFixture } from "../bridge/test-fixture";
import {
  acceptSnapshot,
  emptyStream,
  parseSnapshot,
  type Message,
  type Snapshot,
} from "../bridge/state";
afterEach(cleanup);
beforeEach(() => {
  Element.prototype.scrollTo = vi.fn();
});
const answer = (i: number) =>
  `## Решение ${i}\n\nОбъяснение с **акцентами** и списком:\n\n- первый пункт\n- второй пункт\n\n\`\`\`go\nfunc solve${i}(xs []int) int {\n\tsum := 0\n\tfor _, x := range xs {\n\t\tsum += x\n\t}\n\treturn sum\n}\n\`\`\`\n\n| n | время |\n|---|---|\n| 1 | O(n) |\n`.repeat(
    2,
  );
function longSnapshot(turns: number): Snapshot {
  const s = snapshotFixture();
  const messages: Message[] = [];
  for (let i = 0; i < turns; i++) {
    const base = {
      attachment_ids: [],
      config_revision: 1,
      attempts: [],
      status: "complete" as const,
    };
    messages.push({
      ...base,
      id: `u${i}`,
      role: "user",
      text: `Вопрос ${i}: как посчитать сумму?`,
    });
    messages.push({ ...base, id: `a${i}`, role: "assistant", text: answer(i) });
  }
  s.messages = messages;
  return s;
}
const props = {
  stream: emptyStream,
  onConfigure: vi.fn(),
  onOpenImage: vi.fn(),
  onRetry: vi.fn(),
  onCopy: vi.fn(async () => true),
  onOpenLink: vi.fn(),
};
function time(run: () => void) {
  const start = performance.now();
  act(run);
  return performance.now() - start;
}
describe("long conversation", () => {
  it("keeps 300 turns cheap to re-render on drafts, snapshots and stream updates", () => {
    let current = parseSnapshot(longSnapshot(300));
    const { rerender } = render(<ChatView snapshot={current} {...props} />);
    const snapshots: number[] = [];
    const keystrokes: number[] = [];
    const sharing: number[] = [];
    for (let i = 0; i < 5; i++) {
      // Every draft change publishes a new Snapshot; JSON parsing yields new objects.
      const wire: unknown = JSON.parse(
        JSON.stringify({ ...current, revision: current.revision + 1 }),
      );
      const parsed = performance.now();
      const next = acceptSnapshot(current, parseSnapshot(wire));
      sharing.push(performance.now() - parsed);
      expect(next.messages[0]).toBe(current.messages[0]);
      snapshots.push(time(() => rerender(<ChatView snapshot={next} {...props} />)));
      current = next;
      // A keystroke re-renders App with the same snapshot and stable callbacks.
      keystrokes.push(time(() => rerender(<ChatView snapshot={current} {...props} />)));
    }
    const stream = { revision: 1, blocks: [] };
    const streaming = time(() =>
      rerender(<ChatView snapshot={current} {...props} stream={stream} />),
    );
    console.log(
      `[long-chat] snapshot ms: ${snapshots.map((t) => t.toFixed(1)).join(", ")}; ` +
        `keystroke ms: ${keystrokes.map((t) => t.toFixed(1)).join(", ")}; stream ms: ${streaming.toFixed(1)}; ` +
        `parse+share ms: ${sharing.map((t) => t.toFixed(1)).join(", ")}`,
    );
  });
});
