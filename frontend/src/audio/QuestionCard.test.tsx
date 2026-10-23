// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vite-plus/test";
import { questionFixture } from "../bridge/test-fixture";
import { QuestionCard } from "./QuestionCard";
import { QuestionSettings } from "./QuestionSettings";

afterEach(cleanup);
it("sends exactly the displayed card identity and revision", async () => {
  const execute = vi.fn().mockResolvedValue(undefined);
  render(
    <QuestionCard
      view={{
        ...questionFixture(),
        id: "q",
        text: "Explain channels",
        can_send: true,
        status: "ready",
      }}
      busy={false}
      canSend={true}
      execute={execute}
      onOpenSettings={vi.fn()}
    />,
  );
  await userEvent.click(screen.getByRole("button", { name: "Отправить вопрос" }));
  expect(execute).toHaveBeenCalledWith(
    expect.objectContaining({ action: "send", id: "q", chat_id: "chat", revision: 1 }),
  );
});
it("keeps local edits when polling delivers a late automatic result", async () => {
  const execute = vi.fn().mockResolvedValue(undefined);
  const view = { ...questionFixture(), id: "q", text: "original", enabled: true };
  const rendered = render(
    <QuestionCard
      view={view}
      busy={false}
      canSend={true}
      execute={execute}
      onOpenSettings={vi.fn()}
    />,
  );
  await userEvent.click(screen.getByRole("button", { name: "Редактировать" }));
  const input = screen.getByRole("textbox", { name: "Текст собранного вопроса" });
  await userEvent.clear(input);
  await userEvent.type(input, "my question");
  rendered.rerender(
    <QuestionCard
      view={{ ...view, text: "late model text", revision: 2, editing: true }}
      busy={false}
      canSend={true}
      execute={execute}
      onOpenSettings={vi.fn()}
    />,
  );
  expect(screen.getByRole<HTMLTextAreaElement>("textbox").value).toBe("my question");
  await userEvent.click(screen.getByRole("button", { name: "Сохранить правки" }));
  expect(execute).toHaveBeenLastCalledWith(
    expect.objectContaining({ action: "save", text: "my question", revision: 2 }),
  );
});
it("requires confirmation before rebuilding a manual version", async () => {
  const execute = vi.fn().mockResolvedValue(undefined);
  render(
    <QuestionCard
      view={{ ...questionFixture(), id: "q", text: "mine", manual: true, enabled: true }}
      busy={false}
      canSend={true}
      execute={execute}
      onOpenSettings={vi.fn()}
    />,
  );
  await userEvent.click(screen.getByRole("button", { name: "Пересобрать" }));
  expect(execute).not.toHaveBeenCalled();
  await userEvent.click(screen.getByRole("button", { name: "Подтвердить пересборку" }));
  expect(execute).toHaveBeenCalledWith(
    expect.objectContaining({ action: "rebuild", confirm: true }),
  );
});

it("does not apply an old local edit to the next card after an external skip", async () => {
  const execute = vi.fn().mockResolvedValue(undefined);
  const view = { ...questionFixture(), id: "old", text: "old question", editing: true };
  const rendered = render(
    <QuestionCard
      view={view}
      busy={false}
      canSend={true}
      execute={execute}
      onOpenSettings={vi.fn()}
    />,
  );
  await userEvent.click(screen.getByRole("button", { name: "Редактировать" }));
  rendered.rerender(
    <QuestionCard
      view={{ ...view, id: "new", text: "new question", editing: false, revision: 9 }}
      busy={false}
      canSend={true}
      execute={execute}
      onOpenSettings={vi.fn()}
    />,
  );
  const save = screen.getByRole<HTMLButtonElement>("button", { name: "Сохранить правки" });
  expect(save.disabled).toBe(true);
  await userEvent.click(save);
  expect(execute).toHaveBeenCalledTimes(1);
  await userEvent.click(screen.getByRole("button", { name: "Закрыть локальную правку" }));
  expect(screen.getByText("new question")).toBeTruthy();
});

