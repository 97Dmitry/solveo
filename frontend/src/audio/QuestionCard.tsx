import { useState } from "react";
import type { QuestionCommand, QuestionState } from "../bridge/state";
import { Button } from "../ui/Button";

const statuses: Record<QuestionState["status"], string> = {
  waiting: "Жду вопрос интервьюера",
  draft: "Черновик вопроса",
  analyzing: "Собираю вопрос…",
  ready: "Вопрос готов",
  editing: "Ручная правка",
  error: "Сборка остановлена",
};
export function QuestionCard({
  view,
  busy,
  canSend,
  execute,
}: {
  view: QuestionState;
  busy: boolean;
  canSend: boolean;
  execute: (command: QuestionCommand) => Promise<void>;
}) {
  const [draft, setDraft] = useState<string | null>(null);
  const [model, setModel] = useState(view.model);
  const [effort, setEffort] = useState(view.reasoning);
  const [confirmRebuild, setConfirmRebuild] = useState(false);
  const [error, setError] = useState("");
  const selected = view.models.find((m) => m.id === model);
  const editing = draft !== null;
  const run = async (action: string, extra: Partial<QuestionCommand> = {}) => {
    setError("");
    try {
      await execute({
        action,
        chat_id: view.chat_id,
        id: view.id,
        revision: view.revision,
        version: view.version,
        text: "",
        model: "",
        reasoning: "",
        confirm: false,
        ...extra,
      });
      return true;
    } catch (e) {
      setError(String(e));
      return false;
    }
  };
  return (
    <section className="question-card" aria-label="Собранный вопрос">
      <div className="audio-window-tools">
        <strong>Вопрос интервьюера</strong>
        <Button
          disabled={busy || !view.available || (!view.enabled && !view.model)}
          aria-pressed={view.enabled}
          onClick={() => void run(view.enabled ? "disable" : "enable")}
        >
          {view.enabled ? "Выключить сборщик" : "Включить сборщик"}
        </Button>
      </div>
      {!view.available && <p>Подключение не поддерживает сборку вопросов.</p>}
      <p role="status">
        {statuses[view.status]}
        {!view.enabled && " · выключен"}
        {view.manual &&
          (view.origin === "manual" ? " · ручная версия" : " · подтверждено пользователем")}
      </p>
      {view.status === "draft" && (
        <p>
          {view.can_confirm
            ? "Анализ завершён. Модель оставила вопрос черновиком: проверьте текст и подтвердите его либо дождитесь продолжения."
            : "Ожидаю окончательных системных реплик или их анализа."}
        </p>
      )}
      {(error || view.error) && (
        <p role="alert" className="audio-error">
          {error || view.error}
        </p>
      )}
      {view.unincluded && (
        <small>
          {view.manual
            ? "Есть новые реплики, не включённые в подтверждённый текст."
            : "Есть реплики, ещё не включённые в карточку."}
        </small>
      )}
      {editing ? (
        <label>
          Текст вопроса
          <textarea
            aria-label="Текст собранного вопроса"
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            rows={4}
          />
        </label>
      ) : view.text ? (
        <p className="question-card-text">{view.text}</p>
      ) : null}
      {editing && !view.editing && !busy && (
        <p>
          Правка ещё не подтверждена или карточка уже изменена. Текст сохранён в редакторе.{" "}
          <Button onClick={() => setDraft(null)}>Закрыть локальную правку</Button>
        </p>
      )}
      {view.previous_question_id && <small>Уточнение предыдущего отправленного вопроса</small>}
      <div className="question-card-actions">
        {view.can_confirm && !editing && (
          <Button disabled={busy} onClick={() => void run("confirm")}>
            Подтвердить этот текст
          </Button>
        )}

        {editing ? (
          <Button
            disabled={busy || !view.editing || !draft.trim() || Array.from(draft).length > 8000}
            onClick={() =>
              void run("save", { text: draft }).then((saved) => {
                if (saved) setDraft(null);
              })
            }
          >
            Сохранить правки
          </Button>
        ) : (
          <Button
            disabled={busy}
            onClick={() => {
              setDraft(view.text);
              void run("edit", { text: view.text });
            }}
          >
            Редактировать
          </Button>
        )}
        <Button
          disabled={busy || editing || !view.can_send || !canSend}
          onClick={() => void run("send")}
        >
          Отправить собранный вопрос
        </Button>
        <Button
          disabled={busy || (!view.id && view.status !== "analyzing" && !view.error)}
          onClick={() =>
            void run("skip").then((ok) => {
              if (ok) setDraft(null);
            })
          }
        >
          Пропустить вопрос
        </Button>
        <Button
          disabled={busy || !view.enabled}
          onClick={() => {
            if (editing || view.manual) setConfirmRebuild(true);
            else void run("rebuild");
          }}
        >
          Пересобрать
        </Button>
        {view.error && (
          <Button disabled={busy || !view.enabled} onClick={() => void run("retry")}>
            Повторить анализ
          </Button>
        )}
      </div>
      {confirmRebuild && (
        <div role="group" aria-label="Замена ручной версии">
          <p>Заменить текущий текст результатом сборщика?</p>
          <Button
            disabled={busy}
            onClick={() =>
              void run("rebuild", { confirm: true }).then((ok) => {
                if (ok) {
                  setDraft(null);
                  setConfirmRebuild(false);
                }
              })
            }
          >
            Подтвердить пересборку
          </Button>
          <Button onClick={() => setConfirmRebuild(false)}>Отмена</Button>
        </div>
      )}
      {view.sources.length > 0 && (
        <details className="question-sources">
          <summary>Исходные реплики ({view.sources.length})</summary>
          {view.sources.map((source) => (
            <p key={source.id}>
              <small>{source.track === "system" ? "Собеседник" : "Микрофон"}</small> {source.text}
            </p>
          ))}
        </details>
      )}
      <details
        className="question-settings"
        onToggle={(event) => {
          if (event.currentTarget.open && view.models.length === 0) void run("models");
        }}
      >
        <summary>Модель сборщика{view.model && ` · ${view.model}`}</summary>
        <p>Отдельные запросы к Codex по мере речи. Основной чат и его настройки не меняются.</p>
        <Button disabled={busy} onClick={() => void run("models")}>
          Обновить список моделей
        </Button>
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
            {model && !selected && <option value={model}>{model} (проверить доступность)</option>}
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
        <Button
          disabled={busy || !selected}
          onClick={() => void run("configure", { model, reasoning: effort })}
        >
          Сохранить модель сборщика
        </Button>
      </details>
    </section>
  );
}
