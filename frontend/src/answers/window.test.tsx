// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, it, vi } from "vite-plus/test";
import { AnswersWindow } from "./AnswersWindow";

const api = vi.hoisted(() => ({
  state: vi.fn(),
  ready: vi.fn(),
  answer: vi.fn(),
  command: vi.fn(),
  copy: vi.fn(),
  openLink: vi.fn(),
}));
vi.mock("../bridge/wails", () => ({ answersWindow: api }));

const first = {
  id: "first",
  question: "Как работают транзакции?",
  status: "complete",
  revised: false,
  revision: "attempt-1",
  revision_attempt: false,
};
const second = {
  id: "second",
  question: "Что такое уровни изоляции?",
  status: "requesting",
  revised: false,
  revision: "attempt-2",
  revision_attempt: false,
};
const firstAnswer = {
  session_id: "session",
  id: first.id,
  question: first.question,
  text: "**Атомарность** записи",
  status: first.status,
  revision: first.revision,
  error: "",
  saved_answer: true,
};
const secondAnswer = {
  session_id: "session",
  id: second.id,
  question: second.question,
  text: "**Текущий фрагмент** ответа",
  status: second.status,
  revision: second.revision,
  error: "",
  saved_answer: false,
};
const base = {
  theme: "dark",
  ui_scale: 100,
  pinned: false,
  answers: {
    session_id: "session",
    state: "listening",
    running: true,
    available: true,
    busy: false,
    error: "",
    items: [first],
    sessions: [
      { id: "session", title: "Текущая" },
      { id: "saved", title: "Прошлая" },
    ],
  },
};

beforeEach(() => {
  api.state.mockResolvedValue(base);
  api.ready.mockResolvedValue(undefined);
  api.answer.mockImplementation(async (_session: string, id: string) =>
    id === first.id ? firstAnswer : secondAnswer,
  );
  api.command.mockResolvedValue(undefined);
  api.copy.mockResolvedValue(true);
});

it("controls startup, setup and listening from the parent player state", async () => {
  const show = (answers: Partial<typeof base.answers> & { setup_hint?: string }) =>
    api.state.mockResolvedValue({ ...base, answers: { ...base.answers, ...answers } });
  show({ state: "setup", running: false, setup_hint: "Выберите модель сборщика", busy: true });
  render(<AnswersWindow />);
  expect(await screen.findByText("Выберите модель сборщика")).toBeTruthy();
  await userEvent.click(screen.getByRole("button", { name: "Настройки аудио" }));
  expect(api.command).toHaveBeenCalledWith("openAudioSettings", "session");

  show({ state: "paused", running: false });
  await userEvent.click(await screen.findByRole("button", { name: "Запустить автоответы" }));
  expect(api.command).toHaveBeenCalledWith("startAnswers", "session");

  show({ state: "starting_model", running: false, busy: true });
  await userEvent.click(await screen.findByRole("button", { name: "Отменить запуск" }));
  expect(api.command).toHaveBeenCalledWith("pauseAnswers", "session");
  expect(screen.getByText("Запускаю модель")).toBeTruthy();

  show({ state: "analyzing", busy: true });
  expect(await screen.findByText("Собираю вопрос")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Пауза" }).hasAttribute("disabled")).toBe(false);

  show({ state: "error", running: false, error: "Ошибка подключения к распознавателю" });
  expect((await screen.findByRole("alert")).textContent).toBe(
    "Ошибка подключения к распознавателю",
  );
  await userEvent.click(screen.getByRole("button", { name: "Повторить запуск" }));
  expect(api.command).toHaveBeenLastCalledWith("startAnswers", "session");
});

it("keeps saved sessions readable when automatic answers are unavailable", async () => {
  api.state.mockResolvedValue({
    ...base,
    answers: { ...base.answers, state: "unavailable", available: false, running: false },
  });
  render(<AnswersWindow />);
  expect(await screen.findByText("Атомарность")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Новая сессия" }).hasAttribute("disabled")).toBe(true);
  expect(screen.queryByRole("button", { name: "Запустить автоответы" })).toBeNull();
  await userEvent.selectOptions(
    screen.getByRole("combobox", { name: "Сессия автоответов" }),
    "saved",
  );
  expect(api.command).toHaveBeenCalledWith("openAnswers", "saved");
});

afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});

it("keeps the open answer and its scroll position when a new question arrives", async () => {
  render(<AnswersWindow />);
  expect(await screen.findByText("Атомарность")).toBeTruthy();
  expect(api.answer).toHaveBeenCalledWith("session", "first");

  const list = screen.getByRole("list", { name: "Вопросы" });
  const viewport = screen.getByRole("region", { name: "Вопросы и ответы" });
  const oldRow = screen.getByRole("listitem");
  vi.spyOn(oldRow, "offsetTop", "get").mockImplementation(() =>
    list.children.length > 1 ? 140 : 90,
  );
  viewport.scrollTop = 24;
  api.state.mockResolvedValue({ ...base, answers: { ...base.answers, items: [first, second] } });

  const jump = await screen.findByRole("button", { name: "К новому вопросу" });
  expect(viewport.scrollTop).toBe(74);
  expect(screen.getAllByRole("listitem")[1]).toBe(oldRow);
  expect(screen.getByText("Атомарность")).toBeTruthy();
  expect(api.answer).not.toHaveBeenCalledWith("session", "second");

  await userEvent.click(jump);
  expect(viewport.scrollTop).toBe(0);
  expect(await screen.findByText("Текущий фрагмент")).toBeTruthy();
  expect(screen.queryByText("Атомарность")).toBeNull();
  expect(screen.queryByRole("button", { name: "Скопировать ответ" })).toBeNull();
});

