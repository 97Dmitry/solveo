// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vite-plus/test";
import { audioFixture, questionFixture } from "../bridge/test-fixture";
import { AudioWindow } from "./AudioWindow";
type Command = { name: string; question?: { action: string; chat_id: string } };
const api = vi.hoisted(() => ({
  state: vi.fn(),
  ready: vi.fn(),
  execute: vi.fn<(command: Command) => Promise<void>>(),
}));
vi.mock("../bridge/wails", () => ({ audioWindow: api }));
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});
it("shows the target chat and sends selected final IDs with that target", async () => {
  api.state.mockResolvedValue({
    question: questionFixture(),
    audio: {
      ...audioFixture(),
      model: "ready",
      segments: [
        {
          id: "seg",
          track: "system",
          started_at: "2026-10-23T10:00:00Z",
          text: "Вопрос",
          final: true,
        },
      ],
    },
    chat_id: "chat",
    chat_title: "Интервью",
    can_send: true,
    disabled: false,
    theme: "dark",
    ui_scale: 100,
    show_version: 1,
  });
  api.ready.mockResolvedValue(undefined);
  api.execute.mockResolvedValue(undefined);
  render(<AudioWindow />);
  expect(await screen.findByText("Чат: Интервью")).toBeTruthy();
  await userEvent.click(screen.getByRole("button", { name: /Собеседник.*Вопрос/ }));
  await userEvent.click(screen.getByRole("button", { name: "Отправить" }));
  await waitFor(() =>
    expect(api.execute).toHaveBeenCalledWith({ name: "send", ids: ["seg"], chat_id: "chat" }),
  );
  expect(api.ready).toHaveBeenCalled();
});

it("switches between floating and left-attached modes", async () => {
  const base = {
    question: questionFixture(),
    audio: audioFixture(),
    chat_id: "chat",
    chat_title: "Интервью",
    can_send: true,
    disabled: false,
    theme: "dark",
    ui_scale: 100,
    show_version: 1,
    pinned: false,
    docked: false,
    window_error: "",
  };
  api.state.mockResolvedValue(base);
  api.ready.mockResolvedValue(undefined);
  api.execute.mockResolvedValue(undefined);
  render(<AudioWindow />);
  await userEvent.click(await screen.findByRole("button", { name: "Прикрепить слева" }));
  expect(api.execute).toHaveBeenCalledWith({ name: "dockLeft" });
  api.state.mockResolvedValue({ ...base, docked: true });
  await userEvent.click(await screen.findByRole("button", { name: "Открепить" }));
  expect(api.execute).toHaveBeenCalledWith({ name: "float" });
});

it("keeps live selection across tabs and requests the model catalog when settings open", async () => {
  api.state.mockResolvedValue({
    question: questionFixture(),
    audio: {
      ...audioFixture(),
      history_enabled: true,
      model: "ready",
      segments: [
        {
          id: "seg",
          track: "system",
          started_at: "2026-10-23T10:00:00Z",
          text: "Вопрос",
          final: true,
        },
      ],
    },
    chat_id: "chat",
    chat_title: "Интервью",
    can_send: true,
    disabled: false,
    theme: "dark",
    ui_scale: 100,
    show_version: 1,
    pinned: false,
    docked: false,
    window_error: "",
  });
  api.ready.mockResolvedValue(undefined);
  api.execute.mockResolvedValue(undefined);
  render(<AudioWindow />);
  await userEvent.click(await screen.findByRole("button", { name: /Собеседник.*Вопрос/ }));
  expect(screen.getByRole("tab", { name: "История" })).toBeTruthy();
  await userEvent.click(screen.getByRole("tab", { name: "Настройки" }));
  expect(screen.getByRole("tab", { name: "Настройки" }).getAttribute("aria-selected")).toBe("true");
  expect(screen.getByRole("region", { name: "Распознаватель" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: /Собеседник.*Вопрос/ })).toBeNull();
  expect(
    api.execute.mock.calls.some(
      ([c]) => c.question?.action === "models" && c.question.chat_id === "chat",
    ),
  ).toBe(true);
  // The session controls stay in the header on every tab.
  expect(screen.getByRole("button", { name: "Слушать" })).toBeTruthy();
  await userEvent.click(screen.getByRole("tab", { name: "Эфир" }));
  expect(
    screen.getByRole("button", { name: /Собеседник.*Вопрос/ }).getAttribute("aria-pressed"),
  ).toBe("true");
});

it("hides the history tab when history is disabled", async () => {
  api.state.mockResolvedValue({
    question: questionFixture(),
    audio: audioFixture(),
    chat_id: "chat",
    chat_title: "",
    can_send: true,
    disabled: false,
    theme: "dark",
    ui_scale: 100,
    show_version: 1,
    pinned: false,
    docked: false,
    window_error: "",
  });
  api.ready.mockResolvedValue(undefined);
  render(<AudioWindow />);
  expect(await screen.findByText("Чат: Новый чат")).toBeTruthy();
  expect(screen.getAllByRole("tab").map((tab) => tab.textContent)).toEqual(["Эфир", "Настройки"]);
});
