import { useState } from "react";
import type { QuestionCommand, QuestionState } from "../bridge/state";
import { Button } from "../ui/Button";
import { questionCommand } from "./question";

// QuestionSettings enables the builder and picks its model/effort from the provider catalog. The
// window requests the catalog when the user opens the settings tab; there is no default model.
export function QuestionSettings({
  view,
  busy,
  execute,
}: {
  view: QuestionState;
  busy: boolean;
  execute: (command: QuestionCommand) => Promise<void>;
}) {
  const [model, setModel] = useState(view.model);
  const [effort, setEffort] = useState(view.reasoning);
  const [error, setError] = useState("");
  const selected = view.models.find((m) => m.id === model);
  const run = async (action: string, extra: Partial<QuestionCommand> = {}) => {
    setError("");
    try {
      await execute(questionCommand(view, action, extra));
    } catch (e) {
      setError(String(e));
    }
  };
  return (
    <section className="settings-section" aria-label="Сборщик вопросов">
      <h2>Сборщик вопросов</h2>
      {!view.available ? (
        <p className="audio-note">Подключение не поддерживает сборку вопросов.</p>
      ) : (
        <>
          <p className="audio-note">
            Собирает вопрос интервьюера из реплик собеседника отдельными запросами к Codex по мере
            речи. Основной чат и его настройки не меняются, ничего не отправляется автоматически.
          </p>
          <div className="audio-controls">
            <Button
              disabled={busy || (!view.enabled && !view.model)}
              aria-pressed={view.enabled}
              onClick={() => void run(view.enabled ? "disable" : "enable")}
            >
              {view.enabled ? "Выключить сборщик" : "Включить сборщик"}
            </Button>
            {!view.enabled && !view.model && (
              <span className="audio-note">Сначала сохраните модель сборщика.</span>
            )}
          </div>
          <div className="settings-fields">
            <label>
              Модель сборщика
              <select
                value={model}
                onChange={(event) => {
                  setModel(event.target.value);
                  setEffort(
                    view.models.find((m) => m.id === event.target.value)?.default_reasoning ?? "",
                  );
                }}
              >
                <option value="">Выберите модель</option>
                {model && !selected && (
                  <option value={model}>{model} (проверить доступность)</option>
                )}
                {view.models.map((m) => (
                  <option key={m.id} value={m.id}>
                    {m.label}
                  </option>
                ))}
              </select>
            </label>
            <label>
              Reasoning effort
              <select value={effort} onChange={(event) => setEffort(event.target.value)}>
                <option value="">По умолчанию подключения</option>
                {selected?.reasoning_efforts.map((e) => (
                  <option key={e.id} value={e.id}>
                    {e.id}
                  </option>
                ))}
              </select>
            </label>
          </div>
          <div className="audio-controls">
            <Button
              className="primary"
              disabled={busy || !selected}
              onClick={() => void run("configure", { model, reasoning: effort })}
            >
              Сохранить модель сборщика
            </Button>
            <Button disabled={busy} onClick={() => void run("models")}>
              Обновить список моделей
            </Button>
          </div>
        </>
      )}
      {error && (
        <p role="alert" className="audio-error">
          {error}
        </p>
      )}
    </section>
  );
}
