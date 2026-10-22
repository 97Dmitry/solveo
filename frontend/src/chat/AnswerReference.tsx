import { X } from "lucide-react";
import { referencedAnswer, type Message } from "../bridge/state";
import { IconButton } from "../ui/Button";
import { answerElementID } from "./followup";

export function AnswerReference({
  messages,
  messageID,
  kind = "",
  onClear,
}: {
  messages: Message[];
  messageID: string;
  kind?: string;
  onClear?: () => void;
}) {
  if (!messageID) return null;
  const text = referencedAnswer(messages, messageID, kind);
  return (
    <div className="answer-reference">
      <button
        type="button"
        className="reference-target"
        disabled={!text}
        onClick={() => {
          const target = document.getElementById(answerElementID(messageID, kind));
          target?.scrollIntoView({ behavior: "smooth", block: "center" });
          target?.focus({ preventScroll: true });
        }}
      >
        <strong>
          {kind === "approach"
            ? "Уточнение к подходу"
            : kind === "solution"
              ? "Уточнение к решению"
              : "Уточнение к ответу"}
        </strong>
        <span>{text || "Ответ недоступен"}</span>
      </button>
      {onClear && (
        <IconButton label="Убрать ссылку на ответ" onClick={onClear}>
          <X size={14} />
        </IconButton>
      )}
    </div>
  );
}
