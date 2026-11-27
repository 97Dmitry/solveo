import { useEffect, useState } from "react";
import { Check, Copy, CornerDownLeft, RotateCcw } from "lucide-react";
import { answersWindow, type AnswersWindowView, type AutoAnswerDetail } from "../bridge/wails";
import { Markdown } from "../chat/Markdown";
import { Button } from "../ui/Button";

const retryable = new Set(["failed", "canceled", "unknown", "interrupted"]);

export function AnswerPanel({
  item,
  sessionID,
  latest,
  busy,
  disabled,
  execute,
}: {
  item: AnswersWindowView["answers"]["items"][number];
  sessionID: string;
  latest: boolean;
  busy: boolean;
  disabled: boolean;
  execute: (...command: ["insertAnswer"] | ["retryAnswer" | "discardAnswer", string]) => void;
}) {
  const [result, setResult] = useState<{
    revision: string;
    status: string;
    detail: AutoAnswerDetail | undefined;
    saved: AutoAnswerDetail | undefined;
    error: string;
  }>();
  const [copyError, setCopyError] = useState("");
  const [copied, setCopied] = useState("");
  const { id, revision, status, revision_attempt: revisionAttempt } = item;
  const streamed = status === "requesting" || status === "refining";
  const current = result?.revision === revision && result.status === status ? result : undefined;
  const detail = current?.detail ?? (revisionAttempt ? result?.saved : undefined);

  useEffect(() => {
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const poll = async () => {
      let retry = false;
      try {
        const answer = await answersWindow.answer(sessionID, id);
        if (stopped) return;
        if (
          answer.session_id === sessionID &&
          answer.id === id &&
          answer.revision === revision &&
          answer.status === status
        ) {
          setResult((previous) => ({
            revision,
            status,
            detail: answer,
            saved: answer.saved_answer ? answer : previous?.saved,
            error: "",
          }));
        } else {
          retry = true;
        }
      } catch (error) {
        retry = true;
        if (!stopped)
          setResult((previous) => ({
            revision,
            status,
            detail:
              previous?.revision === revision && previous.status === status
                ? previous.detail
                : undefined,
            saved: previous?.saved,
            error: String(error),
          }));
      } finally {
        if (!stopped && (streamed || retry))
          timer = setTimeout(() => void poll(), retry ? 1000 : 200);
      }
    };
    void poll();
    return () => {
      stopped = true;
      if (timer !== undefined) clearTimeout(timer);
    };
  }, [sessionID, id, revision, status, streamed]);

  const error = current?.error || current?.detail?.error || copyError;
  const copyable = detail?.saved_answer || detail?.status === "complete";
  const recoveryTarget = latest || revisionAttempt;
  const pendingSave = recoveryTarget && status === "pending";
  const canRetry = pendingSave || (recoveryTarget && !busy && retryable.has(status));
  const copy = async (text: string) => {
    const ok = await answersWindow.copy(text);
    setCopyError(ok ? "" : "Не удалось скопировать текст");
    setCopied(ok ? text : "");
  };

  return (
    <div className="answers-detail">
      {detail?.text && detail.saved_answer && status !== "complete" && (
        <p className="answers-note">Последний готовый ответ</p>
      )}
      {detail?.text && (
        <div className={`message-body${copyable ? "" : " streaming"}`}>
          <Markdown
            text={detail.text}
            onOpenLink={answersWindow.openLink}
            {...(copyable ? { onCopy: answersWindow.copy } : {})}
          />
        </div>
      )}
      {!detail?.text && !error && (!detail || streamed) && (
        <p className="answers-loading">
          {status === "refining"
            ? "Уточняю ответ…"
            : streamed
              ? "Готовлю ответ…"
              : "Загружаю ответ…"}
        </p>
      )}
      {error && (
        <p className="answers-detail-error" role="alert">
          {error}
        </p>
      )}
      <div className="answers-actions">
        {copyable && detail?.text && (
          <Button className="small" onClick={() => void copy(detail.text)}>
            {copied === detail.text ? <Check size={13} /> : <Copy size={13} />}
            {copied === detail.text ? "Ответ скопирован" : "Скопировать ответ"}
          </Button>
        )}
        <Button className="small" disabled={disabled} onClick={() => execute("insertAnswer")}>
          <CornerDownLeft size={13} /> В поле ввода чата
        </Button>
        {canRetry && (
          <Button
            className="small"
            disabled={disabled}
            onClick={() => execute("retryAnswer", revision)}
          >
            <RotateCcw size={13} /> {pendingSave ? "Сохранить снова" : "Повторить"}
          </Button>
        )}
        {pendingSave && (
          <DiscardAnswer
            key={revision}
            disabled={disabled}
            refinement={revisionAttempt}
            onDiscard={() => execute("discardAnswer", revision)}
          />
        )}
      </div>
    </div>
  );
}

function DiscardAnswer({
  disabled,
  refinement,
  onDiscard,
}: {
  disabled: boolean;
  refinement: boolean;
  onDiscard: () => void;
}) {
  const [confirm, setConfirm] = useState(false);
  if (!confirm)
    return (
      <Button className="small danger" disabled={disabled} onClick={() => setConfirm(true)}>
        Отбросить
      </Button>
    );
  return (
    <div className="answers-discard">
      <p>{refinement ? "Отбросить несохранённое уточнение?" : "Отбросить несохранённый ответ?"}</p>
      <Button className="small" onClick={() => setConfirm(false)}>
        Отмена
      </Button>
      <Button
        className="small danger"
        disabled={disabled}
        onClick={() => {
          setConfirm(false);
          onDiscard();
        }}
      >
        Отбросить ответ
      </Button>
    </div>
  );
}
