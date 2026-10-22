import { useEffect, useRef, useState } from "react";
import type { ChatConfig, Model, Snapshot } from "../bridge/state";
import { configSchema, errorText } from "../bridge/state";
import { Button } from "../ui/Button";
import { ProfileControls, type ProfileActions } from "./ProfileControls";

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
function ModelFields({
  models,
  modelID,
  reasoning,
  modelLabel,
  effortLabel,
  disabled,
  supportsReasoning,
  onModel,
  onReasoning,
}: {
  models: Model[];
  modelID: string;
  reasoning: string;
  modelLabel: string;
  effortLabel: string;
  disabled: boolean;
  supportsReasoning: boolean;
  onModel: (value: string) => void;
  onReasoning: (value: string) => void;
}) {
  const defaultModel = models.find((model) => model.is_default);
  const selected = modelID === "" ? defaultModel : models.find((model) => model.id === modelID);
  const efforts = selected?.reasoning_efforts ?? [];
  const effort = efforts.find((item) => item.id === reasoning);
  return (
    <>
      <label>
        {modelLabel}
        <select
          value={modelID}
          disabled={disabled}
          onChange={(event) => onModel(event.target.value)}
        >
          <option value="">
            По умолчанию подключения{defaultModel ? ` — ${defaultModel.label}` : ""}
          </option>
          {modelID !== "" && selected === undefined && (
            <option value={modelID}>{modelID} — сохранённая, нет в списке</option>
          )}
          {models.map((model) => (
            <option key={model.id} value={model.id}>
              {model.label}
            </option>
          ))}
        </select>
      </label>
      {selected?.description && <p className="form-intro">{selected.description}</p>}
      {supportsReasoning && (
        <label>
          {effortLabel}
          <select
            value={reasoning}
            disabled={disabled}
            onChange={(event) => onReasoning(event.target.value)}
          >
            <option value="">
              По умолчанию{selected?.default_reasoning ? ` — ${selected.default_reasoning}` : ""}
            </option>
            {reasoning !== "" && effort === undefined && (
              <option value={reasoning}>{reasoning} — сохранённое, недоступно</option>
            )}
            {efforts.map((item) => (
              <option key={item.id} value={item.id}>
                {item.id}
              </option>
            ))}
          </select>
        </label>
      )}
      {effort?.description && <p className="form-intro">{effort.description}</p>}
    </>
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
  profileActions,
  busy = false,
}: {
  profileActions?: ProfileActions;
  busy?: boolean;
  snapshot: Snapshot;
  onSave: (title: string, config: ChatConfig) => Promise<boolean>;
  onClose: () => void;
  loadModels: () => Promise<Model[]>;
}) {
  const mounted = useRef(false);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const [title, setTitle] = useState(snapshot.title);
  const [form, setForm] = useState(snapshot.config);
  const [formBaseline, setFormBaseline] = useState(snapshot.config);
  const [saving, setSaving] = useState(false);
  const [profileBusy, setProfileBusy] = useState(false);
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
  const unavailable = (modelID: string, reasoning: string) => {
    const model =
      modelID === ""
        ? models.find((item) => item.is_default)
        : models.find((item) => item.id === modelID);
    return (
      (modelID !== "" && model === undefined) ||
      (reasoning !== "" && !model?.reasoning_efforts.some((effort) => effort.id === reasoning))
    );
  };
  const canUseConfig = (cfg: ChatConfig) =>
    catalog.status !== "ready" ||
    (!unavailable(cfg.model, cfg.reasoning) &&
      (!cfg.parallel_answers || !unavailable(cfg.fast_model, cfg.fast_reasoning)));
  const disabled = saving || profileBusy || busy;
  const invalidSelection =
    catalog.status === "ready" &&
    (unavailable(form.model, form.reasoning) ||
      (form.parallel_answers && unavailable(form.fast_model, form.fast_reasoning)));
  const change = <K extends keyof ChatConfig>(key: K, value: ChatConfig[K]) =>
    setForm((previous) => ({ ...previous, [key]: value }));
  return (
    <form
      className="settings-form"
      onSubmit={(event) => {
        event.preventDefault();
        if (invalidSelection || disabled) return;
        const parsed = configSchema.safeParse(form);
        if (!parsed.success) {
          setError("Проверьте параметры ответа");
          return;
        }
        setSaving(true);
        void onSave(title, parsed.data).then((ok) => {
          if (!mounted.current) return;
          setSaving(false);
          if (ok) onClose();
        });
      }}
    >
      <p className="form-intro">
        Подготовьте контекст до начала работы. Сохранение настроек не отправляет запрос модели.
      </p>
      {profileActions && (
        <ProfileControls
          snapshot={snapshot}
          config={form}
          dirty={JSON.stringify(form) !== JSON.stringify(formBaseline)}
          navigationDirty={
            JSON.stringify(form) !== JSON.stringify(formBaseline) || title !== snapshot.title
          }
          disabled={disabled || !snapshot.controls.can_switch}
          actions={profileActions}
          onLoad={(config) => {
            setForm(config);
            setFormBaseline(config);
          }}
          onClose={onClose}
          canUseConfig={canUseConfig}
          onBusy={setProfileBusy}
        />
      )}
      <label>
        Название чата
        <input value={title} maxLength={160} onChange={(event) => setTitle(event.target.value)} />
      </label>
      <div className="form-row">
        <label>
          Позиция
          <input value={form.position} onChange={(e) => change("position", e.target.value)} />
        </label>
        <label>
          Стек
          <input value={form.stack} onChange={(e) => change("stack", e.target.value)} />
        </label>
      </div>
      <label>
        Формат интервью
        <input
          value={form.interview_format}
          onChange={(e) => change("interview_format", e.target.value)}
        />
      </label>
      <label>
        Реальный опыт и проекты / текст резюме
        <textarea
          rows={6}
          value={form.experience}
          placeholder="Вставьте текст резюме или опишите свой реальный опыт и проекты."
          onChange={(e) => change("experience", e.target.value)}
        />
      </label>
      <p className="form-intro">
        Только предоставленные вами факты. Если сведений об опыте нет, помощник должен уточнить их
        или дать явно обозначенную заготовку.
      </p>
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
      {form.parallel_answers && <h3>Основное решение</h3>}
      <ModelFields
        models={models}
        modelID={form.model}
        reasoning={form.reasoning}
        modelLabel="Модель"
        effortLabel="Усилие рассуждения"
        disabled={catalog.status !== "ready" || disabled}
        supportsReasoning={snapshot.provider.capabilities.reasoning}
        onModel={(model) => setForm((previous) => ({ ...previous, model, reasoning: "" }))}
        onReasoning={(reasoning) => change("reasoning", reasoning)}
      />
      <div className="form-divider" />
      <label className="checkbox-field">
        <input
          type="checkbox"
          checked={form.parallel_answers}
          disabled={disabled}
          onChange={(event) => change("parallel_answers", event.target.checked)}
        />
        Параллельные подход и решение
      </label>
      <p className="form-intro">
        Эксперимент: два запроса на вопрос. Предварительный подход может отличаться от решения.
        Время каждого ответа отображается в чате.
      </p>
      {form.parallel_answers && (
        <>
          <h3>Быстрый подход</h3>
          <p className="form-intro">
            Выберите доступную лёгкую модель и небольшое усилие рассуждения. Пустые значения
            используют настройки подключения и не гарантируют ускорение.
          </p>
          <ModelFields
            models={models}
            modelID={form.fast_model}
            reasoning={form.fast_reasoning}
            modelLabel="Быстрая модель"
            effortLabel="Рассуждение быстрой модели"
            disabled={catalog.status !== "ready" || disabled}
            supportsReasoning={snapshot.provider.capabilities.reasoning}
            onModel={(fast_model) =>
              setForm((previous) => ({ ...previous, fast_model, fast_reasoning: "" }))
            }
            onReasoning={(reasoning) => change("fast_reasoning", reasoning)}
          />
        </>
      )}
      {invalidSelection && (
        <p role="alert">
          Выберите доступную модель и её уровень рассуждения. Сохранённое значение отсутствует в
          текущем списке.
        </p>
      )}
      <Button
        disabled={catalog.status === "loading" || disabled}
        onClick={() => {
          setCatalog({ status: "loading" });
          setRefresh((value) => value + 1);
        }}
      >
        Обновить модели
      </Button>
      {error && <p role="alert">{error}</p>}
      <div className="form-actions">
        <Button onClick={onClose} disabled={disabled}>
          Отмена
        </Button>
        <Button
          type="submit"
          className="primary"
          disabled={disabled || invalidSelection || !snapshot.controls.can_edit_config}
        >
          {saving ? "Сохраняю…" : "Сохранить преднастройку"}
        </Button>
      </div>
    </form>
  );
}
