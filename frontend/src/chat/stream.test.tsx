// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vite-plus/test";
import { ChatView } from "./ChatView";
import { parseSnapshot, type StreamSnapshot } from "../bridge/state";
import { snapshotFixture } from "../bridge/test-fixture";

beforeEach(() => {
  Element.prototype.scrollTo = vi.fn();
});
afterEach(cleanup);

const requesting = (answers?: unknown) =>
  parseSnapshot({
    ...snapshotFixture(),
    operation: { id: "op", kind: "submit", phase: "requesting", message: "Ожидаю ответ" },
    messages: [
      {
        id: "question",
        role: "user",
        text: "вопрос",
        status: "requesting",
        attachment_ids: [],
        config_revision: 1,
        attempts: [{ id: "attempt", status: "requesting", config_revision: 1, error: "" }],
        ...(answers === undefined ? {} : { answers }),
      },
    ],
  });

const block = (overrides: Partial<StreamSnapshot["blocks"][number]>) => ({
  chat_id: "first",
  operation_id: "op",
  message_id: "question",
  attempt_id: "attempt",
  block: "message" as const,
  text: "Частичный ответ",
  ...overrides,
});

function show(snapshot: ReturnType<typeof requesting>, stream: StreamSnapshot) {
  render(
    <ChatView
      snapshot={snapshot}
      stream={stream}
      onConfigure={vi.fn()}
      onOpenImage={vi.fn()}
      onRetry={vi.fn()}
      onCopy={async () => true}
      onOpenLink={vi.fn()}
    />,
  );
}

it("shows unfinished text of the running request without a copy action", () => {
  show(requesting(), { revision: 1, blocks: [block({ text: "Сначала **сумма**" })] });
  expect(screen.getByText("Получаю ответ…")).toBeTruthy();
  expect(screen.getByText("сумма").tagName).toBe("STRONG");
  expect(screen.queryByLabelText("Скопировать ответ")).toBeNull();
});

it("ignores previews of another operation or attempt", () => {
  show(requesting(), {
    revision: 1,
    blocks: [
      block({ operation_id: "old", text: "чужой" }),
      block({ attempt_id: "old", text: "старый" }),
    ],
  });
  expect(screen.queryByText("чужой")).toBeNull();
  expect(screen.queryByText("старый")).toBeNull();
  expect(screen.queryByText("Получаю ответ…")).toBeNull();
});

it("renders an unclosed code fence as code while streaming", () => {
  show(requesting(), {
    revision: 1,
    blocks: [block({ text: "Решение:\n```go\nfunc sum() int {" })],
  });
  expect(screen.getByText("func sum() int {").closest("code")).toBeTruthy();
  expect(screen.queryByLabelText("Скопировать код")).toBeNull();
});

it("streams approach and solution into their own blocks", () => {
  const answer = (kind: string, status: string, text: string) => ({
    kind,
    status,
    text,
    error: "",
    attempt_id: "attempt",
    model: "",
    reasoning: "",
    elapsed_ms: 0,
  });
  show(requesting([answer("approach", "requesting", ""), answer("solution", "requesting", "")]), {
    revision: 2,
    blocks: [
      block({ block: "approach", text: "Идея через карту" }),
      block({ block: "solution", text: "Код решения" }),
    ],
  });
  const approach = screen.getByLabelText("Подход");
  const solution = screen.getByLabelText("Решение");
  expect(approach.textContent).toContain("Идея через карту");
  expect(approach.textContent).toContain("Получаю подход…");
  expect(solution.textContent).toContain("Код решения");
  expect(solution.textContent).not.toContain("Идея");
});

it("keeps the approach preview after the solution has failed", () => {
  const answer = (kind: string, status: string) => ({
    kind,
    status,
    text: "",
    error: status === "failed" ? "ошибка решения" : "",
    attempt_id: "attempt",
    model: "",
    reasoning: "",
    elapsed_ms: 0,
  });
  const snapshot = requesting([answer("approach", "requesting"), answer("solution", "failed")]);
  const message = snapshot.messages[0];
  if (message === undefined) throw new Error("fixture");
  message.status = "failed";
  show(snapshot, {
    revision: 3,
    blocks: [block({ block: "approach", text: "Подход ещё приходит\n```go\nx := 1" })],
  });
  expect(screen.getByLabelText("Подход").textContent).toContain("Подход ещё приходит");
  expect(screen.queryByLabelText("Скопировать код")).toBeNull();
});
