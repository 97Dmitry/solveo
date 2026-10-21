// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vite-plus/test";
import { ChatView } from "./ChatView";
import { emptyStream, parseSnapshot } from "../bridge/state";
import { snapshotFixture } from "../bridge/test-fixture";

beforeEach(() => {
  Element.prototype.scrollTo = vi.fn();
});
afterEach(cleanup);

const interrupted = (recovered: unknown[], answers?: unknown) =>
  parseSnapshot({
    ...snapshotFixture(),
    messages: [
      {
        id: "question",
        role: "user",
        text: "вопрос",
        status: "interrupted",
        attachment_ids: [],
        config_revision: 1,
        attempts: [{ id: "attempt", status: "interrupted", config_revision: 1, error: "" }],
        ...(answers === undefined ? {} : { answers }),
      },
    ],
    controls: { ...snapshotFixture().controls, can_retry: true },
    recovered,
  });

const block = (block: string, text: string) => ({
  chat_id: "first",
  operation_id: "",
  message_id: "question",
  attempt_id: "attempt",
  block,
  text,
});

function show(snapshot: ReturnType<typeof interrupted>) {
  render(
    <ChatView
      snapshot={snapshot}
      stream={emptyStream}
      onConfigure={vi.fn()}
      onOpenImage={vi.fn()}
      onRetry={vi.fn()}
      onCopy={async () => true}
      onOpenLink={vi.fn()}
    />,
  );
}

it("shows text received before a crash as incomplete, without copy actions", () => {
  show(interrupted([block("message", "Начало **ответа**")]));
  expect(screen.getByText("Получено до сбоя, ответ не завершён")).toBeTruthy();
  expect(screen.getByText("ответа").tagName).toBe("STRONG");
  expect(screen.queryByLabelText("Скопировать ответ")).toBeNull();
  expect(screen.getByText("Повторить")).toBeTruthy();
});

it("shows recovered text only for the interrupted parallel block", () => {
  const answer = (kind: string, status: string, text = "") => ({
    kind,
    status,
    text,
    error: "",
    attempt_id: "attempt",
    model: "",
    reasoning: "",
    elapsed_ms: 0,
  });
  show(
    interrupted(
      [block("approach", "старый подход"), block("solution", "часть решения")],
      [answer("approach", "complete", "Готовый подход"), answer("solution", "interrupted")],
    ),
  );
  expect(screen.getByText("часть решения")).toBeTruthy();
  expect(screen.queryByText("старый подход")).toBeNull();
  expect(screen.queryByLabelText("Скопировать решение")).toBeNull();
});
