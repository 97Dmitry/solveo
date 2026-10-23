// @vitest-environment jsdom
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vite-plus/test";
import { AudioPanel, type AudioActions } from "./AudioPanel";
import { audioFixture } from "../bridge/test-fixture";
import type { AudioState } from "../bridge/state";
afterEach(cleanup);
function actions(): AudioActions {
  return {
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
}
const ok = (id: string) => ({
  id,
  label: id,
  state: "ok" as const,
  message: `${id} готов`,
});
const allOk = (["docker", "engine", "image", "gpu", "model"] as const).map(ok);
function audio(patch: Partial<AudioState>): AudioState {
  return { ...audioFixture(), ...patch };
}
function seg(id: string, text: string, final = true, track: "system" | "mic" = "system") {
  return { id, track, started_at: "2026-10-23T10:00:00Z", text, final };
}
function renderPanel(state: AudioState, a = actions(), canSend = true) {
  render(<AudioPanel audio={state} disabled={false} canSend={canSend} actions={a} />);
  return a;
}

it("checks readiness when opened without status and offers preparation for missing parts", async () => {
  const a = renderPanel(audio({ checks: [] }));
  expect(a.refresh).toHaveBeenCalledTimes(1);
  cleanup();
  const b = renderPanel(
    audio({
      checks: [
        ...allOk.slice(0, 2),
        { id: "image", label: "Образ", state: "missing" as const, message: "Образ не собран" },
      ],
    }),
  );
  expect(screen.getByText("Образ не собран")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Запустить модель" })).toBeNull();
  await userEvent.click(screen.getByRole("button", { name: "Подготовить" }));
  expect(b.prepare).toHaveBeenCalled();
});

it("starts the model only when all checks are ok", async () => {
  const a = renderPanel(audio({ checks: allOk }));
  expect(screen.queryByRole("button", { name: "Подготовить" })).toBeNull();
  await userEvent.click(screen.getByRole("button", { name: "Запустить модель" }));
  expect(a.startModel).toHaveBeenCalled();
});

it("shows progress while busy and hides commands", () => {
  renderPanel(
    audio({
      checks: allOk,
      busy: "starting",
      model: "starting",
      progress: "Загрузка модели в видеопамять",
    }),
  );
  expect(screen.getByText("Загрузка модели в видеопамять")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Запустить модель" })).toBeNull();
});

it("toggles listening and unloads the model only when not listening", async () => {
  const a = renderPanel(audio({ checks: allOk, model: "ready" }));
  await userEvent.click(screen.getByRole("button", { name: "Слушать" }));
  expect(a.startListening).toHaveBeenCalled();
  await userEvent.click(screen.getByRole("button", { name: "Выгрузить модель" }));
  expect(a.stopModel).toHaveBeenCalled();
  cleanup();
  const b = renderPanel(audio({ checks: allOk, model: "ready", listening: true }));
  expect(screen.queryByRole("button", { name: "Выгрузить модель" })).toBeNull();
  await userEvent.click(screen.getByRole("button", { name: "Остановить" }));
  expect(b.stopListening).toHaveBeenCalled();
});

it("labels sources and lets only final phrases be selected", async () => {
  renderPanel(
    audio({
      model: "ready",
      listening: true,
      sources: [
        { track: "system", device: "Наушники", active: true, error: "" },
        { track: "mic", device: "", active: false, error: "устройство звука не найдено" },
      ],
      segments: [seg("a:1", "Готовая фраза"), seg("b:1", "Говорю сейчас", false, "mic")],
    }),
  );
  expect(screen.getByText(/Наушники/)).toBeTruthy();
  expect(screen.getByText(/устройство звука не найдено/)).toBeTruthy();
  const partial = screen.getByRole("button", { name: /Говорю сейчас/ });
  expect(partial.hasAttribute("disabled")).toBe(true);
  const final = screen.getByRole("button", { name: /Готовая фраза/ });
  await userEvent.click(final);
  expect(final.getAttribute("aria-pressed")).toBe("true");
  expect(within(final).getByText("Собеседник")).toBeTruthy();
});

it("sends selected ids in transcript order and clears the selection", async () => {
  const a = renderPanel(
    audio({
      model: "ready",
      segments: [seg("a:1", "Первая"), seg("a:2", "Вторая"), seg("a:3", "Третья")],
    }),
  );
  await userEvent.click(screen.getByRole("button", { name: /Третья/ }));
  await userEvent.click(screen.getByRole("button", { name: /Первая/ }));
  await userEvent.click(screen.getByRole("button", { name: "Отправить" }));
  expect(a.send).toHaveBeenCalledWith(["a:1", "a:3"]);
  expect(screen.getByRole("button", { name: /Первая/ }).getAttribute("aria-pressed")).toBe("false");
});

it("inserts selected text into the composer", async () => {
  const a = renderPanel(audio({ segments: [seg("a:1", "Первая"), seg("a:2", "Вторая")] }));
  await userEvent.click(screen.getByRole("button", { name: /Вторая/ }));
  await userEvent.click(screen.getByRole("button", { name: /Первая/ }));
  await userEvent.click(screen.getByRole("button", { name: "В поле ввода" }));
  expect(a.insert).toHaveBeenCalledWith("Первая\nВторая");
});

it("disables sending while the chat cannot submit", async () => {
  renderPanel(audio({ segments: [seg("a:1", "Первая")] }), actions(), false);
  await userEvent.click(screen.getByRole("button", { name: /Первая/ }));
  expect((screen.getByRole("button", { name: "Отправить" }) as HTMLButtonElement).disabled).toBe(
    true,
  );
  expect((screen.getByRole("button", { name: "В поле ввода" }) as HTMLButtonElement).disabled).toBe(
    false,
  );
});

it("explains when recognition is unavailable", () => {
  renderPanel(audio({ available: false }));
  expect(screen.getByText(/Распознавание речи недоступно/)).toBeTruthy();
});

const grok = {
  id: "grok",
  label: "Grok STT (облако xAI)",
  local: false,
  preparable: false,
  needs_key: true,
  key_label: "API-ключ xAI",
  vendor: "xAI",
  start_label: "Подключить",
  stop_label: "Отключить",
  max_phrases: 100,
  max_phrase_runes: 50,
};
const providers = [
  { id: "qwen", label: "Qwen3-ASR (локально)", local: true },
  { id: "grok", label: "Grok STT (облако xAI)", local: false },
];

it("switches the provider only while recognition is stopped", async () => {
  const a = renderPanel(audio({ providers }));
  await userEvent.selectOptions(screen.getByLabelText("Распознаватель"), "grok");
  expect(a.selectProvider).toHaveBeenCalledWith("grok");
  expect(screen.getByText(/Локально: звук не покидает компьютер/)).toBeTruthy();
  cleanup();
  renderPanel(audio({ providers, model: "ready", listening: true }));
  expect(screen.getByLabelText("Распознаватель").hasAttribute("disabled")).toBe(true);
});

it("marks a cloud provider and takes button wording from it", () => {
  renderPanel(
    audio({
      provider: grok,
      providers,
      key_configured: true,
      checks: [{ id: "key", label: "Ключ xAI", state: "ok", message: "Ключ задан" }],
    }),
  );
  expect(screen.getByText("Облако: звук отправляется в xAI")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Подключить" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Запустить модель" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Подготовить" })).toBeNull();
});

it("stores the key without ever showing the saved one", async () => {
  const a = renderPanel(audio({ provider: grok, providers, key_configured: true }));
  expect(screen.getByText("Ключ сохранён")).toBeTruthy();
  const field = screen.getByLabelText<HTMLInputElement>("API-ключ xAI");
  expect(field.value).toBe("");
  expect(field.type).toBe("password");
  await userEvent.type(field, "xai-new-key");
  await userEvent.click(screen.getByRole("button", { name: "Сохранить ключ" }));
  expect(a.setKey).toHaveBeenCalledWith("grok", "xai-new-key");
  expect(field.value).toBe("");
  await userEvent.click(screen.getByRole("button", { name: "Удалить ключ" }));
  expect(a.deleteKey).toHaveBeenCalledWith("grok");
});
