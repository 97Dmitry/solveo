// @vitest-environment jsdom
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, it, vi } from "vite-plus/test";
import { ChatView } from "./ChatView";
import { emptyStream, parseSnapshot } from "../bridge/state";
import { snapshotFixture } from "../bridge/test-fixture";

beforeEach(() => {
  Element.prototype.scrollTo = vi.fn();
});
afterEach(cleanup);
const message = { attachment_ids: [], attempts: [], config_revision: 1, status: "complete" };
it("prepares an action for the selected old answer and copies a question separately", async () => {
  const onFollowup = vi.fn();
  const onReuse = vi.fn();
  render(
    <ChatView
      snapshot={parseSnapshot({
        ...snapshotFixture(),
        messages: [
          { ...message, id: "q", role: "user", text: "original question" },
          { ...message, id: "old", role: "assistant", text: "old answer" },
          { ...message, id: "latest", role: "assistant", text: "latest answer" },
        ],
      })}
      stream={emptyStream}
      onConfigure={vi.fn()}
      onOpenImage={vi.fn()}
      onRetry={vi.fn()}
      onCopy={async () => true}
      onOpenLink={vi.fn()}
      onFollowup={onFollowup}
      onReuse={onReuse}
      canPrepareFollowup
    />,
  );
  const old = screen.getByText("old answer").closest("article");
  if (!old) throw new Error("missing answer");
  await userEvent.click(within(old).getByRole("button", { name: "Короче" }));
  expect(onFollowup).toHaveBeenCalledWith("old", "", "Дай более краткий ответ.");
  await userEvent.click(within(old).getByRole("button", { name: "Напиши код" }));
  expect(onFollowup).toHaveBeenLastCalledWith(
    "old",
    "",
    "Напиши полный рабочий код решения исходного задания, к которому относится этот ответ. Учти условия и ограничения задачи. Добавь краткое пояснение и сложность.",
  );
  await userEvent.click(screen.getByRole("button", { name: "Изменить и отправить заново" }));
  expect(onReuse).toHaveBeenCalledWith("q");
});
it("does not offer followups for unfinished previews", () => {
  render(
    <ChatView
      snapshot={parseSnapshot({
        ...snapshotFixture(),
        messages: [{ ...message, id: "q", role: "user", text: "question", status: "requesting" }],
      })}
      stream={emptyStream}
      onConfigure={vi.fn()}
      onOpenImage={vi.fn()}
      onRetry={vi.fn()}
      onCopy={async () => true}
      onOpenLink={vi.fn()}
      onFollowup={vi.fn()}
      canPrepareFollowup
    />,
  );
  expect(screen.queryByRole("button", { name: "Короче" })).toBeNull();
});

it("addresses parallel blocks independently and leaves failed blocks unavailable", async () => {
  const onFollowup = vi.fn();
  const answer = { error: "", attempt_id: "a", model: "", reasoning: "", elapsed_ms: 10 };
  render(
    <ChatView
      snapshot={parseSnapshot({
        ...snapshotFixture(),
        messages: [
          {
            ...message,
            id: "parallel",
            role: "user",
            text: "question",
            answers: [
              { ...answer, kind: "approach", status: "complete", text: "idea" },
              { ...answer, kind: "solution", status: "failed", text: "" },
            ],
          },
        ],
      })}
      stream={emptyStream}
      onConfigure={vi.fn()}
      onOpenImage={vi.fn()}
      onRetry={vi.fn()}
      onCopy={async () => true}
      onOpenLink={vi.fn()}
      onFollowup={onFollowup}
      canPrepareFollowup
    />,
  );
  await userEvent.click(
    within(screen.getByRole("region", { name: "Подход" })).getByRole("button", {
      name: "Подробнее",
    }),
  );
  expect(onFollowup).toHaveBeenCalledWith("parallel", "approach", "Раскрой ответ подробнее.");
  expect(
    within(screen.getByRole("region", { name: "Решение" })).queryByRole("button", {
      name: "Подробнее",
    }),
  ).toBeNull();
});
