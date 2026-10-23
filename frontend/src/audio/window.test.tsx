// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vite-plus/test";
import { audioFixture, questionFixture } from "../bridge/test-fixture";
import { AudioWindow } from "./AudioWindow";
const api = vi.hoisted(() => ({ state: vi.fn(), ready: vi.fn(), execute: vi.fn() }));
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
