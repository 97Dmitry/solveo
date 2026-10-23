// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { HotkeySettings } from "./HotkeySettings";
import { snapshotFixture } from "../bridge/test-fixture";
afterEach(cleanup);
const defaults = () => snapshotFixture().settings.hotkeys;
function setup(overrides: Partial<Parameters<typeof HotkeySettings>[0]> = {}) {
  const props = {
    hotkeys: defaults(),
    errors: {},
    disabled: false,
    onSave: vi.fn(),
    loadDefaults: vi.fn(async () => defaults()),
    suspend: vi.fn(async () => undefined),
    ...overrides,
  };
  render(<HotkeySettings {...props} />);
  return props;
}
const field = (name: string) => screen.getByRole("button", { name: `Сочетание: ${name}` });
describe("hotkey settings", () => {
  it("records a layout-independent shortcut while global hotkeys are paused", () => {
    const props = setup();
    fireEvent.click(field("Показать / скрыть окно"));
    expect(props.suspend).toHaveBeenCalledWith(true);
    expect(field("Показать / скрыть окно").textContent).toBe("Нажмите сочетание…");
    // Russian layout: key is "р", the physical key is H.
    fireEvent.keyDown(field("Показать / скрыть окно"), {
      key: "р",
      code: "KeyH",
      ctrlKey: true,
      shiftKey: true,
    });
    expect(props.suspend).toHaveBeenLastCalledWith(false);
    expect(field("Показать / скрыть окно").textContent).toBe("Ctrl+Shift+H");
    fireEvent.click(screen.getByRole("button", { name: "Сохранить сочетания" }));
    expect(props.onSave).toHaveBeenCalledWith({ ...defaults(), toggle: "Ctrl+Shift+H" });
  });
  it("requires a modifier and cancels recording on Escape", () => {
    const props = setup();
    fireEvent.click(field("Отправить вопрос и очередь"));
    fireEvent.keyDown(field("Отправить вопрос и очередь"), {
      key: "j",
      code: "KeyJ",
      shiftKey: true,
    });
    expect(screen.getByText("Добавьте Ctrl, Alt или Win")).toBeTruthy();
    fireEvent.keyDown(field("Отправить вопрос и очередь"), { key: "Escape", code: "Escape" });
    expect(field("Отправить вопрос и очередь").textContent).toBe("Ctrl+Alt+Enter");
    expect(props.suspend).toHaveBeenLastCalledWith(false);
  });
  it("explains conflicts, disables actions and restores defaults", async () => {
    const custom = { ...defaults(), "quick-full": "" };
    const props = setup({
      hotkeys: custom,
      errors: { toggle: "Ctrl+Alt+O занято другим приложением" },
    });
    expect(screen.getByText("Не работает: Ctrl+Alt+O занято другим приложением")).toBeTruthy();
    expect(field("Снимок всего экрана и ответ").textContent).toBe("Отключено");
    fireEvent.click(field("Снимок всего экрана и ответ"));
    fireEvent.keyDown(field("Снимок всего экрана и ответ"), {
      key: "s",
      code: "KeyS",
      ctrlKey: true,
      altKey: true,
    });
    expect(
      screen.getByText(
        "Ctrl+Alt+S назначено: «Снимок области и ответ», «Снимок всего экрана и ответ»",
      ),
    ).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "Сохранить сочетания" }).hasAttribute("disabled"),
    ).toBe(true);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Стандартные" }));
      await Promise.resolve();
    });
    expect(field("Снимок всего экрана и ответ").textContent).toBe("Ctrl+Alt+F");
    fireEvent.click(screen.getByRole("button", { name: "Отключить: Показать / скрыть окно" }));
    fireEvent.click(screen.getByRole("button", { name: "Сохранить сочетания" }));
    expect(props.onSave).toHaveBeenCalledWith({ ...defaults(), toggle: "" });
  });
});
