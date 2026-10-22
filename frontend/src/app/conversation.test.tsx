// @vitest-environment jsdom
import { act, cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vite-plus/test";
import App from "./App";

const transport = vi.hoisted(() => ({
  listeners: new Map<string, (data: unknown) => void>(),
  submit: vi.fn<() => Promise<string>>(),
}));
vi.mock("../bridge/wails", async () => {
  const { SolveoClient } = await import("../bridge/client");
  const { snapshotFixture } = await import("../bridge/test-fixture");
  return {
    client: new SolveoClient({
      snapshot: async () => snapshotFixture(),
      ready: async () => {},
      setDraft: async () => {},
      submit: transport.submit,
      streamSnapshot: async () => ({ revision: 0, blocks: [] }),
      on: (name, cb) => {
        transport.listeners.set(name, cb);
        return () => {
          transport.listeners.delete(name);
        };
      },
    }),
    actions: { copy: async () => true, openLink: () => {} },
    loadAccount: async () => ({ signed_in: true, kind: "fake" }),
    loadModels: async () => [],
  };
});
import { snapshotFixture } from "../bridge/test-fixture";
afterEach(cleanup);
it("keeps the next question editable across preparation, waiting, streaming and saving", async () => {
  Element.prototype.scrollTo = vi.fn();
  let finish: ((id: string) => void) | undefined;
  transport.submit.mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  render(<App />);
  const input = await screen.findByRole("textbox", { name: "Вопрос" });
  const user = userEvent.setup();
  await user.type(input, "sent question");
  await user.click(screen.getByRole("button", { name: "Отправить вопрос" }));
  expect(screen.getByText("Подготавливаю запрос…")).toBeTruthy();
  await user.clear(input);
  await user.type(input, "next question");
  const fixture = snapshotFixture();
  const emit = (phase: "requesting" | "saving", revision: number, message: string) => {
    act(() =>
      transport.listeners.get("solveo:state")?.({
        ...fixture,
        revision,
        operation: { id: "op", kind: "submit", phase, message },
        controls: { ...fixture.controls, can_submit: false, can_cancel: phase === "requesting" },
      }),
    );
  };
  emit("requesting", 2, "Ожидаю ответ");
  expect(screen.getByText("Ожидаю ответ")).toBeTruthy();
  expect(input.hasAttribute("disabled")).toBe(false);
  act(() =>
    transport.listeners.get("solveo:stream")?.({
      revision: 1,
      blocks: [
        {
          chat_id: "first",
          operation_id: "op",
          message_id: "q",
          attempt_id: "a",
          block: "message",
          text: "chunk",
        },
      ],
    }),
  );
  expect(screen.getByText("Получаю ответ…")).toBeTruthy();
  emit("saving", 3, "Сохраняю результат");
  expect(screen.getByText("Сохраняю результат")).toBeTruthy();
  expect(input.hasAttribute("disabled")).toBe(false);
  await user.type(input, " while saving");
  await act(async () => {
    finish?.("op");
    await Promise.resolve();
  });
  expect(input).toHaveProperty("value", "next question while saving");
});
