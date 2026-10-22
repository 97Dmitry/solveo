// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { HistoryDrawer } from "./HistoryDrawer";
import { snapshotFixture } from "../bridge/test-fixture";
import type { SearchHit } from "../bridge/state";
afterEach(cleanup);
beforeEach(() => {
  vi.useRealTimers();
});
function snapshot() {
  const s = snapshotFixture();
  s.history = [
    {
      id: "pinned",
      title: "Закреплённый",
      provider_id: "codex",
      updated_at: "2026-10-20T10:00:00Z",
      prepared: false,
      pinned: true,
    },
    {
      id: "first",
      title: "Текущий",
      provider_id: "codex",
      updated_at: "2026-10-22T10:00:00Z",
      prepared: false,
      pinned: false,
    },
    {
      id: "old",
      title: "Старый",
      provider_id: "codex",
      updated_at: "2026-10-19T10:00:00Z",
      prepared: true,
      pinned: false,
    },
  ];
  return s;
}
function props(overrides: Partial<Parameters<typeof HistoryDrawer>[0]> = {}) {
  return {
    snapshot: snapshot(),
    busy: false,
    onOpen: vi.fn(),
    onNew: vi.fn(),
    onDelete: vi.fn(() => Promise.resolve({ deleted: [], failed: [] })),
    onPin: vi.fn(),
    onExport: vi.fn(() => Promise.resolve(undefined)),
    onReveal: vi.fn(),
    search: vi.fn(() => Promise.resolve<SearchHit[]>([])),
    ...overrides,
  };
}
describe("history drawer", () => {
  it("deletes several chats after one confirmation and reports each failure", async () => {
    const onDelete = vi.fn(() =>
      Promise.resolve({
        deleted: ["old"],
        failed: [{ id: "first", title: "Текущий", error: "файл занят" }],
      }),
    );
    render(<HistoryDrawer {...props({ onDelete })} />);
    fireEvent.click(screen.getByRole("button", { name: "Выбрать" }));
    fireEvent.click(screen.getByRole("checkbox", { name: "Выбрать чат Старый" }));
    fireEvent.click(screen.getByRole("checkbox", { name: "Выбрать чат Текущий" }));
    expect(screen.getByText("Выбрано: 2")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Удалить" }));
    const dialog = screen.getByRole("alertdialog");
    expect(dialog.textContent).toContain(
      "Удалить 2 чата вместе со снимками, черновиками и очередью?",
    );
    expect(dialog.textContent).toContain("Текущий чат будет закрыт");
    expect(onDelete).not.toHaveBeenCalled();
    await act(async () => {
      fireEvent.click(screen.getAllByRole("button", { name: "Удалить" }).at(-1) ?? dialog);
      await Promise.resolve();
    });
    expect(onDelete).toHaveBeenCalledWith(["old", "first"]);
    expect(screen.getByText("Удалено: 1 чат.")).toBeTruthy();
    expect(screen.getByText("Не удалось удалить «Текущий»: файл занят")).toBeTruthy();
    expect(screen.queryByRole("checkbox")).toBeNull();
  });
  it("cancels a single deletion without calling Go", () => {
    const onDelete = vi.fn(() => Promise.resolve(undefined));
    render(<HistoryDrawer {...props({ onDelete })} />);
    fireEvent.click(screen.getByRole("button", { name: "Удалить чат Старый" }));
    expect(screen.getByRole("alertdialog").textContent).toContain("Удалить 1 чат");
    fireEvent.click(screen.getByRole("button", { name: "Отмена" }));
    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(onDelete).not.toHaveBeenCalled();
  });
  it("pins and exports chats", async () => {
    const onPin = vi.fn();
    const onReveal = vi.fn();
    const onExport = vi.fn(() =>
      Promise.resolve({ name: "20261022-100000-Старый", path: "C:\\data\\exports\\x", missing: 1 }),
    );
    render(<HistoryDrawer {...props({ onPin, onExport, onReveal })} />);
    fireEvent.click(screen.getByRole("button", { name: "Открепить чат Закреплённый" }));
    expect(onPin).toHaveBeenCalledWith("pinned", false);
    fireEvent.click(screen.getByRole("button", { name: "Закрепить чат Старый" }));
    expect(onPin).toHaveBeenCalledWith("old", true);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Экспортировать чат Старый" }));
      await Promise.resolve();
    });
    expect(screen.getByText("Экспортировано в C:\\data\\exports\\x")).toBeTruthy();
    expect(screen.getByText(/Недоступных снимков: 1/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Открыть папку" }));
    expect(onReveal).toHaveBeenCalledWith("20261022-100000-Старый");
  });
  it("debounces search and ignores stale answers", async () => {
    vi.useFakeTimers();
    const resolvers: ((hits: SearchHit[]) => void)[] = [];
    const search = vi.fn(
      () =>
        new Promise<SearchHit[]>((resolve) => {
          resolvers.push(resolve);
        }),
    );
    const onOpen = vi.fn();
    render(<HistoryDrawer {...props({ search, onOpen })} />);
    const input = screen.getByRole("searchbox", { name: "Поиск по чатам" });
    fireEvent.change(input, { target: { value: "go" } });
    act(() => {
      vi.advanceTimersByTime(100);
    });
    fireEvent.change(input, { target: { value: "gomaxprocs" } });
    act(() => {
      vi.advanceTimersByTime(250);
    });
    expect(search).toHaveBeenCalledTimes(1);
    expect(search).toHaveBeenCalledWith("gomaxprocs");
    fireEvent.change(input, { target: { value: "kafka" } });
    act(() => {
      vi.advanceTimersByTime(250);
    });
    await act(async () => {
      resolvers[0]?.([
        {
          chat_id: "old",
          title: "Старый",
          snippet: "stale",
          matches: 1,
          pinned: false,
          updated_at: "2026-10-19T10:00:00Z",
        },
      ]);
      resolvers[1]?.([
        {
          chat_id: "first",
          title: "Текущий",
          snippet: "…про kafka…",
          matches: 2,
          pinned: false,
          updated_at: "2026-10-22T10:00:00Z",
        },
      ]);
      await Promise.resolve();
    });
    expect(screen.queryByText("stale")).toBeNull();
    expect(screen.getByText("…про kafka…")).toBeTruthy();
    expect(screen.queryByText("Закреплённый")).toBeNull();
    fireEvent.click(screen.getByText("Текущий"));
    expect(onOpen).toHaveBeenCalledWith("first");
    fireEvent.change(input, { target: { value: "" } });
    expect(screen.getByText("Закреплённый")).toBeTruthy();
  });
});
