import { ChevronDown, MessageSquareReply } from "lucide-react";
import { Menu } from "../ui/Menu";
import { followupCommands, type FollowupHandler } from "./followup";

// FollowupActions is one compact menu in the answer header; every item only prepares the editor.
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
    <Menu
      label="Уточнить ответ"
      className="followup-trigger"
      disabled={disabled}
      title={
        disabled
          ? "Для уточнения нужен пустой редактор и завершённый запрос"
          : "Уточнение будет добавлено в редактор"
      }
      items={[
        { label: "Свой вопрос к ответу", onSelect: () => onFollowup(messageID, kind, "") },
        "separator",
        ...followupCommands.map(([label, text]) => ({
          label,
          onSelect: () => onFollowup(messageID, kind, text),
        })),
      ]}
    >
      <MessageSquareReply size={14} />
      <span>Уточнить</span>
      <ChevronDown size={12} />
    </Menu>
  );
}
