import { useRef, type Ref } from "react";
import { ArrowUp, Square, ImagePlus } from "lucide-react";
import { IconButton } from "../ui/Button";
type Props = {
  textareaRef?: Ref<HTMLTextAreaElement>;
  canAddImage?: boolean;
  onAddImage?: (file: File) => void;
  onImageError?: (message: string) => void;
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
  canAddImage = false,
  onAddImage,
  onImageError,
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
  const fileInput = useRef<HTMLInputElement>(null);
  return (
    <div className="composer">
      <textarea
        ref={textareaRef}
        aria-label="Вопрос"
        rows={1}
        value={text}
        disabled={disabled}
        placeholder="Задайте вопрос или добавьте снимок…"
        onChange={(e) => onChange(e.target.value)}
        onPaste={(e) => {
          const files = Array.from(e.clipboardData.files);
          if (files.length === 0) return;
          e.preventDefault();
          if (!canAddImage || !onAddImage) {
            onImageError?.(
              "Сейчас нельзя добавить изображение: дождитесь завершения операции или освободите очередь",
            );
          } else if (files.length !== 1) {
            onImageError?.("Добавляйте изображения по одному");
          } else {
            const file = files[0];
            if (file?.type.startsWith("image/")) onAddImage(file);
            else onImageError?.("Вставьте изображение PNG/JPEG или добавьте его из файла");
          }
        }}
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
      {onAddImage && (
        <input
          ref={fileInput}
          type="file"
          accept="image/png,image/jpeg,.png,.jpg,.jpeg"
          aria-label="Файл изображения"
          hidden
          disabled={!canAddImage}
          onChange={(e) => {
            const file = e.currentTarget.files?.[0];
            e.currentTarget.value = "";
            if (file && canAddImage) onAddImage(file);
          }}
        />
      )}
      <div className="composer-actions">
        {onAddImage && (
          <IconButton
            label="Изображение из файла"
            title="Добавить PNG/JPEG до 32 МиБ; изображение также можно вставить через Ctrl+V"
            disabled={!canAddImage}
            onClick={() => fileInput.current?.click()}
          >
            <ImagePlus size={17} />
          </IconButton>
        )}
        {busy ? (
          <IconButton label="Отменить запрос" className="send-button cancel" onClick={onCancel}>
            <Square size={16} />
          </IconButton>
        ) : (
          <IconButton
            label="Отправить вопрос"
            title="Отправить вопрос (Enter; Shift+Enter — новая строка)"
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
