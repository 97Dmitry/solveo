import { useState } from "react";
import { Eraser, MessageSquareShare, Send, X } from "lucide-react";
import type { AudioState } from "../bridge/state";
import { Button, IconButton } from "../ui/Button";
import { unsentCount, type AudioActions } from "./actions";
import { TranscriptList } from "./TranscriptList";

// LiveTranscript keeps only the phrase selection locally; selections of vanished or still-partial
// phrases are dropped on every render. Nothing is sent automatically.
export function LiveTranscript({
  audio,
  disabled,
  canSend,
  sendHotkey,
  actions,
}: {
  audio: AudioState;
  disabled: boolean;
  canSend: boolean;
  sendHotkey: string;
  actions: AudioActions;
}) {
  const unsent = unsentCount(audio.segments);
  const [picked, setPicked] = useState<ReadonlySet<string>>(new Set());
  const finals = new Set(audio.segments.filter((s) => s.final).map((s) => s.id));
  const selected = new Set([...picked].filter((id) => finals.has(id)));
  const chosen = audio.segments.filter((s) => selected.has(s.id));
  const toggle = (id: string) => {
    const next = new Set(selected);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    setPicked(next);
  };
  return (
    <section className="live-transcript" aria-label="Расшифровка">
      <TranscriptList
        segments={audio.segments}
        selected={selected}
        onToggle={toggle}
        empty={
          audio.listening
            ? "Жду речь…"
            : audio.model === "ready"
              ? "Нажмите «Слушать», чтобы начать. Выбранные фразы можно отправить в чат."
              : "Фразы появятся здесь во время прослушивания."
        }
      />
      {audio.segments.length > 0 && (
        <div className="transcript-actions">
          {chosen.length > 0 && (
            <span className="selection-count" aria-live="polite">
              Выбрано: {chosen.length}
              <IconButton label="Снять выбор" onClick={() => setPicked(new Set())}>
                <X size={14} />
              </IconButton>
            </span>
          )}
          <Button
            className={chosen.length === 0 ? "primary" : ""}
            disabled={unsent === 0 || !canSend || audio.sending_unsent}
            title={`Отправить все реплики собеседника после последней отправленной${sendHotkey ? ` (${sendHotkey})` : ""}`}
            onClick={actions.sendUnsent}
          >
            <MessageSquareShare size={14} />
            {audio.sending_unsent
              ? "Жду конец фразы…"
              : `Новые реплики${unsent ? ` (${unsent})` : ""}`}
          </Button>
          <Button
            className={chosen.length > 0 ? "primary" : ""}
            disabled={chosen.length === 0 || !canSend}
            onClick={() => {
              actions.send(chosen.map((s) => s.id));
              setPicked(new Set());
            }}
          >
            <Send size={14} />
            Отправить
          </Button>
          <Button
            disabled={chosen.length === 0 || disabled}
            onClick={() => {
              actions.insert(chosen.map((s) => s.text).join("\n"));
              setPicked(new Set());
            }}
          >
            В поле ввода
          </Button>
          <IconButton
            label="Очистить расшифровку"
            disabled={audio.segments.length === 0 || disabled}
            onClick={actions.clear}
          >
            <Eraser size={17} />
          </IconButton>
        </div>
      )}
    </section>
  );
}
