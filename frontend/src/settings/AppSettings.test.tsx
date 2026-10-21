// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vite-plus/test";
import { AppSettings } from "./AppSettings";
import { snapshotFixture } from "../bridge/test-fixture";
afterEach(cleanup);
const connection = () => ({
  loadAccount: vi.fn(async () => ({ signed_in: false, kind: "" })),
  onLogin: vi.fn(),
  onCancelLogin: vi.fn(),
  onOpenLink: vi.fn(),
});
it("changes interface scale independently of text size and restores the saved value", async () => {
  const snapshot = snapshotFixture();
  snapshot.settings.ui_scale = 125;
  const save = vi.fn();
  render(
    <AppSettings
      snapshot={snapshot}
      connection={connection()}
      onSettings={save}
      onCheck={vi.fn()}
      onLog={vi.fn()}
      onRetrySave={vi.fn()}
      onDiscard={vi.fn()}
      onExit={vi.fn()}
    />,
  );
  expect(screen.getByLabelText<HTMLSelectElement>("Масштаб интерфейса").value).toBe("125");
  await userEvent.selectOptions(screen.getByLabelText("Масштаб интерфейса"), "150");
  expect(save).toHaveBeenCalledWith({ ...snapshot.settings, ui_scale: 150 });
});

it("shows the device-code login from Go state and refreshes the account", async () => {
  const snapshot = snapshotFixture();
  const actions = connection();
  const view = (s: typeof snapshot) => (
    <AppSettings
      snapshot={s}
      connection={actions}
      onSettings={vi.fn()}
      onCheck={vi.fn()}
      onLog={vi.fn()}
      onRetrySave={vi.fn()}
      onDiscard={vi.fn()}
      onExit={vi.fn()}
    />
  );
  const { rerender } = render(view(snapshot));
  expect(screen.queryByRole("checkbox", { name: /потоковый/ })).toBeNull();
  await userEvent.click(await screen.findByText("Войти в Codex"));
  expect(actions.onLogin).toHaveBeenCalled();
  rerender(
    view({
      ...snapshot,
      login: {
        id: "l",
        url: "https://auth.example.test/device",
        code: "ABCD-1234",
        state: "pending",
        error: "",
      },
    }),
  );
  expect(screen.getByText("ABCD-1234")).toBeTruthy();
  await userEvent.click(screen.getByText("Открыть страницу входа"));
  expect(actions.onOpenLink).toHaveBeenCalledWith("https://auth.example.test/device");
  actions.loadAccount.mockResolvedValue({ signed_in: true, kind: "chatgpt" });
  rerender(view({ ...snapshot, login: { id: "l", url: "", code: "", state: "done", error: "" } }));
  expect(await screen.findByText("Вход выполнен (chatgpt)")).toBeTruthy();
  expect(screen.queryByText("Войти в Codex")).toBeNull();
});

it("saves global instructions explicitly without checking the model connection", async () => {
  const snapshot = snapshotFixture();
  const save = vi.fn();
  const check = vi.fn();
  render(
    <AppSettings
      snapshot={snapshot}
      connection={connection()}
      onSettings={save}
      onCheck={check}
      onLog={vi.fn()}
      onRetrySave={vi.fn()}
      onDiscard={vi.fn()}
      onExit={vi.fn()}
    />,
  );
  expect(screen.getByText("Применяется к новым чатам")).toBeTruthy();
  await userEvent.type(screen.getByLabelText("Общие инструкции"), "Отвечай кратко");
  expect(save).not.toHaveBeenCalled();
  await userEvent.click(screen.getByText("Сохранить общие инструкции"));
  expect(save).toHaveBeenCalledWith({
    ...snapshot.settings,
    global_instructions: "Отвечай кратко",
  });
  expect(check).not.toHaveBeenCalled();
});

it("keeps newer edits while a settings save completes", async () => {
  const snapshot = snapshotFixture();
  const actions = connection();
  const save = vi.fn();
  const view = (s: typeof snapshot) => (
    <AppSettings
      snapshot={s}
      connection={actions}
      onSettings={save}
      onCheck={vi.fn()}
      onLog={vi.fn()}
      onRetrySave={vi.fn()}
      onDiscard={vi.fn()}
      onExit={vi.fn()}
    />
  );
  const { rerender } = render(view(snapshot));
  await userEvent.type(screen.getByLabelText("Общие инструкции"), "A");
  await userEvent.click(screen.getByText("Сохранить общие инструкции"));
  await userEvent.type(screen.getByLabelText("Общие инструкции"), "B");
  rerender(view({ ...snapshot, settings: { ...snapshot.settings, global_instructions: "A" } }));
  expect(screen.getByLabelText<HTMLTextAreaElement>("Общие инструкции").value).toBe("AB");
});
