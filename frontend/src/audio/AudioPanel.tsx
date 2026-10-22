import { useEffect, useRef, useState } from "react";
import { CircleCheck, CircleDashed, CircleHelp, CircleX, Mic, Square } from "lucide-react";
import type { AudioState } from "../bridge/state";
import { Button } from "../ui/Button";
import { TranscriptList } from "./TranscriptList";
export type AudioActions = {
  refresh: () => void;
  prepare: () => void;
  startModel: () => void;
  stopModel: () => void;
  startListening: () => void;
  stopListening: () => void;
  clear: () => void;
  send: (segmentIDs: string[]) => void;
  insert: (text: string) => void;
};
type Check = AudioState["checks"][number];
const checkLabel = {
  docker: "Docker",
  engine: "Docker Engine",
  image: "Образ solveo-stt",
  gpu: "GPU",
  model: "Модель Qwen3-ASR",
} satisfies Record<Check["id"], string>;
const sourceLabel = { system: "Собеседник", mic: "Микрофон" } as const;
function CheckIcon({ state }: { state: Check["state"] }) {
  if (state === "ok") return <CircleCheck size={15} aria-label="готово" />;
  if (state === "error") return <CircleX size={15} aria-label="ошибка" />;
  if (state === "missing") return <CircleDashed size={15} aria-label="не готово" />;
  return <CircleHelp size={15} aria-label="неизвестно" />;
}
// AudioPanel shows readiness, model and listening controls and the transcript. Go owns every state;
// only the phrase selection is local.
export function AudioPanel({
  audio,
  disabled,
  canSend,
  actions,
}: {
  audio: AudioState;
  disabled: boolean;
  canSend: boolean;
  actions: AudioActions;
}) {
  const [picked, setPicked] = useState<ReadonlySet<string>>(new Set());
  const needsCheck = audio.available && audio.checks.length === 0 && audio.busy === "";
  const { refresh } = actions;
  // Check once per opening; a later empty list means a check is already running.
  const asked = useRef(false);
  useEffect(() => {
    if (needsCheck && !asked.current) {
      asked.current = true;
      refresh();
    }
  }, [needsCheck, refresh]);
  if (!audio.available)
    return (
      <section className="audio-panel" aria-label="Распознавание речи">
        <p className="audio-note">Распознавание речи недоступно в этой сборке</p>
      </section>
    );
  const finals = new Set(audio.segments.filter((s) => s.final).map((s) => s.id));
  const selected = new Set([...picked].filter((id) => finals.has(id)));
  const chosen = audio.segments.filter((s) => selected.has(s.id));
  const idle = audio.busy === "" && !disabled;
  const allOk = audio.checks.length > 0 && audio.checks.every((c) => c.state === "ok");
  const ready = audio.model === "ready";
  const toggle = (id: string) => {
    const next = new Set(selected);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    setPicked(next);
  };
  return (
    <section className="audio-panel" aria-label="Распознавание речи">
      {!ready && (
        <ul className="audio-checks">
          {audio.checks.map((c) => (
            <li key={c.id} className={`check ${c.state}`}>
              <CheckIcon state={c.state} />
              <span className="check-name">{checkLabel[c.id]}</span>
              <span className="check-message">{c.message}</span>
            </li>
          ))}
        </ul>
      )}
      <div className="audio-controls">
        {audio.busy === "" && !allOk && audio.checks.length > 0 && !ready && (
          <Button className="primary" disabled={!idle} onClick={actions.prepare}>
            Подготовить
          </Button>
        )}
        {audio.busy === "" && !ready && audio.checks.length > 0 && (
          <Button disabled={!idle} onClick={actions.refresh}>
            Проверить снова
          </Button>
        )}
        {audio.busy === "" && allOk && (audio.model === "stopped" || audio.model === "failed") && (
          <Button className="primary" disabled={!idle} onClick={actions.startModel}>
            Запустить модель
          </Button>
        )}
        {audio.busy === "" && ready && !audio.listening && (
          <>
            <Button className="primary" disabled={!idle} onClick={actions.startListening}>
              <Mic size={15} />
              Слушать
            </Button>
            <Button disabled={!idle} onClick={actions.stopModel}>
              Выгрузить модель
            </Button>
          </>
        )}
        {audio.listening && (
          <Button className="listening" disabled={disabled} onClick={actions.stopListening}>
            <Square size={13} />
            Остановить
          </Button>
        )}
        {audio.busy !== "" && (
          <span className="audio-progress" aria-live="polite">
            {audio.progress || "Выполняется…"}
          </span>
        )}
      </div>
      {audio.error && (
        <p className="audio-error" role="alert">
          {audio.error}
        </p>
      )}
      {audio.sources.length > 0 && (
        <ul className="audio-sources">
          {audio.sources.map((s) => (
            <li key={s.track} className={s.active ? "active" : ""}>
              {sourceLabel[s.track]}: {s.error || s.device || "—"}
            </li>
          ))}
        </ul>
      )}
      <TranscriptList segments={audio.segments} selected={selected} onToggle={toggle} />
      <div className="transcript-actions">
        <Button
          className="primary"
          disabled={chosen.length === 0 || !canSend}
          onClick={() => {
            actions.send(chosen.map((s) => s.id));
            setPicked(new Set());
          }}
        >
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
        <Button disabled={audio.segments.length === 0 || disabled} onClick={actions.clear}>
          Очистить
        </Button>
      </div>
    </section>
  );
}
