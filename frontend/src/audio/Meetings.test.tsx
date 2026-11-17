// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vite-plus/test";
import { Meetings } from "./Meetings";
const api = vi.hoisted(() => ({ list: vi.fn(), detail: vi.fn(), command: vi.fn(), copy: vi.fn() }));
vi.mock("../bridge/wails", () => ({ meetingBridge: api }));
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});
it("starts only selected sources and keeps cloud processing explicit", async () => {
  api.list.mockResolvedValue({ items: [], notice: "" });
  api.command.mockResolvedValue(undefined);
  render(
    <Meetings
      state={{
        recording_id: "",
        processing_id: "",
        stage: "",
        completed: 0,
        total: 0,
        error: "",
        notice: "",
        revision: 0,
      }}
      listening={false}
    />,
  );
  await userEvent.type(screen.getByRole("textbox", { name: "Название встречи" }), "Weekly");
  await userEvent.click(screen.getByRole("checkbox", { name: "Микрофон" }));
  await userEvent.click(screen.getByRole("button", { name: "Начать запись" }));
  await waitFor(() =>
    expect(api.command).toHaveBeenCalledWith({
      action: "start",
      id: "",
      title: "Weekly",
      tracks: ["system"],
      partial: false,
    }),
  );
  expect(api.command).toHaveBeenCalledTimes(1);
  expect(screen.getByText(/передаётся в Google только/)).toBeTruthy();
});
it("offers stop while main chat or recognition is unavailable", async () => {
  api.list.mockResolvedValue({ items: [], notice: "" });
  api.command.mockResolvedValue(undefined);
  render(
    <Meetings
      state={{
        recording_id: "active",
        processing_id: "",
        stage: "",
        completed: 0,
        total: 0,
        error: "",
        notice: "",
        revision: 1,
      }}
      listening={false}
    />,
  );
  await userEvent.click(screen.getByRole("button", { name: "Остановить запись" }));
  expect(api.command).toHaveBeenCalledWith(expect.objectContaining({ action: "stop" }));
});

it("labels incomplete transcripts and sends partial summary only explicitly", async () => {
  const id = "a".repeat(32);
  api.list.mockResolvedValue({
    items: [{ id, title: "Review", started_at: "2026-11-17T10:00:00Z" }],
    notice: "",
  });
  api.detail.mockResolvedValue({
    id,
    title: "Review",
    status: "interrupted",
    error: "Устройство отключено",
    duration: 12,
    bytes: 1000,
    revision: 2,
    parts: [
      {
        file: "mic-0000.wav",
        track: "mic",
        offset: 0,
        samples: 16000,
        rate: 16000,
        complete: true,
        transcribed: true,
      },
    ],
    segments: [
      {
        id: "seg",
        track: "mic",
        part: "mic-0000.wav",
        speaker: "spk_1",
        start: 0,
        end: 1,
        text: "Проверить релиз",
      },
    ],
    summary: undefined,
  });
  api.command.mockResolvedValue(undefined);
  render(
    <Meetings
      state={{
        recording_id: "",
        processing_id: "",
        stage: "",
        completed: 0,
        total: 0,
        error: "",
        notice: "",
        revision: 1,
      }}
      listening={false}
    />,
  );
  const partial = await screen.findByRole("button", { name: "Summary доступной части" });
  expect(
    screen.getByRole("button", { name: "Сделать summary" }).getAttribute("disabled"),
  ).not.toBeNull();
  expect(api.command).not.toHaveBeenCalled();
  await userEvent.click(partial);
  await waitFor(() =>
    expect(api.command).toHaveBeenCalledWith({
      action: "summary",
      id,
      title: "",
      tracks: [],
      partial: true,
    }),
  );
});

it("shows deferred cleanup separately from recognition errors", async () => {
  api.list.mockResolvedValue({ items: [], notice: "" });
  const state = {
    recording_id: "",
    processing_id: "",
    stage: "",
    completed: 4,
    total: 4,
    error: "",
    notice: "Очистка временных файлов Google отложена.",
    revision: 2,
  };
  const { rerender } = render(<Meetings state={state} listening={false} />);
  expect(await screen.findByRole("status")).toHaveProperty("textContent", state.notice);
  expect(screen.queryByRole("alert")).toBeNull();
  rerender(
    <Meetings
      state={{ ...state, notice: "", processing_id: "active", stage: "cleanup" }}
      listening={false}
    />,
  );
  expect(screen.getByRole("status").textContent).toContain("Удаление временных файлов Google");
  expect(screen.queryByRole("button", { name: "Отменить обработку" })).toBeNull();
});

it("resumes the paused operation with its original meeting and partial flag", async () => {
  api.list.mockResolvedValue({ items: [], notice: "" });
  api.command.mockResolvedValue(undefined);
  render(
    <Meetings
      listening={false}
      state={{
        recording_id: "",
        processing_id: "",
        stage: "paused",
        completed: 1,
        total: 4,
        error: "",
        notice: "",
        revision: 3,
        paused: {
          id: "original-meeting",
          kind: "summary",
          partial: true,
          retry_at: "",
          reason: "Gemini ограничил частоту запросов",
        },
      }}
    />,
  );
  expect(screen.queryByRole("alert")).toBeNull();
  await userEvent.click(screen.getByRole("button", { name: "Продолжить" }));
  expect(api.command).toHaveBeenCalledWith({
    action: "summary",
    id: "original-meeting",
    partial: true,
    title: "",
    tracks: [],
  });
});
it("disables continuation until the server retry time", async () => {
  api.list.mockResolvedValue({ items: [], notice: "" });
  render(
    <Meetings
      listening={false}
      state={{
        recording_id: "",
        processing_id: "",
        stage: "paused",
        completed: 1,
        total: 4,
        error: "",
        notice: "",
        revision: 3,
        paused: {
          id: "original-meeting",
          kind: "transcribe",
          partial: false,
          retry_at: "2099-01-01T00:00:00Z",
          reason: "Gemini ограничил частоту запросов",
        },
      }}
    />,
  );
  expect(screen.getByRole("button", { name: "Продолжить" }).hasAttribute("disabled")).toBe(true);
});

it("offers transcription continuation from archived checkpoints after reopening", async () => {
  const id = "b".repeat(32);
  api.list.mockResolvedValue({
    items: [{ id, title: "Запись", started_at: "2026-11-17T10:00:00Z" }],
    notice: "",
  });
  api.detail.mockResolvedValue({
    id,
    title: "Запись",
    status: "recorded",
    error: "",
    duration: 301,
    bytes: 1000,
    revision: 2,
    parts: [
      {
        file: "system-0000.wav",
        track: "system",
        offset: 0,
        samples: 16000,
        rate: 16000,
        complete: true,
        transcribed: true,
      },
      {
        file: "system-0001.wav",
        track: "system",
        offset: 1,
        samples: 16000,
        rate: 16000,
        complete: true,
        transcribed: false,
      },
    ],
    segments: [],
    summary: undefined,
  });
  api.command.mockResolvedValue(undefined);
  render(
    <Meetings
      listening={false}
      state={{
        recording_id: "",
        processing_id: "",
        stage: "",
        completed: 0,
        total: 0,
        error: "",
        notice: "",
        revision: 0,
      }}
    />,
  );
  await userEvent.click(await screen.findByRole("button", { name: "Продолжить транскрибацию" }));
  expect(api.command).toHaveBeenCalledWith({
    action: "transcribe",
    id,
    partial: false,
    title: "",
    tracks: [],
  });
});
