import { useRef, type Ref } from "react";
import { ArrowUp, Square } from "lucide-react";
import { IconButton } from "../ui/Button";
type Props = {
  textareaRef?: Ref<HTMLTextAreaElement>;
  text: string;
  disabled: boolean;
  canSend: boolean;
  busy: boolean;
  onChange: (text: string) => void;
  onSend: () => void;
  onCancel: () => void;
  onComposition: (value: boolean) => void;
};
export function Composer({
  textareaRef,
  text,
  disabled,
  canSend,
  busy,
  onChange,
  onSend,
  onCancel,
  onComposition,
}: Props) {
  const composing = useRef(false);
  return (
    <div className="composer">
      <textarea
        ref={textareaRef}
        aria-label="Вопрос"
        rows={2}
        value={text}
        disabled={disabled}
        placeholder="Задайте вопрос или добавьте снимок…"
        onChange={(e) => onChange(e.target.value)}
        onCompositionStart={() => {
          composing.current = true;
          onComposition(true);
        }}
        onCompositionEnd={() => {
          composing.current = false;
          onComposition(false);
        }}
        onKeyDown={(e) => {
          if (
            e.key === "Enter" &&
            !e.shiftKey &&
            !e.nativeEvent.isComposing &&
            !composing.current
          ) {
            e.preventDefault();
            if (canSend && !disabled) onSend();
          }
        }}
      />
      <div className="composer-footer">
        <span>
          Enter — отправить <span className="secondary-hint"> / Shift+Enter — новая строка</span>
        </span>
        {busy ? (
          <IconButton label="Отменить запрос" className="send-button cancel" onClick={onCancel}>
            <Square size={16} />
          </IconButton>
        ) : (
          <IconButton
            label="Отправить вопрос"
            className="send-button"
            disabled={!canSend || disabled}
            onClick={onSend}
          >
            <ArrowUp size={20} />
          </IconButton>
        )}
      </div>
    </div>
  );
}
