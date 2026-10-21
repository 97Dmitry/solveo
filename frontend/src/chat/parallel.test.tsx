// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, it, vi } from "vite-plus/test";
import { ChatView } from "./ChatView";
import { emptyStream, parseSnapshot } from "../bridge/state";
import { snapshotFixture } from "../bridge/test-fixture";

beforeEach(() => {
  Element.prototype.scrollTo = vi.fn();
});
afterEach(cleanup);
it("shows completed approach while the solution is pending", () => {
  const snapshot = parseSnapshot({
    ...snapshotFixture(),
    messages: [
      {
        id: "question",
        role: "user",
        text: "solve",
        status: "requesting",
        attachment_ids: [],
        config_revision: 1,
        attempts: [],
        answers: [
          {
            kind: "approach",
            status: "complete",
            text: "Use a hash map",
            error: "",
            attempt_id: "attempt",
            model: "light",
            reasoning: "none",
            elapsed_ms: 1200,
          },
          {
            kind: "solution",
            status: "requesting",
            text: "",
            error: "",
            attempt_id: "attempt",
            model: "strong",
            reasoning: "high",
            elapsed_ms: 0,
          },
        ],
      },
    ],
  });
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
  expect(screen.getByText("Подход")).toBeTruthy();
  expect(screen.getByText("Use a hash map")).toBeTruthy();
  expect(screen.getByText(/Ожидаю решение/)).toBeTruthy();
  expect(screen.queryByRole("button", { name: /Повторить/ })).toBeNull();
});
it("retries only the solution by the original question ID", async () => {
  const snapshot = parseSnapshot({
    ...snapshotFixture(),
    controls: { ...snapshotFixture().controls, can_retry: true },
    messages: [
      {
        id: "question",
        role: "user",
        text: "solve",
        status: "failed",
        attachment_ids: [],
        config_revision: 1,
        attempts: [],
        answers: [
          {
            kind: "approach",
            status: "complete",
            text: "Use a map",
            error: "",
            attempt_id: "attempt",
            model: "light",
            reasoning: "none",
            elapsed_ms: 1200,
          },
          {
            kind: "solution",
            status: "failed",
            text: "",
            error: "offline",
            attempt_id: "attempt",
            model: "strong",
            reasoning: "high",
            elapsed_ms: 3000,
          },
        ],
      },
    ],
  });
  const retry = vi.fn();
  render(
    <ChatView
      snapshot={snapshot}
      stream={emptyStream}
      onConfigure={vi.fn()}
      onOpenImage={vi.fn()}
      onRetry={retry}
      onCopy={async () => true}
      onOpenLink={vi.fn()}
    />,
  );
  expect(screen.getByText("Use a map")).toBeTruthy();
  expect(screen.getByText("offline")).toBeTruthy();
  await userEvent.click(screen.getByRole("button", { name: "Повторить решение" }));
  expect(retry).toHaveBeenCalledWith("question");
});

it("selects code from the main answer rather than the preliminary approach", async () => {
  const { lastAnswerText, latestCode } = await import("./code");
  const snapshot = parseSnapshot({
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
            kind: "approach",
            status: "complete",
            text: "```go\nwrong\n```",
            error: "",
            attempt_id: "attempt",
            model: "light",
            reasoning: "none",
            elapsed_ms: 1,
          },
          {
            kind: "solution",
            status: "complete",
            text: "```go\ncorrect\n```",
            error: "",
            attempt_id: "attempt",
            model: "strong",
            reasoning: "high",
            elapsed_ms: 2,
          },
        ],
      },
    ],
  });
  expect(latestCode(lastAnswerText(snapshot.messages))).toBe("correct\n");
});
