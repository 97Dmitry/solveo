// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { Composer } from "./Composer";
import { AttachmentStrip } from "./AttachmentStrip";
afterEach(cleanup);
const items = ["a", "b", "c"].map((id) => ({ id, label: id, url: `/attachments/chat/${id}` }));
const composerProps = {
  text: "draft stays",
  disabled: false,
  canSend: true,
  busy: false,
  onChange: vi.fn(),
  onSend: vi.fn(),
  onCancel: vi.fn(),
  onComposition: vi.fn(),
};
describe("attachments", () => {
  it("numbers queue and moves by confirmed ID permutation", () => {
    const reorder = vi.fn();
    render(<AttachmentStrip items={items} disabled={false} onOpen={vi.fn()} onReorder={reorder} />);
    expect(screen.getByText("1")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Раньше: a" }).hasAttribute("disabled")).toBe(true);
    expect(screen.getByRole("button", { name: "Позже: c" }).hasAttribute("disabled")).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Раньше: c" }));
    expect(reorder).toHaveBeenCalledWith(["a", "c", "b"]);
  });
  it("adds history image without reordering it", () => {
    const reuse = vi.fn();
    render(<AttachmentStrip items={items} disabled={false} onOpen={vi.fn()} onReuse={reuse} />);
    fireEvent.click(screen.getByRole("button", { name: "Добавить к вопросу: b" }));
    expect(reuse).toHaveBeenCalledWith("b");
    expect(screen.queryByRole("button", { name: "Раньше: a" })).toBeNull();
  });
  it("disables queue mutations while keeping preview usable", () => {
    const open = vi.fn();
    render(
      <AttachmentStrip
        items={items}
        disabled
        onOpen={open}
        onReorder={vi.fn()}
        onReuse={vi.fn()}
      />,
    );
    expect(
      screen.getByRole("button", { name: "Добавить к вопросу: b" }).hasAttribute("disabled"),
    ).toBe(true);
    fireEvent.click(screen.getByTitle("b"));
    expect(open).toHaveBeenCalledWith("b");
  });
  it("imports a file and resets the chooser for reselection", () => {
    const add = vi.fn();
    render(<Composer {...composerProps} onAddImage={add} canAddImage />);
    const picker = screen.getByLabelText("Файл изображения");
    const file = new File(["png"], "picture.png", { type: "image/png" });
    fireEvent.change(picker, { target: { files: [file] } });
    fireEvent.change(picker, { target: { files: [file] } });
    expect(add).toHaveBeenCalledTimes(2);
    fireEvent.change(picker, { target: { files: [] } });
    expect(add).toHaveBeenCalledTimes(2);
  });
  it("pastes image without replacing draft and leaves text paste alone", () => {
    const add = vi.fn();
    render(<Composer {...composerProps} onAddImage={add} canAddImage />);
    const input = screen.getByRole("textbox");
    const file = new File(["png"], "clipboard.png", { type: "image/png" });
    expect(fireEvent.paste(input, { clipboardData: { files: [file] } })).toBe(false);
    expect(add).toHaveBeenCalledWith(file);
    expect(input).toHaveProperty("value", "draft stays");
    expect(fireEvent.paste(input, { clipboardData: { files: [] } })).toBe(true);
  });
  it("rejects multiple files or unavailable queue explicitly", () => {
    const add = vi.fn(),
      fail = vi.fn();
    const file = new File(["png"], "clipboard.png", { type: "image/png" });
    const view = render(
      <Composer {...composerProps} onAddImage={add} onImageError={fail} canAddImage />,
    );
    fireEvent.paste(screen.getByRole("textbox"), { clipboardData: { files: [file, file] } });
    expect(add).not.toHaveBeenCalled();
    expect(fail).toHaveBeenCalledWith(expect.stringContaining("одному"));
    view.rerender(
      <Composer {...composerProps} onAddImage={add} onImageError={fail} canAddImage={false} />,
    );
    fireEvent.paste(screen.getByRole("textbox"), { clipboardData: { files: [file] } });
    expect(add).not.toHaveBeenCalled();
    expect(fail).toHaveBeenCalledTimes(2);
    expect(screen.getByRole("button", { name: "Из файла" }).hasAttribute("disabled")).toBe(true);
  });
});
