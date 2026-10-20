import { useEffect, useState } from "react";
import type { ChatConfig, Model, Snapshot } from "../bridge/state";
import { configSchema, errorText } from "../bridge/state";
import { Button } from "../ui/Button";

const responseLanguages = [
  "Русский",
  "English",
  "Deutsch",
  "Français",
  "Español",
  "中文",
  "日本語",
  "한국어",
  "Português",
  "Українська",
];
const codeLanguages = [
  "Go",
  "Python",
  "JavaScript",
  "TypeScript",
  "Java",
  "C++",
  "C",
  "C#",
  "Rust",
  "Kotlin",
  "Swift",
  "SQL",
  "Bash",
  "PHP",
  "Ruby",
  "Dart",
  "R",
  "Scala",
];
function LanguageSelect({
  value,
  options,
  automatic,
  onChange,
}: {
  value: string;
  options: string[];
  automatic: string;
  onChange: (value: string) => void;
}) {
  return (
    <select value={value} onChange={(event) => onChange(event.target.value)}>
      <option value="auto">{automatic}</option>
      {value !== "auto" && !options.includes(value) && (
        <option value={value}>{value || "По умолчанию"}</option>
      )}
      {options.map((language) => (
        <option key={language} value={language}>
          {language}
        </option>
      ))}
    </select>
  );
}
type Catalog =
  | { status: "loading" }
  | { status: "ready"; models: Model[] }
  | { status: "error"; message: string };
