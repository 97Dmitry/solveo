import { X } from "lucide-react";
import type { Snapshot } from "../bridge/state";
import { IconButton } from "../ui/Button";
export function AttachmentStrip({
  items,
  disabled,
  onOpen,
  onRemove,
}: {
  items: Snapshot["queue"];
  disabled: boolean;
  onOpen: (id: string) => void;
  onRemove?: (id: string) => void;
}) {
  if (items.length === 0) return null;
  return (
    <div className="attachments">
      {items.map((item) => (
        <div className="attachment" key={item.id}>
          <button
            className="thumbnail"
            type="button"
            title={item.label}
            onClick={() => onOpen(item.id)}
          >
            <img src={item.url} alt={item.label} />
          </button>
          {onRemove && (
            <IconButton
              label={`Убрать ${item.label}`}
              className="remove-image"
              disabled={disabled}
              onClick={() => onRemove(item.id)}
            >
              <X size={12} />
            </IconButton>
          )}
        </div>
      ))}
    </div>
  );
}
