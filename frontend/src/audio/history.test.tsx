// @vitest-environment jsdom
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vite-plus/test";
import type { AudioHistoryDocument } from "../bridge/wails";
import { AudioHistory } from "./AudioHistory";
const api = vi.hoisted(() => ({
  forChat: vi.fn(),
  deleteChat: vi.fn(),
}));
vi.mock("../bridge/wails", () => ({ audioHistory: api }));
afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});
function document(id = "session", count = 301): AudioHistoryDocument {
  return {
    id,
    chat_id: "chat-a",
    provider_id: "qwen",
    provider_label: "Qwen",
    started_at: "2026-10-23T10:00:00Z",
    ended_at: "2026-10-23T11:00:00Z",
    dictionary: ["Solveo"],
    sources: [],
    errors: [{ at: "2026-10-23T10:01:00Z", track: "mic", message: "Тестовая ошибка" }],
    submissions: [],
    segments: Array.from({ length: count }, (_, index) => ({
      id: `${id}:${index}`,
      track: "system",
      text: `Фраза ${index}`,
      provider_label: index < 150 ? "Qwen" : "Gemini",
      final: true,
      started_at: "2026-10-23T10:00:00Z",
    })),
  };
}
const rows = () => screen.queryAllByRole("listitem").filter((li) => li.matches(".history-row"));
it("loads the merged current-chat timeline on open and pages it from the latest phrases", async () => {
  api.forChat.mockResolvedValue(document());
  render(<AudioHistory chatID="chat-a" />);
  await screen.findByText("Страница 2 из 2");
  expect(api.forChat).toHaveBeenCalledWith("chat-a");
  expect(screen.getByText(/^301 фраза/)).toBeTruthy();
  const metadata = screen.getByText(/^Подробности/).closest("details");
  expect(metadata?.hasAttribute("open")).toBe(false);
  expect(screen.getByText(/Фраз: 301/).closest("details")).toBe(metadata);
  expect(screen.getByText("Подробности · ошибок: 1")).toBeTruthy();
  expect(screen.queryByRole("combobox")).toBeNull();
  expect(rows()).toHaveLength(1);
  expect(rows()[0]?.textContent).toContain("Фраза 300");
  expect(rows()[0]?.textContent).toContain("Собеседник · Gemini");
  // Archive rows are plain text, never selectable phrase buttons.
  expect(screen.queryByRole("button", { name: /Фраза/ })).toBeNull();
  await userEvent.click(screen.getByRole("button", { name: "Предыдущие фразы" }));
  expect(rows()).toHaveLength(300);
  expect(rows()[0]?.textContent).toContain("Фраза 0");
  await userEvent.click(screen.getByRole("button", { name: "Следующие фразы" }));
  expect(rows()).toHaveLength(1);
});
it("filters the whole timeline by text, highlights matches and marks sent phrases", async () => {
  const doc = document("chat-a", 30);
  doc.submissions = [
    {
      at: "2026-10-23T10:05:00Z",
      chat_id: "chat-a",
      operation_id: "op",
      segment_ids: ["chat-a:12"],
    },
  ];
  api.forChat.mockResolvedValue(doc);
  render(<AudioHistory chatID="chat-a" />);
  await screen.findByText(/^30 фраз /);
  await userEvent.type(screen.getByRole("searchbox", { name: "Поиск по истории" }), "фраза 1");
  await waitFor(() => expect(screen.getByText(/Найдено: 11 из 30/)).toBeTruthy());
  expect(rows()).toHaveLength(11);
  expect(rows()[0]?.querySelector("mark")?.textContent).toBe("Фраза 1");
  expect(screen.getAllByText("отправлено в чат")).toHaveLength(1);
  await userEvent.clear(screen.getByRole("searchbox"));
  await userEvent.type(screen.getByRole("searchbox"), "нет такого");
  expect(await screen.findByText("Ничего не найдено")).toBeTruthy();
});
it("shows empty current-chat history and read errors", async () => {
  api.forChat
    .mockResolvedValueOnce(document("empty", 0))
    .mockRejectedValueOnce(new Error("Не удалось прочитать"));
  render(<AudioHistory chatID="chat-a" />);
  expect(await screen.findByText("Голосовой истории в этом чате пока нет")).toBeTruthy();
  await userEvent.click(screen.getByRole("button", { name: "Обновить историю чата" }));
  expect(await screen.findByText("Не удалось прочитать")).toBeTruthy();
});
it("reloads for a changed chat and ignores late responses for old or closed chats", async () => {
  let resolveFirst: (value: AudioHistoryDocument) => void = () => {};
  let resolveThird: (value: AudioHistoryDocument) => void = () => {};
  api.forChat.mockImplementation((id: string) =>
    id === "chat-a"
      ? new Promise<AudioHistoryDocument>((resolve) => {
          resolveFirst = resolve;
        })
      : id === "chat-c"
        ? new Promise<AudioHistoryDocument>((resolve) => {
            resolveThird = resolve;
          })
        : Promise.resolve(document("chat-b", 2)),
  );
  const view = render(<AudioHistory chatID="chat-a" />);
  view.rerender(<AudioHistory chatID="chat-b" />);
  await waitFor(() => expect(rows()[0]?.textContent).toContain("Фраза 0"));
  await act(async () => resolveFirst(document("chat-a", 5)));
  expect(rows()).toHaveLength(2);
  view.rerender(<AudioHistory chatID="chat-c" />);
  await waitFor(() => expect(api.forChat).toHaveBeenCalledWith("chat-c"));
  view.unmount();
  await act(async () => resolveThird(document("chat-c")));
  expect(rows()).toHaveLength(0);
});
it("requires confirmation for chat deletion and ignores completion after switching chats", async () => {
  let finishDelete: () => void = () => {};
  api.forChat.mockImplementation((id: string) => Promise.resolve(document(id, 3)));
  api.deleteChat.mockImplementation(
    () =>
      new Promise<void>((resolve) => {
        finishDelete = resolve;
      }),
  );
  const view = render(<AudioHistory chatID="chat-a" />);
  await userEvent.click(await screen.findByText(/^Подробности/));
  await userEvent.click(
    screen.getByRole("button", { name: "Удалить голосовую историю этого чата" }),
  );
  expect(api.deleteChat).not.toHaveBeenCalled();
  await userEvent.click(screen.getByRole("button", { name: "Отмена" }));
  await userEvent.click(
    screen.getByRole("button", { name: "Удалить голосовую историю этого чата" }),
  );
  await userEvent.click(screen.getByRole("button", { name: "Подтвердить удаление" }));
  expect(api.deleteChat).toHaveBeenCalledWith("chat-a");
  view.rerender(<AudioHistory chatID="chat-b" />);
  await waitFor(() => expect(api.forChat).toHaveBeenCalledWith("chat-b"));
  await waitFor(() => expect(rows()).toHaveLength(3));
  await act(async () => finishDelete());
  expect(rows()).toHaveLength(3);
  expect(rows()[0]?.textContent).toContain("Фраза 0");
});