it("shows a completed model draft as awaiting review and confirms without sending", async () => {
  const execute = vi.fn().mockResolvedValue(undefined);
  render(
    <QuestionCard
      view={{
        ...questionFixture(),
        id: "q",
        text: "Что такое map и как работает поиск?",
        status: "draft",
        can_confirm: true,
      }}
      busy={false}
      canSend={true}
      execute={execute}
      onOpenSettings={vi.fn()}
    />,
  );
  expect(screen.getByRole("status").textContent).toContain("Черновик вопроса");
  expect(screen.getByText(/Анализ завершён/)).toBeTruthy();
  expect(screen.getByRole<HTMLButtonElement>("button", { name: "Отправить вопрос" }).disabled).toBe(
    true,
  );
  await userEvent.click(screen.getByRole("button", { name: "Подтвердить этот текст" }));
  expect(execute).toHaveBeenCalledTimes(1);
  expect(execute).toHaveBeenCalledWith(
    expect.objectContaining({ action: "confirm", id: "q", chat_id: "chat", revision: 1 }),
  );
});
it("does not offer confirmation while new system speech is unresolved", () => {
  render(
    <QuestionCard
      view={{
        ...questionFixture(),
        id: "q",
        text: "question",
        status: "draft",
        can_confirm: false,
        unincluded: true,
      }}
      busy={false}
      canSend={true}
      execute={vi.fn()}
      onOpenSettings={vi.fn()}
    />,
  );
  expect(screen.queryByRole("button", { name: "Подтвердить этот текст" })).toBeNull();
  expect(screen.getByText(/Ожидаю окончательных системных реплик/)).toBeTruthy();
});

it("uses only the selected model's advertised efforts and saves its default", async () => {
  const execute = vi.fn().mockResolvedValue(undefined);
  const model = {
    id: "chosen",
    label: "Chosen model",
    description: "",
    is_default: false,
    default_reasoning: "low",
    reasoning_efforts: [
      { id: "low", description: "fast" },
      { id: "high", description: "deep" },
    ],
  };
  render(
    <QuestionSettings
      view={{ ...questionFixture(), models: [model] }}
      busy={false}
      execute={execute}
    />,
  );
  await userEvent.selectOptions(
    screen.getByRole("combobox", { name: "Модель сборщика" }),
    "chosen",
  );
  expect(screen.getByRole<HTMLSelectElement>("combobox", { name: "Reasoning effort" }).value).toBe(
    "low",
  );
  await userEvent.click(screen.getByRole("button", { name: "Сохранить модель сборщика" }));
  expect(execute).toHaveBeenLastCalledWith(
    expect.objectContaining({ action: "configure", model: "chosen", reasoning: "low" }),
  );
});
it("collapses a disabled builder to one line and enables it only with a model", async () => {
  const execute = vi.fn().mockResolvedValue(undefined);
  const onOpenSettings = vi.fn();
  const view = render(
    <QuestionCard
      view={questionFixture()}
      busy={false}
      canSend={true}
      execute={execute}
      onOpenSettings={onOpenSettings}
    />,
  );
  expect(screen.getByRole("status").textContent).toBe("Сборщик вопросов выключен");
  expect(screen.queryByRole("button", { name: "Пересобрать" })).toBeNull();
  await userEvent.click(screen.getByRole("button", { name: "Выбрать модель" }));
  expect(onOpenSettings).toHaveBeenCalled();
  expect(execute).not.toHaveBeenCalled();
  view.rerender(
    <QuestionCard
      view={{ ...questionFixture(), model: "chosen" }}
      busy={false}
      canSend={true}
      execute={execute}
      onOpenSettings={onOpenSettings}
    />,
  );
  await userEvent.click(screen.getByRole("button", { name: "Включить сборщик" }));
  expect(execute).toHaveBeenCalledWith(expect.objectContaining({ action: "enable" }));
});
it("toggles the builder from settings", async () => {
  const execute = vi.fn().mockResolvedValue(undefined);
  render(
    <QuestionSettings
      view={{ ...questionFixture(), enabled: true, model: "chosen" }}
      busy={false}
      execute={execute}
    />,
  );
  const toggle = screen.getByRole("button", { name: "Выключить сборщик" });
  expect(toggle.getAttribute("aria-pressed")).toBe("true");
  await userEvent.click(toggle);
  expect(execute).toHaveBeenCalledWith(
    expect.objectContaining({ action: "disable", chat_id: "chat", revision: 1 }),
  );
});