export function ChatSettings({
  snapshot,
  onSave,
  onClose,
  loadModels,
}: {
  snapshot: Snapshot;
  onSave: (title: string, config: ChatConfig) => Promise<boolean>;
  onClose: () => void;
  loadModels: () => Promise<Model[]>;
}) {
  const [title, setTitle] = useState(snapshot.title);
  const [form, setForm] = useState(snapshot.config);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [catalog, setCatalog] = useState<Catalog>({ status: "loading" });
  const [refresh, setRefresh] = useState(0);
  useEffect(() => {
    let ignore = false;
    void loadModels().then(
      (models) => {
        if (!ignore) setCatalog({ status: "ready", models });
      },
      (cause: unknown) => {
        if (!ignore) setCatalog({ status: "error", message: errorText(cause) });
      },
    );
    return () => {
      ignore = true;
    };
  }, [loadModels, refresh]);
  const models = catalog.status === "ready" ? catalog.models : [];
  const defaultModel = models.find((model) => model.is_default);
  const selectedModel =
    form.model === "" ? defaultModel : models.find((model) => model.id === form.model);
  const efforts = selectedModel?.reasoning_efforts ?? [];
  const selectedEffort = efforts.find((effort) => effort.id === form.reasoning);
  const unknownModel = form.model !== "" && !models.some((model) => model.id === form.model);
  const unknownEffort = form.reasoning !== "" && selectedEffort === undefined;
  const invalidSelection = catalog.status === "ready" && (unknownModel || unknownEffort);
  const change = <K extends keyof ChatConfig>(key: K, value: ChatConfig[K]) =>
    setForm((previous) => ({ ...previous, [key]: value }));
  return (
    <form
      className="settings-form"
      onSubmit={(event) => {
        event.preventDefault();
        if (invalidSelection || saving) return;
        const parsed = configSchema.safeParse(form);
        if (!parsed.success) {
          setError("Проверьте параметры ответа");
          return;
        }
        setSaving(true);
        void onSave(title, parsed.data).then((ok) => {
          setSaving(false);
          if (ok) onClose();
        });
      }}
    >
      <p className="form-intro">
        Подготовьте контекст до начала работы. Сохранение настроек не отправляет запрос модели.
      </p>
      <label>
        Название чата
        <input value={title} maxLength={160} onChange={(event) => setTitle(event.target.value)} />
      </label>
      <label>
        Что будет происходить
        <textarea
          rows={4}
          value={form.context}
          placeholder="Собеседование на Go-разработчика. Буду присылать задачи на код, вопросы по Go и SQL…"
          onChange={(event) => change("context", event.target.value)}
        />
      </label>
      <label>
        Как отвечать
        <textarea
          rows={3}
          value={form.instructions}
          placeholder="Сначала решение, затем короткое объяснение. Для кода укажи сложность."
          onChange={(event) => change("instructions", event.target.value)}
        />
      </label>
      <div className="form-row">
        <label>
          Язык ответа
          <LanguageSelect
            value={form.response_language}
            options={responseLanguages}
            automatic="Как в вопросе"
            onChange={(value) => change("response_language", value)}
          />
        </label>
        <label>
          Язык кода
          <LanguageSelect
            value={form.code_language}
            options={codeLanguages}
            automatic="Как в задании"
            onChange={(value) => change("code_language", value)}
          />
        </label>
      </div>
      <label>
        Подробность
        <select
          value={form.detail}
          onChange={(event) => {
            const parsed = configSchema.shape.detail.safeParse(event.target.value);
            if (parsed.success) change("detail", parsed.data);
          }}
        >
          <option value="brief">Кратко</option>
          <option value="normal">Обычно</option>
          <option value="detailed">Подробно</option>
        </select>
      </label>
      <div className="form-divider" />
      <p className="form-intro">
        Подключение: {snapshot.provider.label}. Модели и уровни рассуждения загружаются через
        подключение Codex.
      </p>
      {catalog.status === "loading" && <p role="status">Загружаю модели…</p>}
      {catalog.status === "error" && (
        <p role="alert">
          Не удалось загрузить модели: {catalog.message}. Сохранённые параметры остаются без
          изменений.
        </p>
      )}
      <label>
        Модель
        <select
          value={form.model}
          disabled={catalog.status !== "ready" || saving}
          onChange={(event) =>
            setForm((previous) => ({ ...previous, model: event.target.value, reasoning: "" }))
          }
        >
          <option value="">
            По умолчанию подключения{defaultModel ? ` — ${defaultModel.label}` : ""}
          </option>
          {unknownModel && (
            <option value={form.model}>{form.model} — сохранённая, нет в списке</option>
          )}
          {models.map((model) => (
            <option key={model.id} value={model.id}>
              {model.label}
            </option>
          ))}
        </select>
      </label>
      {selectedModel?.description && <p className="form-intro">{selectedModel.description}</p>}
      {snapshot.provider.capabilities.reasoning && (
        <label>
          Усилие рассуждения
          <select
            value={form.reasoning}
            disabled={catalog.status !== "ready" || saving}
            onChange={(event) => change("reasoning", event.target.value)}
          >
            <option value="">
              По умолчанию
              {selectedModel?.default_reasoning ? ` — ${selectedModel.default_reasoning}` : ""}
            </option>
            {unknownEffort && (
              <option value={form.reasoning}>{form.reasoning} — сохранённое, недоступно</option>
            )}
            {efforts.map((effort) => (
              <option key={effort.id} value={effort.id}>
                {effort.id}
              </option>
            ))}
          </select>
        </label>
      )}
      {selectedEffort?.description && <p className="form-intro">{selectedEffort.description}</p>}
      {invalidSelection && (
        <p role="alert">
          Выберите доступную модель и её уровень рассуждения. Сохранённое значение отсутствует в
          текущем списке.
        </p>
      )}
      <Button
        disabled={catalog.status === "loading" || saving}
        onClick={() => {
          setCatalog({ status: "loading" });
          setRefresh((value) => value + 1);
        }}
      >
        Обновить модели
      </Button>
      {error && <p role="alert">{error}</p>}
      <div className="form-actions">
        <Button onClick={onClose} disabled={saving}>
          Отмена
        </Button>
        <Button
          type="submit"
          className="primary"
          disabled={saving || invalidSelection || !snapshot.controls.can_edit_config}
        >
          {saving ? "Сохраняю…" : "Сохранить преднастройку"}
        </Button>
      </div>
    </form>
  );
}
