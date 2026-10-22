// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { createRef } from "react";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { ChatSearch } from "./ChatSearch";
import { findRanges } from "./search";
afterEach(cleanup);
function conversation() {
  const root = document.createElement("div");
  root.innerHTML = `
    <article><div class="message-meta"><span>Вы</span></div><p>Как работает Kafka?</p></article>
    <article><div class="message-meta"><span>Solveo</span><button>Скопировать Kafka</button></div>
      <p>В <strong>Kafka</strong> сообщения хранятся в партициях. kafka масштабируется.</p></article>`;
  document.body.append(root);
  return root;
}
describe("conversation search", () => {
  it("finds case-insensitive matches in message text only", () => {
    const root = conversation();
    const ranges = findRanges(root, "KAFKA");
    expect(ranges.map((r) => r.toString())).toEqual(["Kafka", "Kafka", "kafka"]);
    expect(findRanges(root, "")).toEqual([]);
    expect(findRanges(root, "Solveo")).toEqual([]);
    root.remove();
  });
  it("counts and navigates matches with Enter, Shift+Enter and closes on Escape", () => {
    const root = conversation();
    const ref = createRef<HTMLElement>();
    ref.current = root;
    const onClose = vi.fn();
    render(<ChatSearch focus={1} root={ref} content="1" onClose={onClose} />);
    const input = screen.getByRole("searchbox", { name: "Поиск в разговоре" });
    expect(document.activeElement).toBe(input);
    fireEvent.change(input, { target: { value: "kafka" } });
    expect(screen.getByText("1 из 3")).toBeTruthy();
    fireEvent.keyDown(input, { key: "Enter" });
    expect(screen.getByText("2 из 3")).toBeTruthy();
    fireEvent.keyDown(input, { key: "Enter", shiftKey: true });
    fireEvent.keyDown(input, { key: "Enter", shiftKey: true });
    expect(screen.getByText("3 из 3")).toBeTruthy();
    fireEvent.change(input, { target: { value: "нет такого" } });
    expect(screen.getByText("Не найдено")).toBeTruthy();
    fireEvent.keyDown(input, { key: "Escape" });
    expect(onClose).toHaveBeenCalled();
    root.remove();
  });
});
