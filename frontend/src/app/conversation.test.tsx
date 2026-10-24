// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vite-plus/test";
import App from "./App";

const transport = vi.hoisted(() => ({
  showAudio: vi.fn(),
  reuseImage: vi.fn(),
  importImage: vi.fn(),
  reorder: vi.fn(),
  listeners: new Map<string, (data: unknown) => void>(),
  submit: vi.fn<() => Promise<string>>(),
  capture: vi.fn(),
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
      audioSnapshot: async () => ({}),
      submitTranscript: async () => "op",
      on: (name, cb) => {
        transport.listeners.set(name, cb);
        return () => {
          transport.listeners.delete(name);
        };
      },
    }),
    onAudioInsert: () => () => {},
    actions: {
      showAudioWindow: transport.showAudio,
      copy: async () => true,
      openLink: () => {},
      reuseImage: transport.reuseImage,
      importImage: transport.importImage,
      reorder: transport.reorder,
      capture: transport.capture,
    },
    loadAccount: async () => ({ signed_in: true, kind: "fake" }),
    loadModels: async () => [],
    defaultHotkeys: async () => snapshotFixture().settings.hotkeys,
    suspendHotkeys: async () => {},
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

it("opens interview profiles from an existing read-only chat", async () => {
  Element.prototype.scrollTo = vi.fn();
  HTMLDialogElement.prototype.showModal = vi.fn(function (this: HTMLDialogElement) {
    this.open = true;
  });
  HTMLDialogElement.prototype.close = vi.fn(function (this: HTMLDialogElement) {
    this.open = false;
  });
  render(<App />);
  await screen.findByRole("textbox", { name: "Вопрос" });
  const fixture = snapshotFixture();
  act(() =>
    transport.listeners.get("solveo:state")?.({
      ...fixture,
      revision: 100,
      provider_id: "legacy",
      controls: {
        ...fixture.controls,
        can_edit_config: false,
        can_submit: false,
        can_capture: false,
        can_switch: true,
      },
    }),
  );
  const button = screen.getByRole("button", { name: "Преднастройка" });
  expect(button).toHaveProperty("disabled", false);
  await userEvent.click(button);
  expect(screen.getByLabelText("Профили подготовки интервью")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Сохранить преднастройку" })).toHaveProperty(
    "disabled",
    true,
  );
});

it("connects composer and historical image actions to the active chat", async () => {
  Element.prototype.scrollTo = vi.fn();
  render(<App />);
  await screen.findByRole("textbox", { name: "Вопрос" });
  const fixture = snapshotFixture();
  act(() =>
    transport.listeners.get("solveo:state")?.({
      ...fixture,
      revision: 200,
      messages: [
        {
          id: "old-question",
          role: "user",
          text: "previous",
          status: "complete",
          config_revision: 1,
          attempts: [],
          attachment_ids: ["old"],
        },
      ],
      images: [{ id: "old", label: "Снимок 1", url: "/attachments/first/old" }],
      queue: [{ id: "new", label: "new", url: "/attachments/first/new" }],
    }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Добавить к вопросу: Снимок 1" }));
  expect(transport.reuseImage).toHaveBeenCalledWith("first", "old");
  const file = new File(["bytes"], "picture.png", { type: "image/png" });
  fireEvent.paste(screen.getByRole("textbox"), { clipboardData: { files: [file] } });
  expect(transport.importImage).toHaveBeenCalledWith("first", file);
  expect(screen.getByText("К вопросу: 1 из 20 изображений")).toBeTruthy();
});

it("opens detached audio without placing a panel in the conversation", async () => {
  render(<App />);
  const button = await screen.findByRole("button", { name: "Распознавание речи" });
  await userEvent.click(button);
  expect(transport.showAudio).toHaveBeenCalled();
  expect(screen.queryByRole("region", { name: "Распознавание речи" })).toBeNull();
  expect(screen.getByRole("textbox", { name: "Вопрос" })).toBeTruthy();
});

it("offers full-screen capture from the snapshot menu", async () => {
  Element.prototype.scrollTo = vi.fn();
  render(<App />);
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Варианты снимка" }));
  await user.click(screen.getByRole("menuitem", { name: "Весь экран и ответ" }));
  expect(transport.capture).toHaveBeenCalledWith("quick-full");
  expect(screen.queryByRole("menuitem")).toBeNull();
});

it("opens help from the header and edits shortcuts on the settings tab", async () => {
  Element.prototype.scrollTo = vi.fn();
  render(<App />);
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Справка и горячие клавиши" }));
  expect(screen.getByRole("heading", { name: "Справка и горячие клавиши" })).toBeTruthy();
  expect(screen.getByText("Отправить новые реплики собеседника")).toBeTruthy();
  await user.click(screen.getByRole("button", { name: "Изменить сочетания" }));
  expect(screen.getByRole("heading", { name: "Настройки Solveo" })).toBeTruthy();
  expect(screen.getByRole("tab", { name: "Клавиши" }).getAttribute("aria-selected")).toBe("true");
});
