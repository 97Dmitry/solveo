// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vite-plus/test";
import { questionFixture } from "../bridge/test-fixture";
import { QuestionCard } from "./QuestionCard";

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
    />,
  );
  await userEvent.click(screen.getByRole("button", { name: "Отправить собранный вопрос" }));
  expect(execute).toHaveBeenCalledWith(
    expect.objectContaining({ action: "send", id: "q", chat_id: "chat", revision: 1 }),
  );
});
it("keeps local edits when polling delivers a late automatic result", async () => {
  const execute = vi.fn().mockResolvedValue(undefined);
  const view = { ...questionFixture(), id: "q", text: "original", enabled: true };
  const rendered = render(
    <QuestionCard view={view} busy={false} canSend={true} execute={execute} />,
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
    <QuestionCard view={view} busy={false} canSend={true} execute={execute} />,
  );
  await userEvent.click(screen.getByRole("button", { name: "Редактировать" }));
  rendered.rerender(
    <QuestionCard
      view={{ ...view, id: "new", text: "new question", editing: false, revision: 9 }}
      busy={false}
      canSend={true}
      execute={execute}
    />,
  );
  const save = screen.getByRole<HTMLButtonElement>("button", { name: "Сохранить правки" });
  expect(save.disabled).toBe(true);
  await userEvent.click(save);
  expect(execute).toHaveBeenCalledTimes(1);
  await userEvent.click(screen.getByRole("button", { name: "Закрыть локальную правку" }));
  expect(screen.getByText("new question")).toBeTruthy();
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
    <QuestionCard
      view={{ ...questionFixture(), models: [model] }}
      busy={false}
      canSend={true}
      execute={execute}
    />,
  );
  await userEvent.click(screen.getByText("Модель сборщика", { selector: "summary" }));
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
    />,
  );
  expect(screen.getByRole("status").textContent).toContain("Черновик вопроса");
  expect(screen.getByText(/Анализ завершён/)).toBeTruthy();
  expect(
    screen.getByRole<HTMLButtonElement>("button", { name: "Отправить собранный вопрос" }).disabled,
  ).toBe(true);
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
    />,
  );
  expect(screen.queryByRole("button", { name: "Подтвердить этот текст" })).toBeNull();
  expect(screen.getByText(/Ожидаю окончательных системных реплик/)).toBeTruthy();
});
