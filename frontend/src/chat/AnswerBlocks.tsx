import { useState } from "react";
import { Check, Copy } from "lucide-react";
import type { Message } from "../bridge/state";
import { IconButton } from "../ui/Button";
import { Markdown } from "./Markdown";

export function AnswerBlocks({
  answers,
  previews,
  onCopy,
  onOpenLink,
}: {
  answers: NonNullable<Message["answers"]>;
  // Unfinished text of requesting blocks; never copied or treated as complete.
  previews: Partial<Record<"approach" | "solution", string>>;
  onCopy: (text: string) => Promise<boolean>;
  onOpenLink: (url: string) => void;
}) {
  const [copied, setCopied] = useState("");
  return (
    <div className="answer-blocks">
      {answers.map((answer) => {
        const approach = answer.kind === "approach";
        const copyID = `${answer.attempt_id}:${answer.kind}`;
        return (
          <section
            key={answer.kind}
            className={`message assistant answer-block ${answer.kind}`}
            aria-label={approach ? "Подход" : "Решение"}
          >
            <div className="message-meta">
              <span>{approach ? "Подход" : "Решение"}</span>
              <small>
                {answer.model || "Модель по умолчанию"}
                {answer.reasoning ? ` · ${answer.reasoning}` : ""}
              </small>
              {answer.status !== "requesting" && (
                <time>
                  {new Intl.NumberFormat("ru", { maximumFractionDigits: 1 }).format(
                    answer.elapsed_ms / 1000,
                  )}{" "}
                  с
                </time>
              )}
              {answer.text && (
                <IconButton
                  label={
                    copied === copyID
                      ? "Ответ скопирован"
                      : approach
                        ? "Скопировать подход"
                        : "Скопировать решение"
                  }
                  onClick={() => {
                    void onCopy(answer.text).then((ok) => {
                      if (ok) setCopied(copyID);
                    });
                  }}
                >
                  {copied === copyID ? <Check size={13} /> : <Copy size={13} />}
                </IconButton>
              )}
            </div>
            {approach && answer.status === "complete" && (
              <p className="answer-note">Предварительный ответ — может отличаться от решения.</p>
            )}
            {answer.text && (
              <div className="message-body">
                <Markdown text={answer.text} onCopy={onCopy} onOpenLink={onOpenLink} />
              </div>
            )}
            {answer.status === "requesting" && previews[answer.kind] && (
              <div className="message-body streaming" aria-busy="true">
                <Markdown text={previews[answer.kind] ?? ""} onOpenLink={onOpenLink} />
              </div>
            )}
            {answer.status === "requesting" && (
              <p className="answer-note" role="status">
                {previews[answer.kind]
                  ? approach
                    ? "Получаю подход…"
                    : "Получаю решение…"
                  : approach
                    ? "Ожидаю подход…"
                    : "Ожидаю решение…"}
              </p>
            )}
            {answer.status !== "requesting" && answer.status !== "complete" && (
              <p className="answer-note">
                {answer.error ||
                  (answer.status === "interrupted" || answer.status === "unknown"
                    ? "Исход запроса неизвестен"
                    : "Запрос остановлен")}
              </p>
            )}
          </section>
        );
      })}
    </div>
  );
}
