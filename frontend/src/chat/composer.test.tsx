// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { Composer } from "./Composer";
afterEach(cleanup);
describe("composer", () => {
  it("sends text without a screenshot", () => {
    const send = vi.fn();
    render(
      <Composer
        text="hello"
        disabled={false}
        canSend
        busy={false}
        onChange={() => {}}
        onSend={send}
        onCancel={() => {}}
        onComposition={() => {}}
      />,
    );
    fireEvent.keyDown(screen.getByRole("textbox"), { key: "Enter" });
    expect(send).toHaveBeenCalledOnce();
  });
  it("does not submit Shift+Enter or IME Enter", () => {
    const send = vi.fn();
    render(
      <Composer
        text="hello"
        disabled={false}
        canSend
        busy={false}
        onChange={() => {}}
        onSend={send}
        onCancel={() => {}}
        onComposition={() => {}}
      />,
    );
    const input = screen.getByRole("textbox");
    fireEvent.keyDown(input, { key: "Enter", shiftKey: true });
    fireEvent.compositionStart(input);
    fireEvent.keyDown(input, { key: "Enter", isComposing: true });
    expect(send).not.toHaveBeenCalled();
  });
});
