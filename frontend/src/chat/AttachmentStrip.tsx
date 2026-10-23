import { ArrowLeft, ArrowRight, Plus, X } from "lucide-react";
import type { Snapshot } from "../bridge/state";
import { IconButton } from "../ui/Button";
// Tiles keep a fixed height: order numbers stay visible, while reorder/reuse tools overlay the
// thumbnail on hover or keyboard focus instead of adding a row under it.
export function AttachmentStrip({
  items,
  disabled,
  onOpen,
  onRemove,
  onReorder,
  onReuse,
}: {
  items: Snapshot["queue"];
  disabled: boolean;
  onOpen: (id: string) => void;
  onRemove?: (id: string) => void;
  onReorder?: (ids: string[]) => void;
  onReuse?: (id: string) => void;
}) {
  if (items.length === 0) return null;
  const move = (index: number, delta: number) => {
    const item = items[index];
    if (!item) return;
    const next = items.filter((other) => other.id !== item.id);
    next.splice(index + delta, 0, item);
    onReorder?.(next.map((other) => other.id));
  };
  return (
    <div className="attachments">
      {items.map((item, index) => (
        <div className="attachment" key={item.id}>
          <button
            className="thumbnail"
            type="button"
            title={item.label}
            onClick={() => onOpen(item.id)}
          >
            <img src={item.url} alt={item.label} />
          </button>
          {onReorder && <span className="attachment-number">{index + 1}</span>}
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
          {((onReorder && items.length > 1) || onReuse) && (
            <div className="attachment-tools">
              {onReorder && items.length > 1 && (
                <>
                  <IconButton
                    label={`Раньше: ${item.label}`}
                    disabled={disabled || index === 0}
                    onClick={() => move(index, -1)}
                  >
                    <ArrowLeft size={13} />
                  </IconButton>
                  <IconButton
                    label={`Позже: ${item.label}`}
                    disabled={disabled || index === items.length - 1}
                    onClick={() => move(index, 1)}
                  >
                    <ArrowRight size={13} />
                  </IconButton>
                </>
              )}
              {onReuse && (
                <IconButton
                  label={`Добавить к вопросу: ${item.label}`}
                  disabled={disabled}
                  onClick={() => onReuse(item.id)}
                >
                  <Plus size={14} />
                </IconButton>
              )}
            </div>
          )}
        </div>
      ))}
    </div>
  );
}
