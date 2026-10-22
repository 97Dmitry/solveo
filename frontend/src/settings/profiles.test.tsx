// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vite-plus/test";
import { ChatSettings } from "./ChatSettings";
import { snapshotFixture } from "../bridge/test-fixture";
const profile = () => ({
  id: "a".repeat(32),
  name: "Backend",
  config: {
    ...snapshotFixture().config,
    position: "Backend",
    stack: "Go",
    experience: "Built billing",
  },
  revision: 1,
  created_at: "2026-10-22T10:00:00Z",
  updated_at: "2026-10-22T10:00:00Z",
});
afterEach(cleanup);
function setup() {
  const snapshot = snapshotFixture();
  snapshot.profiles = [profile()];
  const actions = {
    save: vi.fn(async () => true),
    remove: vi.fn(async () => true),
    reload: vi.fn(async () => true),
    create: vi.fn(async () => true),
  };
  const saveChat = vi.fn(async () => true);
  const close = vi.fn();
  render(
    <ChatSettings
      snapshot={snapshot}
      onSave={saveChat}
      onClose={close}
      loadModels={async () => []}
      profileActions={actions}
    />,
  );
  return { actions, saveChat, close };
}
it("saves manually entered resume as a profile without applying chat settings", async () => {
  const { actions, saveChat, close } = setup();
  await userEvent.type(screen.getByLabelText("Название профиля"), "My interview");
  await userEvent.type(screen.getByLabelText("Позиция"), "Backend developer");
  await userEvent.type(
    screen.getByLabelText("Реальный опыт и проекты / текст резюме"),
    "Built payments",
  );
  await userEvent.click(screen.getByRole("button", { name: "Сохранить как профиль" }));
  expect(actions.save).toHaveBeenCalledWith(
    expect.objectContaining({
      id: "",
      name: "My interview",
      expected_revision: 0,
      config: {
        ...snapshotFixture().config,
        position: "Backend developer",
        experience: "Built payments",
      },
    }),
  );
  expect(saveChat).not.toHaveBeenCalled();
  expect(close).not.toHaveBeenCalled();
});
it("confirms replacing dirty form and applies a loaded profile only on explicit save", async () => {
  const { actions, saveChat } = setup();
  const context = screen.getByLabelText("Что будет происходить");
  await userEvent.type(context, "unsaved");
  await userEvent.selectOptions(screen.getByLabelText("Профиль подготовки"), profile().id);
  expect(context).toHaveProperty("value", "unsaved");
  await userEvent.click(screen.getByRole("button", { name: "Оставить форму" }));
  expect(context).toHaveProperty("value", "unsaved");
  await userEvent.selectOptions(screen.getByLabelText("Профиль подготовки"), profile().id);
  await userEvent.click(screen.getByRole("button", { name: "Заменить поля" }));
  expect(screen.getByLabelText("Реальный опыт и проекты / текст резюме")).toHaveProperty(
    "value",
    "Built billing",
  );
  expect(saveChat).not.toHaveBeenCalled();
  expect(actions.create).not.toHaveBeenCalled();
  await userEvent.click(screen.getByRole("button", { name: "Сохранить преднастройку" }));
  expect(saveChat).toHaveBeenCalledWith(
    "Новый чат",
    expect.objectContaining({ experience: "Built billing", stack: "Go" }),
  );
});
it("creates a chat from the saved revision and confirms deletion", async () => {
  const { actions, saveChat } = setup();
  await userEvent.selectOptions(screen.getByLabelText("Профиль подготовки"), profile().id);
  await userEvent.click(screen.getByRole("button", { name: "Удалить профиль" }));
  expect(actions.remove).not.toHaveBeenCalled();
  await userEvent.click(screen.getByRole("button", { name: "Подтвердить удаление" }));
  expect(actions.remove).toHaveBeenCalledWith(profile().id, 1);
  // The fake does not publish a new snapshot: selection stays until backend confirms it.
  await userEvent.selectOptions(screen.getByLabelText("Профиль подготовки"), profile().id);
  await userEvent.click(screen.getByRole("button", { name: "Новый чат из профиля" }));
  expect(actions.create).toHaveBeenCalledWith(profile().id, 1);
  expect(saveChat).not.toHaveBeenCalled();
});
it("keeps the form and shows errors when profile storage fails", async () => {
  const snapshot = snapshotFixture();
  snapshot.profiles_error = "library unreadable";
  const actions = {
    save: vi.fn(async () => false),
    remove: vi.fn(async () => false),
    reload: vi.fn(async () => false),
    create: vi.fn(async () => false),
  };
  render(
    <ChatSettings
      snapshot={snapshot}
      onSave={async () => true}
      onClose={() => {}}
      loadModels={async () => []}
      profileActions={actions}
    />,
  );
  await userEvent.type(
    screen.getByLabelText("Реальный опыт и проекты / текст резюме"),
    "keep resume",
  );
  expect(screen.getByText("library unreadable")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Сохранить как профиль" })).toHaveProperty(
    "disabled",
    true,
  );
  await userEvent.click(screen.getByRole("button", { name: "Перечитать профили" }));
  expect(actions.reload).toHaveBeenCalledOnce();
  expect(screen.getByLabelText("Реальный опыт и проекты / текст резюме")).toHaveProperty(
    "value",
    "keep resume",
  );
});

