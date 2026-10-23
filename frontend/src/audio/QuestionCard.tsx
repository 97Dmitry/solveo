import { useState } from "react";
import { Pencil, RefreshCw, RotateCcw, Send, SkipForward } from "lucide-react";
import type { QuestionCommand, QuestionState } from "../bridge/state";
import { Button, IconButton } from "../ui/Button";
import { sourceLabel } from "./actions";
import { questionCommand } from "./question";

const statuses: Record<QuestionState["status"], string> = {
  waiting: "Жду вопрос интервьюера",
  draft: "Черновик вопроса",
  analyzing: "Собираю вопрос…",
  ready: "Вопрос готов",
  editing: "Ручная правка",
  error: "Сборка остановлена",
};
// QuestionCard is the live part of the question builder: status, text and the per-card actions.
// Model and enable settings live in QuestionSettings.
export function QuestionCard({
  view,
  busy,
  canSend,
  execute,
  onOpenSettings,
}: {
  view: QuestionState;
  busy: boolean;
  canSend: boolean;
  execute: (command: QuestionCommand) => Promise<void>;
  onOpenSettings: () => void;
}) {
  const [draft, setDraft] = useState<string | null>(null);
  const [confirmRebuild, setConfirmRebuild] = useState(false);
  const [error, setError] = useState("");
  const editing = draft !== null;
  const run = async (action: string, extra: Partial<QuestionCommand> = {}) => {
    setError("");
    try {
      await execute(questionCommand(view, action, extra));
      return true;
    } catch (e) {
      setError(String(e));
      return false;
    }
  };
  if (!view.available)
    return (
      <section className="question-card compact" aria-label="Собранный вопрос">
        <p className="audio-note">Подключение не поддерживает сборку вопросов.</p>
      </section>
    );
  if (!view.enabled && !view.text && !editing && !view.error)
    return (
      <section className="question-card compact" aria-label="Собранный вопрос">
        <span className="question-status" role="status">
          Сборщик вопросов выключен
        </span>
        {view.model ? (
          <Button disabled={busy} onClick={() => void run("enable")}>
            Включить сборщик
          </Button>
        ) : (
          <Button onClick={onOpenSettings}>Выбрать модель</Button>
        )}
        {error && (
          <p role="alert" className="audio-error">
            {error}
          </p>
        )}
      </section>
    );
  return (
    <section className={`question-card ${view.status}`} aria-label="Собранный вопрос">
      <div className="question-card-head">
        <span className="question-status" role="status">
          {statuses[view.status]}
          {!view.enabled && " · выключен"}
          {view.manual &&
            (view.origin === "manual" ? " · ручная версия" : " · подтверждено пользователем")}
        </span>
        <div className="question-card-tools">
          {!editing && (
            <IconButton
              label="Редактировать"
              disabled={busy}
              onClick={() => {
                setDraft(view.text);
                void run("edit", { text: view.text });
              }}
            >
              <Pencil size={16} />
            </IconButton>
          )}
          <IconButton
            label="Пропустить вопрос"
            disabled={busy || (!view.id && view.status !== "analyzing" && !view.error)}
            onClick={() =>
              void run("skip").then((ok) => {
                if (ok) setDraft(null);
              })
            }
          >
            <SkipForward size={16} />
          </IconButton>
          <IconButton
            label="Пересобрать"
            disabled={busy || !view.enabled}
            onClick={() => {
              if (editing || view.manual) setConfirmRebuild(true);
              else void run("rebuild");
            }}
          >
            <RefreshCw size={16} />
          </IconButton>
          {view.error && (
            <IconButton
              label="Повторить анализ"
              disabled={busy || !view.enabled}
              onClick={() => void run("retry")}
            >
              <RotateCcw size={16} />
            </IconButton>
          )}
        </div>
      </div>
      {view.status === "draft" && (
        <small className="question-hint">
          {view.can_confirm
            ? "Анализ завершён. Модель оставила вопрос черновиком: проверьте текст и подтвердите его либо дождитесь продолжения."
            : "Ожидаю окончательных системных реплик или их анализа."}
        </small>
      )}
      {(error || view.error) && (
        <p role="alert" className="audio-error">
          {error || view.error}
        </p>
      )}
      {view.unincluded && (
        <small className="question-hint">
          {view.manual
            ? "Есть новые реплики, не включённые в подтверждённый текст."
            : "Есть реплики, ещё не включённые в карточку."}
        </small>
      )}
      {editing ? (
        <label className="question-editor">
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
        <p className="question-hint">
          Правка ещё не подтверждена или карточка уже изменена. Текст сохранён в редакторе.{" "}
          <Button onClick={() => setDraft(null)}>Закрыть локальную правку</Button>
        </p>
      )}
      {view.previous_question_id && (
        <small className="question-hint">Уточнение предыдущего отправленного вопроса</small>
      )}
      {(editing || view.text) && (
        <div className="question-card-actions">
          {view.can_confirm && !editing && (
            <Button disabled={busy} onClick={() => void run("confirm")}>
              Подтвердить этот текст
            </Button>
          )}
          {editing && (
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
          )}
          <Button
            className="primary"
            disabled={busy || editing || !view.can_send || !canSend}
            onClick={() => void run("send")}
          >
            <Send size={14} />
            Отправить вопрос
          </Button>
        </div>
      )}
      {confirmRebuild && (
        <div className="question-confirm" role="group" aria-label="Замена ручной версии">
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
              <small>{sourceLabel[source.track]}</small> {source.text}
            </p>
          ))}
        </details>
      )}
    </section>
  );
}
