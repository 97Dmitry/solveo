import { MessageSquare, Plus, Trash2 } from "lucide-react";
import type { Snapshot } from "../bridge/state";
import { Button, IconButton } from "../ui/Button";
export function HistoryDrawer({
  snapshot,
  onOpen,
  onNew,
  onDelete,
}: {
  snapshot: Snapshot;
  onOpen: (id: string) => void;
  onNew: () => void;
  onDelete: (id: string) => void;
}) {
  return (
    <div className="history-body">
      <Button className="new-chat-button" disabled={!snapshot.controls.can_switch} onClick={onNew}>
        <Plus size={16} />
        Новый чат
      </Button>
      <div className="history-list">
        {snapshot.history.map((item) => (
          <div
            className={`history-item ${item.id === snapshot.active_chat_id ? "active" : ""}`}
            key={item.id}
          >
            <button
              type="button"
              disabled={!snapshot.controls.can_switch}
              onClick={() => onOpen(item.id)}
            >
              <MessageSquare size={17} />
              <span>
                <strong>{item.title}</strong>
                <small>
                  {item.prepared ? "Подготовлен · " : ""}
                  {new Date(item.updated_at).toLocaleDateString("ru-RU", {
                    day: "numeric",
                    month: "short",
                  })}
                </small>
              </span>
            </button>
            {item.id !== snapshot.active_chat_id && (
              <IconButton
                label={`Удалить чат ${item.title}`}
                disabled={!snapshot.controls.can_switch}
                onClick={() => onDelete(item.id)}
              >
                <Trash2 size={14} />
              </IconButton>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
