// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vite-plus/test";
import { ChatSettings } from "./ChatSettings";
import { snapshotFixture } from "../bridge/test-fixture";
afterEach(cleanup);
it("saves preparation independently of a chat question", async () => {
  const save = vi.fn(async () => true);
  const close = vi.fn();
  render(
    <ChatSettings
      snapshot={snapshotFixture()}
      onSave={save}
      onClose={close}
      loadModels={async () => []}
    />,
  );
  await userEvent.type(screen.getByLabelText("Что будет происходить"), "Go interview");
  await userEvent.click(screen.getByRole("button", { name: "Сохранить преднастройку" }));
  expect(save).toHaveBeenCalledWith(
    "Новый чат",
    expect.objectContaining({ context: "Go interview" }),
  );
  expect(close).toHaveBeenCalledOnce();
});
it("cancel discards form changes without saving", async () => {
  const save = vi.fn(async () => true);
  const close = vi.fn();
  render(
    <ChatSettings
      snapshot={snapshotFixture()}
      onSave={save}
      onClose={close}
      loadModels={async () => []}
    />,
  );
  await userEvent.type(screen.getByLabelText("Что будет происходить"), "draft context");
  await userEvent.click(screen.getByRole("button", { name: "Отмена" }));
  expect(save).not.toHaveBeenCalled();
  expect(close).toHaveBeenCalledOnce();
});
it("selects languages and only the selected model's advertised reasoning efforts", async () => {
  const save = vi.fn(async () => true);
  const loadModels = vi.fn(async () => [
    {
      id: "model-a",
      label: "Model A",
      description: "",
      is_default: true,
      default_reasoning: "adaptive",
      reasoning_efforts: [
        { id: "adaptive", description: "Automatic depth" },
        { id: "none", description: "No reasoning" },
      ],
    },
    {
      id: "model-b",
      label: "Model B",
      description: "",
      is_default: false,
      default_reasoning: "high",
      reasoning_efforts: [{ id: "high", description: "Deep" }],
    },
  ]);
  render(
    <ChatSettings
      snapshot={snapshotFixture()}
      onSave={save}
      onClose={vi.fn()}
      loadModels={loadModels}
    />,
  );
  await userEvent.selectOptions(screen.getByLabelText("Язык ответа"), "English");
  await userEvent.selectOptions(screen.getByLabelText("Язык кода"), "Go");
  await screen.findByRole("option", { name: "Model A" });
  await userEvent.selectOptions(screen.getByLabelText("Модель"), "model-a");
  await userEvent.selectOptions(screen.getByLabelText("Усилие рассуждения"), "adaptive");
  await userEvent.selectOptions(screen.getByLabelText("Модель"), "model-b");
  expect(screen.queryByRole("option", { name: /adaptive/ })).toBeNull();
  expect(screen.getByLabelText<HTMLSelectElement>("Усилие рассуждения").value).toBe("");
  await userEvent.click(screen.getByRole("button", { name: "Сохранить преднастройку" }));
  expect(save).toHaveBeenCalledWith(
    "Новый чат",
    expect.objectContaining({
      response_language: "English",
      code_language: "Go",
      model: "model-b",
      reasoning: "",
    }),
  );
});
it("keeps saved configuration after discovery failure and allows retry", async () => {
  const snapshot = snapshotFixture();
  snapshot.config.model = "saved-model";
  snapshot.config.reasoning = "ultra";
  const save = vi.fn(async () => true);
  const loadModels = vi
    .fn<() => Promise<import("../bridge/state").Model[]>>()
    .mockRejectedValueOnce(new Error("offline"))
    .mockResolvedValueOnce([
      {
        id: "saved-model",
        label: "Saved model",
        description: "",
        is_default: true,
        default_reasoning: "ultra",
        reasoning_efforts: [{ id: "ultra", description: "Deepest" }],
      },
    ]);
  render(
    <ChatSettings snapshot={snapshot} onSave={save} onClose={vi.fn()} loadModels={loadModels} />,
  );
  await screen.findByRole("alert");
  expect(screen.getByLabelText<HTMLSelectElement>("Модель").value).toBe("saved-model");
  await userEvent.type(screen.getByLabelText("Что будет происходить"), "Keep this context");
  await userEvent.click(screen.getByRole("button", { name: "Обновить модели" }));
  await screen.findByRole("option", { name: "Saved model" });
  expect(screen.getByLabelText<HTMLSelectElement>("Усилие рассуждения").value).toBe("ultra");
  await userEvent.click(screen.getByRole("button", { name: "Сохранить преднастройку" }));
  expect(save).toHaveBeenCalledWith(
    "Новый чат",
    expect.objectContaining({
      model: "saved-model",
      reasoning: "ultra",
      context: "Keep this context",
    }),
  );
});
it("does not save an obsolete model or effort advertised by another model", async () => {
  const snapshot = snapshotFixture();
  snapshot.config.model = "model-a";
  snapshot.config.reasoning = "max";
  const save = vi.fn(async () => true);
  const loadModels = vi.fn(async () => [
    {
      id: "model-a",
      label: "Model A",
      description: "",
      is_default: true,
      default_reasoning: "",
      reasoning_efforts: [],
    },
  ]);
  render(
    <ChatSettings snapshot={snapshot} onSave={save} onClose={vi.fn()} loadModels={loadModels} />,
  );
  await screen.findByRole("option", { name: "Model A" });
  await userEvent.click(screen.getByRole("button", { name: "Сохранить преднастройку" }));
  expect(save).not.toHaveBeenCalled();
  await userEvent.selectOptions(screen.getByLabelText("Усилие рассуждения"), "");
  await userEvent.click(screen.getByRole("button", { name: "Сохранить преднастройку" }));
  expect(save).toHaveBeenCalledWith("Новый чат", expect.objectContaining({ reasoning: "" }));
});

it("saves independent approach model and effort without resetting the main model", async () => {
  const save = vi.fn(async () => true);
  const models = [
    {
      id: "strong",
      label: "Strong",
      description: "",
      is_default: true,
      default_reasoning: "high",
      reasoning_efforts: [{ id: "high", description: "Deep" }],
    },
    {
      id: "light",
      label: "Light",
      description: "",
      is_default: false,
      default_reasoning: "none",
      reasoning_efforts: [{ id: "none", description: "Fast" }],
    },
  ];
  render(
    <ChatSettings
      snapshot={snapshotFixture()}
      onSave={save}
      onClose={vi.fn()}
      loadModels={async () => models}
    />,
  );
  await screen.findByRole("option", { name: "Strong" });
  await userEvent.selectOptions(screen.getByLabelText("Модель"), "strong");
  await userEvent.selectOptions(screen.getByLabelText("Усилие рассуждения"), "high");
  await userEvent.click(screen.getByLabelText("Параллельные подход и решение"));
  await userEvent.selectOptions(screen.getByLabelText("Быстрая модель"), "light");
  await userEvent.selectOptions(screen.getByLabelText("Рассуждение быстрой модели"), "none");
  await userEvent.click(screen.getByRole("button", { name: "Сохранить преднастройку" }));
  expect(save).toHaveBeenCalledWith(
    "Новый чат",
    expect.objectContaining({
      parallel_answers: true,
      model: "strong",
      reasoning: "high",
      fast_model: "light",
      fast_reasoning: "none",
    }),
  );
});