it("updates an open card in place and copies its completed answer", async () => {
  render(<AnswersWindow />);
  expect(await screen.findByText("Атомарность")).toBeTruthy();
  const oldRow = screen.getByRole("listitem");
  await waitFor(() => expect(api.state.mock.calls.length).toBeGreaterThan(1));
  expect(api.answer).toHaveBeenCalledTimes(1);
  const revised = {
    ...first,
    question: "Как транзакции предотвращают гонки?",
    revised: true,
    revision: "attempt-3",
  };
  let resolvePreview: (value: typeof firstAnswer) => void = () => undefined;
  api.answer.mockImplementationOnce(
    () =>
      new Promise<typeof firstAnswer>((resolve) => {
        resolvePreview = resolve;
      }),
  );
  api.state.mockResolvedValue({
    ...base,
    answers: {
      ...base.answers,
      items: [{ ...revised, status: "refining", revision_attempt: true }],
    },
  });
  expect(await screen.findByText("Уточняется")).toBeTruthy();
  expect(screen.getByText("Атомарность")).toBeTruthy();
  expect(screen.getByText("Последний готовый ответ")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Скопировать ответ" })).toBeTruthy();
  await waitFor(() => expect(api.answer).toHaveBeenCalledTimes(2));
  resolvePreview({
    ...firstAnswer,
    status: "refining",
    revision: revised.revision,
    text: "**Черновик уточнения**",
    saved_answer: false,
  });
  expect(await screen.findByText("Черновик уточнения")).toBeTruthy();
  expect(screen.queryByText("Атомарность")).toBeNull();
  expect(screen.queryByRole("button", { name: "Скопировать ответ" })).toBeNull();
  api.state.mockResolvedValue({
    ...base,
    answers: { ...base.answers, items: [revised] },
  });
  api.answer.mockResolvedValue({
    ...firstAnswer,
    question: revised.question,
    revision: revised.revision,
    text: "**Изоляция** запросов",
  });
  expect(await screen.findByText("Изоляция")).toBeTruthy();
  const question = await screen.findByRole("button", {
    name: /Как транзакции предотвращают гонки/,
  });
  expect(question.getAttribute("aria-expanded")).toBe("true");
  expect(screen.getByRole("listitem")).toBe(oldRow);
  await userEvent.click(screen.getByRole("button", { name: "Скопировать ответ" }));
  expect(api.copy).toHaveBeenCalledWith("**Изоляция** запросов");
  expect(await screen.findByRole("button", { name: "Ответ скопирован" })).toBeTruthy();
});

it("inserts only the card reference and sends session controls against the displayed session", async () => {
  render(<AnswersWindow />);
  await userEvent.click(await screen.findByRole("button", { name: "В поле ввода чата" }));
  expect(api.command).toHaveBeenCalledWith("insertAnswer", "session", "first");
  await userEvent.click(screen.getByRole("button", { name: "Пауза" }));
  expect(api.command).toHaveBeenCalledWith("pauseAnswers", "session");
  await userEvent.click(screen.getByRole("button", { name: "Закрепить поверх окон" }));
  expect(api.command).toHaveBeenCalledWith("pinAnswers", "session");
  await userEvent.selectOptions(
    screen.getByRole("combobox", { name: "Сессия автоответов" }),
    "saved",
  );
  expect(api.command).toHaveBeenCalledWith("openAnswers", "saved");
  await userEvent.click(screen.getByRole("button", { name: "Новая сессия" }));
  expect(api.command).toHaveBeenCalledWith("newAnswers", "session");
});

it("drops detail from a previous session and offers retry for a failed answer", async () => {
  let resolvePrevious: (value: typeof firstAnswer) => void = () => undefined;
  api.answer.mockImplementation((session: string) =>
    session === "session"
      ? new Promise<typeof firstAnswer>((resolve) => {
          resolvePrevious = resolve;
        })
      : Promise.resolve({
          ...firstAnswer,
          session_id: "saved",
          text: "",
          status: "failed",
          error: "Соединение потеряно",
          saved_answer: false,
        }),
  );
  render(<AnswersWindow />);
  await waitFor(() => expect(api.answer).toHaveBeenCalledWith("session", "first"));
  api.state.mockResolvedValue({
    ...base,
    answers: {
      ...base.answers,
      session_id: "saved",
      state: "paused",
      running: false,
      items: [{ ...first, status: "failed" }],
    },
  });
  expect(await screen.findByText("Соединение потеряно")).toBeTruthy();
  resolvePrevious(firstAnswer);
  await userEvent.click(screen.getByRole("button", { name: "Повторить" }));
  expect(api.command).toHaveBeenCalledWith("retryAnswer", "saved", "first", first.revision);
  expect(screen.queryByText("Атомарность")).toBeNull();
});

it("allows pausing work in progress and keeps save recovery available during that work", async () => {
  const pending = {
    ...base.answers,
    busy: true,
    items: [{ ...first, status: "pending" }],
  };
  api.state.mockResolvedValue({ ...base, answers: pending });
  api.answer.mockResolvedValue({
    ...firstAnswer,
    status: "pending",
    error: "Не удалось сохранить",
    saved_answer: false,
  });
  render(<AnswersWindow />);
  await userEvent.click(await screen.findByRole("button", { name: "Пауза" }));
  expect(api.command).toHaveBeenCalledWith("pauseAnswers", "session");
  expect(screen.getByRole("button", { name: "Новая сессия" }).hasAttribute("disabled")).toBe(true);
  api.state.mockResolvedValue({
    ...base,
    answers: { ...pending, state: "paused", running: false },
  });
  const start = await screen.findByRole("button", { name: "Запустить автоответы" });
  expect(start.hasAttribute("disabled")).toBe(true);
  await userEvent.click(screen.getByRole("button", { name: "Сохранить снова" }));
  expect(api.command).toHaveBeenCalledWith("retryAnswer", "session", "first", first.revision);
  await userEvent.click(screen.getByRole("button", { name: "Отбросить" }));
  expect(api.command).toHaveBeenCalledTimes(2);
  await userEvent.click(screen.getByRole("button", { name: "Отбросить ответ" }));
  expect(api.command).toHaveBeenCalledWith("discardAnswer", "session", "first", first.revision);
});

it("retains the saved answer and restores a failed or unsaved revision on an older card", async () => {
  const older = { ...first, status: "failed", revision: "attempt-3", revision_attempt: true };
  const newer = { ...second, status: "complete" };
  const answers = { ...base.answers, items: [older, newer] };
  const failed = {
    ...firstAnswer,
    status: "failed",
    revision: older.revision,
    error: "Не удалось уточнить",
  };
  api.state.mockResolvedValue({ ...base, answers });
  api.answer.mockImplementation(async (_session: string, id: string) =>
    id === older.id ? failed : { ...secondAnswer, status: "complete", saved_answer: true },
  );
  render(<AnswersWindow />);
  await userEvent.click(await screen.findByRole("button", { name: /Как работают транзакции/ }));
  expect(await screen.findByText("Не удалось уточнить")).toBeTruthy();
  expect(screen.getByText("Последний готовый ответ")).toBeTruthy();
  expect(screen.getByText("Атомарность").closest(".streaming")).toBeNull();
  await userEvent.click(screen.getByRole("button", { name: "Скопировать ответ" }));
  expect(api.copy).toHaveBeenCalledWith(firstAnswer.text);
  await userEvent.click(screen.getByRole("button", { name: "Повторить" }));
  expect(api.command).toHaveBeenCalledWith("retryAnswer", "session", "first", older.revision);

  api.state.mockResolvedValue({
    ...base,
    answers: { ...answers, busy: true, items: [{ ...older, status: "pending" }, newer] },
  });
  api.answer.mockResolvedValue({ ...failed, status: "pending", error: "Не удалось сохранить" });
  expect(await screen.findByRole("button", { name: "Сохранить снова" })).toBeTruthy();
  await userEvent.click(await screen.findByRole("button", { name: "Отбросить" }));
  await userEvent.click(screen.getByRole("button", { name: "Отбросить ответ" }));
  expect(api.command).toHaveBeenCalledWith("discardAnswer", "session", "first", older.revision);
});

it("asks for a fresh discard confirmation when the pending attempt changes on the same card", async () => {
  const pending = { ...first, status: "pending", revision_attempt: true };
  const showPending = (revision: string) => {
    api.state.mockResolvedValue({
      ...base,
      answers: { ...base.answers, busy: true, items: [{ ...pending, revision }] },
    });
    api.answer.mockResolvedValue({ ...firstAnswer, status: "pending", revision });
  };
  showPending("attempt-1");
  render(<AnswersWindow />);
  await userEvent.click(await screen.findByRole("button", { name: "Отбросить" }));
  expect(screen.getByRole("button", { name: "Отбросить ответ" })).toBeTruthy();

  showPending("attempt-2");
  const discard = await screen.findByRole("button", { name: "Отбросить" });
  expect(screen.queryByRole("button", { name: "Отбросить ответ" })).toBeNull();
  expect(api.command).not.toHaveBeenCalled();
  await userEvent.click(discard);
  await userEvent.click(screen.getByRole("button", { name: "Отбросить ответ" }));
  expect(api.command).toHaveBeenCalledWith("discardAnswer", "session", "first", "attempt-2");
});
