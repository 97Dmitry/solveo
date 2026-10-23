// @vitest-environment jsdom
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vite-plus/test";
import type { AudioHistoryDocument } from "../bridge/wails";
import { AudioHistory } from "./AudioHistory";
import { AudioPanel, type AudioActions, type ArchivedTranscript } from "./AudioPanel";
import { audioFixture } from "../bridge/test-fixture";
const api = vi.hoisted(() => ({
  list: vi.fn(),
  load: vi.fn(),
  delete: vi.fn(),
  forChat: vi.fn(),
  deleteChat: vi.fn(),
  attach: vi.fn(),
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
function list(...ids: string[]) {
  return {
    sessions: ids.map((id) => ({
      id,
      provider_id: "qwen",
      provider_label: id,
      started_at: "2026-10-23T10:00:00Z",
      ended_at: "2026-10-23T11:00:00Z",
      segment_count: 301,
    })),
    error: "",
  };
}
const historyProps = { chatID: "chat-a", chatTitle: "Первый чат" };
it("opens a merged current-chat timeline without a session chooser and paginates it", async () => {
  api.forChat.mockResolvedValue(document());
  const onArchive = vi.fn<(archive: ArchivedTranscript | undefined) => void>();
  render(<AudioHistory {...historyProps} onArchive={onArchive} />);
  expect(api.forChat).not.toHaveBeenCalled();
  await userEvent.click(screen.getByRole("button", { name: "История чата" }));
  await screen.findByText("Страница 1 из 2");
  expect(api.forChat).toHaveBeenCalledWith("chat-a");
  expect(api.list).not.toHaveBeenCalled();
  expect(screen.queryByRole("combobox")).toBeNull();
  expect(onArchive.mock.lastCall?.[0]?.title).toContain("Первый чат");
  expect(onArchive.mock.lastCall?.[0]?.segments).toHaveLength(300);
  await userEvent.click(screen.getByRole("button", { name: "Следующие фразы" }));
  expect(onArchive.mock.lastCall?.[0]?.segments[0]?.text).toBe("Фраза 300");
  expect(onArchive.mock.lastCall?.[0]?.segments[0]?.provider_label).toBe("Gemini");
  await userEvent.click(screen.getByRole("button", { name: "Предыдущие фразы" }));
  expect(onArchive.mock.lastCall?.[0]?.segments).toHaveLength(300);
});
it("shows empty current-chat history and read errors", async () => {
  api.forChat
    .mockResolvedValueOnce(document("empty", 0))
    .mockRejectedValueOnce(new Error("Не удалось прочитать"));
  render(<AudioHistory {...historyProps} onArchive={vi.fn()} />);
  await userEvent.click(screen.getByRole("button", { name: "История чата" }));
  expect(await screen.findByText("Голосовой истории в этом чате пока нет")).toBeTruthy();
  await userEvent.click(screen.getByRole("button", { name: "Обновить историю чата" }));
  expect(await screen.findByText("Не удалось прочитать")).toBeTruthy();
});
it("reloads for a changed chat, ignores late old responses and cancels on returning to live", async () => {
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
        : Promise.resolve(document("chat-b")),
  );
  const onArchive = vi.fn<(archive: ArchivedTranscript | undefined) => void>();
  const view = render(<AudioHistory {...historyProps} onArchive={onArchive} />);
  await userEvent.click(screen.getByRole("button", { name: "История чата" }));
  view.rerender(<AudioHistory chatID="chat-b" chatTitle="Второй чат" onArchive={onArchive} />);
  await waitFor(() => expect(onArchive.mock.lastCall?.[0]?.id).toBe("chat-b"));
  await act(async () => resolveFirst(document("chat-a")));
  expect(onArchive.mock.lastCall?.[0]?.id).toBe("chat-b");
  expect(onArchive.mock.lastCall?.[0]?.title).toContain("Второй чат");
  view.rerender(<AudioHistory chatID="chat-c" chatTitle="Третий чат" onArchive={onArchive} />);
  await userEvent.click(screen.getByRole("button", { name: "Эфир" }));
  await act(async () => resolveThird(document("chat-c")));
  expect(onArchive.mock.lastCall?.[0]).toBeUndefined();
});
it("offers unassociated legacy entries only on demand and routes legacy deletion separately", async () => {
  api.forChat.mockResolvedValue(document("chat-a"));
  api.list.mockResolvedValue({
    ...list("legacy", "associated"),
    sessions: list("legacy", "associated").sessions.map((session) => ({
      ...session,
      chat_id: session.id === "legacy" ? "" : "chat-a",
    })),
  });
  api.load.mockResolvedValue(document("legacy"));
  api.delete.mockResolvedValue(undefined);
  render(<AudioHistory {...historyProps} onArchive={vi.fn()} />);
  await userEvent.click(screen.getByRole("button", { name: "История чата" }));
  await userEvent.click(screen.getByText("Записи старой версии"));
  await waitFor(() => expect(api.list).toHaveBeenCalled());
  await userEvent.selectOptions(screen.getByLabelText("Старая запись"), "legacy");
  await screen.findByRole("button", { name: "Удалить старую запись" });
  expect(screen.queryByRole("option", { name: /associated/ })).toBeNull();
  await userEvent.click(screen.getByRole("button", { name: "Удалить старую запись" }));
  await userEvent.click(screen.getByRole("button", { name: "Подтвердить удаление" }));
  await waitFor(() => expect(api.delete).toHaveBeenCalledWith("legacy"));
  expect(api.deleteChat).not.toHaveBeenCalled();
});
it("requires confirmation for chat deletion and ignores completion after switching chats", async () => {
  let finishDelete: () => void = () => {};
  api.forChat.mockImplementation((id: string) => Promise.resolve(document(id)));
  api.deleteChat.mockImplementation(
    () =>
      new Promise<void>((resolve) => {
        finishDelete = resolve;
      }),
  );
  const onArchive = vi.fn<(archive: ArchivedTranscript | undefined) => void>();
  const view = render(<AudioHistory {...historyProps} onArchive={onArchive} />);
  await userEvent.click(screen.getByRole("button", { name: "История чата" }));
  await userEvent.click(
    await screen.findByRole("button", { name: "Удалить голосовую историю этого чата" }),
  );
  expect(api.deleteChat).not.toHaveBeenCalled();
  await userEvent.click(screen.getByRole("button", { name: "Отмена" }));
  await userEvent.click(
    screen.getByRole("button", { name: "Удалить голосовую историю этого чата" }),
  );
  await userEvent.click(screen.getByRole("button", { name: "Подтвердить удаление" }));
  expect(api.deleteChat).toHaveBeenCalledWith("chat-a");
  view.rerender(<AudioHistory chatID="chat-b" chatTitle="Второй чат" onArchive={onArchive} />);
  await waitFor(() => expect(onArchive.mock.lastCall?.[0]?.id).toBe("chat-b"));
  await act(async () => finishDelete());
  expect(onArchive.mock.lastCall?.[0]?.id).toBe("chat-b");
  expect(api.delete).not.toHaveBeenCalled();
});
it("attaches a legacy entry only after an explicit action and reloads the current chat", async () => {
  api.forChat.mockResolvedValue(document("chat-a", 0));
  api.list.mockResolvedValue({
    ...list("legacy"),
    sessions: list("legacy").sessions.map((session) => ({ ...session, chat_id: "" })),
  });
  api.load.mockResolvedValue(document("legacy"));
  api.attach.mockResolvedValue(undefined);
  const onArchive = vi.fn<(archive: ArchivedTranscript | undefined) => void>();
  render(<AudioHistory {...historyProps} onArchive={onArchive} />);
  await userEvent.click(screen.getByRole("button", { name: "История чата" }));
  await userEvent.click(screen.getByText("Записи старой версии"));
  await screen.findByRole("option", { name: /legacy/ });
  await userEvent.selectOptions(screen.getByLabelText("Старая запись"), "legacy");
  const attachButton = await screen.findByRole("button", { name: "Привязать к этому чату" });
  expect(api.attach).not.toHaveBeenCalled();
  expect(screen.getByText("Чат для привязки: Первый чат")).toBeTruthy();
  api.forChat.mockResolvedValue(document("chat-a", 2));
  await userEvent.click(attachButton);
  await waitFor(() => expect(api.attach).toHaveBeenCalledWith("legacy", "chat-a"));
  await waitFor(() => expect(onArchive.mock.lastCall?.[0]?.segments).toHaveLength(2));
  expect(onArchive.mock.lastCall?.[0]?.title).toContain("Первый чат");
  expect(screen.queryByRole("button", { name: "Привязать к этому чату" })).toBeNull();
});
it("keeps listening controls usable and makes archive rows readonly without send/insert/clear", async () => {
  const actions: AudioActions = {
    refresh: vi.fn(),
    prepare: vi.fn(),
    startModel: vi.fn(),
    stopModel: vi.fn(),
    startListening: vi.fn(),
    stopListening: vi.fn(),
    clear: vi.fn(),
    send: vi.fn(),
    insert: vi.fn(),
    selectProvider: vi.fn(),
    setKey: vi.fn(),
    deleteKey: vi.fn(),
  };
  render(
    <AudioPanel
      audio={{ ...audioFixture(), model: "ready", segments: document("live", 1).segments }}
      disabled={false}
      canSend
      actions={actions}
      archive={{ id: "old", title: "Архивный сеанс", segments: document("old", 1).segments }}
    />,
  );
  const phrase = screen.getByRole("button", { name: /Фраза 0/ });
  expect(phrase.hasAttribute("disabled")).toBe(true);
  expect(phrase.getAttribute("title")).toBe("Архив: только просмотр");
  expect(screen.getByText("Собеседник · Qwen")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Отправить" })).toBeNull();
  expect(screen.queryByRole("button", { name: "В поле ввода" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Очистить" })).toBeNull();
  await userEvent.click(screen.getByRole("button", { name: "Слушать" }));
  expect(actions.startListening).toHaveBeenCalled();
  expect(actions.send).not.toHaveBeenCalled();
  expect(actions.insert).not.toHaveBeenCalled();
  expect(actions.clear).not.toHaveBeenCalled();
});
