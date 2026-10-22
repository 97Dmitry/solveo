import { Button } from "../ui/Button";

import { followupCommands, type FollowupHandler } from "./followup";

export function FollowupActions({
  messageID,
  kind = "",
  disabled,
  onFollowup,
}: {
  messageID: string;
  kind?: string;
  disabled: boolean;
  onFollowup: FollowupHandler;
}) {
  return (
    <div
      className="followup-actions"
      aria-label="Уточнить ответ"
      title={
        disabled
          ? "Для уточнения нужен пустой редактор и завершённый запрос"
          : "Уточнение будет добавлено в редактор"
      }
    >
      <Button disabled={disabled} onClick={() => onFollowup(messageID, kind, "")}>
        Уточнить
      </Button>
      {followupCommands.map(([label, text]) => (
        <Button key={label} disabled={disabled} onClick={() => onFollowup(messageID, kind, text)}>
          {label}
        </Button>
      ))}
    </div>
  );
}