it("updates the selected profile from current fields and preserves edits after a failed write", async () => {
  const snapshot = snapshotFixture();
  snapshot.profiles = [profile()];
  const actions = {
    save: vi.fn(async () => false),
    remove: vi.fn(async () => true),
    reload: vi.fn(async () => true),
    create: vi.fn(async () => true),
  };
  render(
    <ChatSettings
      snapshot={snapshot}
      onSave={async () => true}
      onClose={() => {}}
      loadModels={async () => []}
      profileActions={actions}
    />,
  );
  await userEvent.selectOptions(screen.getByLabelText("Профиль подготовки"), profile().id);
  await userEvent.clear(screen.getByLabelText("Реальный опыт и проекты / текст резюме"));
  await userEvent.type(
    screen.getByLabelText("Реальный опыт и проекты / текст резюме"),
    "My actual work",
  );
  await userEvent.click(screen.getByRole("button", { name: "Обновить профиль" }));
  expect(actions.save).toHaveBeenCalledWith(
    expect.objectContaining({
      id: profile().id,
      expected_revision: 1,
      config: { ...profile().config, experience: "My actual work" },
    }),
  );
  expect(screen.getByLabelText("Реальный опыт и проекты / текст резюме")).toHaveProperty(
    "value",
    "My actual work",
  );
  expect(screen.getByText(/Действие с профилем не выполнено/)).toBeTruthy();
});
it("does not create a chat with unavailable saved model and never changes the resume on discovery failure", async () => {
  const snapshot = snapshotFixture();
  const saved = profile();
  saved.config.model = "obsolete";
  snapshot.profiles = [saved];
  const actions = {
    save: vi.fn(async () => true),
    remove: vi.fn(async () => true),
    reload: vi.fn(async () => true),
    create: vi.fn(async () => true),
  };
  const loadModels = vi
    .fn<() => Promise<import("../bridge/state").Model[]>>()
    .mockRejectedValueOnce(new Error("offline"))
    .mockResolvedValueOnce([
      {
        id: "current",
        label: "Current",
        description: "",
        is_default: true,
        default_reasoning: "",
        reasoning_efforts: [],
      },
    ]);
  render(
    <ChatSettings
      snapshot={snapshot}
      onSave={async () => true}
      onClose={() => {}}
      loadModels={loadModels}
      profileActions={actions}
    />,
  );
  await screen.findByText(/Не удалось загрузить модели/);
  await userEvent.selectOptions(screen.getByLabelText("Профиль подготовки"), saved.id);
  expect(screen.getByLabelText("Реальный опыт и проекты / текст резюме")).toHaveProperty(
    "value",
    "Built billing",
  );
  expect(screen.getByLabelText("Модель")).toHaveProperty("value", "obsolete");
  await userEvent.click(screen.getByRole("button", { name: "Обновить модели" }));
  await screen.findByRole("option", { name: "Current" });
  expect(screen.getByRole("button", { name: "Новый чат из профиля" })).toHaveProperty(
    "disabled",
    true,
  );
  expect(screen.getByRole("button", { name: "Сохранить преднастройку" })).toHaveProperty(
    "disabled",
    true,
  );
  expect(actions.create).not.toHaveBeenCalled();
  expect(screen.getByLabelText("Реальный опыт и проекты / текст резюме")).toHaveProperty(
    "value",
    "Built billing",
  );
});

