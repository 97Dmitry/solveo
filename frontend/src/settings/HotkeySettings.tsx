import { useEffect, useState, type KeyboardEvent } from "react";
import { X } from "lucide-react";
import { errorText, hotkeyActions, type HotkeyAction, type Hotkeys } from "../bridge/state";
import { Button, IconButton } from "../ui/Button";

// Labels match internal/hotkeys, which names actions in conflict errors.
const labels: Record<HotkeyAction, string> = {
  "send-question": "Отправить собранный вопрос",
  "skip-question": "Пропустить собранный вопрос",
  "quick-region": "Снимок области и ответ",
  "queue-region": "Добавить область в очередь",
  "quick-full": "Снимок всего экрана и ответ",
  submit: "Отправить вопрос и очередь",
  toggle: "Показать / скрыть окно",
};
const named: Record<string, string> = {
  Enter: "Enter",
  NumpadEnter: "Enter",
  Space: "Space",
  Tab: "Tab",
  Backspace: "Backspace",
  Insert: "Insert",
  Delete: "Delete",
  Home: "Home",
  End: "End",
  PageUp: "PageUp",
  PageDown: "PageDown",
  ArrowLeft: "Left",
  ArrowUp: "Up",
  ArrowRight: "Right",
  ArrowDown: "Down",
  PrintScreen: "PrintScreen",
  Pause: "Pause",
};
// keyName maps the physical key, so shortcuts do not depend on the keyboard layout.
function keyName(code: string): string {
  const letter = /^Key([A-Z])$/.exec(code);
  if (letter?.[1]) return letter[1];
  const digit = /^Digit([0-9])$/.exec(code);
  if (digit?.[1]) return digit[1];
  if (/^F([1-9]|1[0-9]|2[0-4])$/.test(code)) return code;
  return named[code] ?? "";
}
function shortcutFromEvent(e: KeyboardEvent): { text: string; error: string } {
  const key = keyName(e.code);
  if (key === "") return { text: "", error: "" };
  const mods = [
    e.ctrlKey && "Ctrl",
    e.altKey && "Alt",
    e.shiftKey && "Shift",
    e.metaKey && "Win",
  ].filter((m) => m !== false);
  if (!e.ctrlKey && !e.altKey && !e.metaKey)
    return { text: "", error: "Добавьте Ctrl, Alt или Win" };
  return { text: [...mods, key].join("+"), error: "" };
}
function conflicts(set: Hotkeys): string[] {
  const owners = new Map<string, HotkeyAction[]>();
  for (const action of hotkeyActions) {
    const text = set[action];
    if (text !== "") owners.set(text, [...(owners.get(text) ?? []), action]);
  }
  return [...owners]
    .filter(([, actions]) => actions.length > 1)
    .map(
      ([text, actions]) => `${text} назначено: ${actions.map((a) => `«${labels[a]}»`).join(", ")}`,
    );
}
const same = (a: Hotkeys, b: Hotkeys) => hotkeyActions.every((action) => a[action] === b[action]);

export function HotkeySettings({
  hotkeys,
  errors,
  disabled,
  onSave,
  loadDefaults,
  suspend,
}: {
  hotkeys: Hotkeys;
  errors: Partial<Record<HotkeyAction, string>>;
  disabled: boolean;
  onSave: (hotkeys: Hotkeys) => void;
  loadDefaults: () => Promise<Hotkeys>;
  suspend: (on: boolean) => Promise<void>;
}) {
  const [draft, setDraft] = useState(hotkeys);
  const [base, setBase] = useState(hotkeys);
  const [recording, setRecording] = useState<HotkeyAction | null>(null);
  const [hint, setHint] = useState("");
  // Adopt saved values from Go unless the user is editing.
  if (!same(base, hotkeys)) {
    setBase(hotkeys);
    if (same(draft, base)) setDraft(hotkeys);
  }
  // Ending recording always resumes the global hotkeys, including on unmount.
  // Moving between fields keeps one pause, so resume/suspend calls cannot reorder.
  const paused = recording !== null;
  useEffect(() => {
    if (!paused) return;
    let active = true;
    suspend(true).catch((error: unknown) => {
      if (active) setHint(errorText(error));
    });
    return () => {
      active = false;
      void suspend(false).catch(() => undefined);
    };
  }, [paused, suspend]);
  const problems = conflicts(draft);
  const dirty = !same(draft, hotkeys);
  return (
    <div className="hotkey-settings">
      <p className="form-intro">
        Глобальные сочетания работают, даже когда окно скрыто. Нажмите на поле и введите новое
        сочетание; Esc отменяет запись. Сочетание должно включать Ctrl, Alt или Win.
      </p>
      {hotkeyActions.map((action) => (
        <div className="hotkey-row" key={action}>
          <span>{labels[action]}</span>
          <button
            type="button"
            className={`hotkey-field ${recording === action ? "recording" : ""}`}
            aria-label={`Сочетание: ${labels[action]}`}
            disabled={disabled}
            onClick={() => {
              setHint("");
              setRecording(action);
            }}
            onBlur={() => setRecording((r) => (r === action ? null : r))}
            onKeyDown={(e) => {
              if (recording !== action) return;
              e.preventDefault();
              e.stopPropagation();
              if (e.key === "Escape") {
                setRecording(null);
                return;
              }
              const { text, error } = shortcutFromEvent(e);
              setHint(error);
              if (text === "") return;
              setDraft((d) => ({ ...d, [action]: text }));
              setRecording(null);
            }}
          >
            {recording === action ? "Нажмите сочетание…" : draft[action] || "Отключено"}
          </button>
          <IconButton
            label={`Отключить: ${labels[action]}`}
            disabled={disabled || draft[action] === ""}
            onClick={() => setDraft((d) => ({ ...d, [action]: "" }))}
          >
            <X size={14} />
          </IconButton>
          {errors[action] && (
            <small className="hotkey-error" role="alert">
              Не работает: {errors[action]}
            </small>
          )}
        </div>
      ))}
      {hint && (
        <p className="hotkey-error" role="status">
          {hint}
        </p>
      )}
      {problems.map((p) => (
        <p className="hotkey-error" role="alert" key={p}>
          {p}
        </p>
      ))}
      <div className="button-row">
        <Button
          className="primary"
          disabled={disabled || !dirty || problems.length > 0}
          onClick={() => onSave(draft)}
        >
          Сохранить сочетания
        </Button>
        <Button disabled={disabled || !dirty} onClick={() => setDraft(hotkeys)}>
          Отменить
        </Button>
        <Button
          disabled={disabled}
          onClick={() => {
            loadDefaults().then(setDraft, (error: unknown) => setHint(errorText(error)));
          }}
        >
          Стандартные
        </Button>
      </div>
    </div>
  );
}