it("uses the revision selected before editing instead of overwriting a newer profile", async () => {
  const snapshot = snapshotFixture();
  snapshot.profiles = [profile()];
  const actions = {
    save: vi.fn(async () => false),
    remove: vi.fn(async () => true),
    reload: vi.fn(async () => true),
    create: vi.fn(async () => true),
  };
  const props = {
    onSave: async () => true,
    onClose: () => {},
    loadModels: async () => [],
    profileActions: actions,
  };
  const { rerender } = render(<ChatSettings snapshot={snapshot} {...props} />);
  await userEvent.selectOptions(screen.getByLabelText("Профиль подготовки"), profile().id);
  await userEvent.type(screen.getByLabelText("Реальный опыт и проекты / текст резюме"), "My edits");
  rerender(
    <ChatSettings
      snapshot={{ ...snapshot, revision: 2, profiles: [{ ...profile(), revision: 2 }] }}
      {...props}
    />,
  );
  await userEvent.click(screen.getByRole("button", { name: "Обновить профиль" }));
  expect(actions.save).toHaveBeenCalledWith(expect.objectContaining({ expected_revision: 1 }));
});

it("fills preparation on selection and switches unchanged profiles without extra confirmation", async () => {
  const snapshot = snapshotFixture();
  const first = profile();
  const second = {
    ...profile(),
    id: "b".repeat(32),
    name: "Frontend",
    config: { ...profile().config, position: "Frontend", experience: "Built a website" },
  };
  snapshot.profiles = [first, second];
  const saveChat = vi.fn(async () => true);
  const actions = {
    save: vi.fn(async () => true),
    remove: vi.fn(async () => true),
    reload: vi.fn(async () => true),
    create: vi.fn(async () => true),
  };
  render(
    <ChatSettings
      snapshot={snapshot}
      onSave={saveChat}
      onClose={() => {}}
      loadModels={async () => []}
      profileActions={actions}
    />,
  );
  await userEvent.selectOptions(screen.getByLabelText("Профиль подготовки"), first.id);
  expect(screen.getByLabelText("Позиция")).toHaveProperty("value", "Backend");
  expect(screen.queryByRole("button", { name: "Загрузить в форму" })).toBeNull();
  await userEvent.selectOptions(screen.getByLabelText("Профиль подготовки"), second.id);
  expect(screen.getByLabelText("Позиция")).toHaveProperty("value", "Frontend");
  expect(screen.queryByRole("group", { name: "Подтверждение действия с профилем" })).toBeNull();
  await userEvent.type(screen.getByLabelText("Позиция"), " edits");
  await userEvent.selectOptions(screen.getByLabelText("Профиль подготовки"), first.id);
  expect(screen.getByLabelText("Позиция")).toHaveProperty("value", "Frontend edits");
  await userEvent.click(screen.getByRole("button", { name: "Оставить форму" }));
  expect(screen.getByLabelText("Профиль подготовки")).toHaveProperty("value", second.id);
  expect(screen.getByLabelText("Название профиля")).toHaveProperty("value", "Frontend");
  expect(saveChat).not.toHaveBeenCalled();
  expect(actions.save).not.toHaveBeenCalled();
  expect(actions.create).not.toHaveBeenCalled();
});

it("preserves an edited chat title on selection and confirms discarding it only when creating a chat", async () => {
  const { actions } = setup();
  await userEvent.type(screen.getByLabelText("Название чата"), " draft title");
  await userEvent.selectOptions(screen.getByLabelText("Профиль подготовки"), profile().id);
  expect(screen.getByLabelText("Позиция")).toHaveProperty("value", "Backend");
  expect(screen.getByLabelText("Название чата")).toHaveProperty("value", "Новый чат draft title");
  expect(screen.queryByRole("group", { name: "Подтверждение действия с профилем" })).toBeNull();
  await userEvent.click(screen.getByRole("button", { name: "Новый чат из профиля" }));
  expect(actions.create).not.toHaveBeenCalled();
  await userEvent.click(screen.getByRole("button", { name: "Создать из сохранённого профиля" }));
  expect(actions.create).toHaveBeenCalledWith(profile().id, 1);
});
