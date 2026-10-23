import { useEffect, useRef, useState } from "react";
import { CircleCheck, CircleDashed, CircleHelp, CircleX, Mic, Square } from "lucide-react";
import type { AudioState, TranscriptSegment } from "../bridge/state";
import { Button } from "../ui/Button";
import { TranscriptList } from "./TranscriptList";
export type ArchivedTranscript = { id: string; title: string; segments: TranscriptSegment[] };
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
  selectProvider: (id: string) => void;
  setKey: (provider: string, key: string) => void;
  deleteKey: (provider: string) => void;
};
type Check = AudioState["checks"][number];
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
  archive,
}: {
  audio: AudioState;
  disabled: boolean;
  canSend: boolean;
  actions: AudioActions;
  archive?: ArchivedTranscript | undefined;
}) {
  const [picked, setPicked] = useState<ReadonlySet<string>>(new Set());
  const needsCheck = audio.available && audio.checks.length === 0 && audio.busy === "";
  const [key, setKey] = useState("");
  const { refresh } = actions;
  // Check once per opening and per provider; a later empty list means a check is already running.
  const asked = useRef("");
  const providerID = audio.provider.id;
  useEffect(() => {
    if (needsCheck && asked.current !== providerID) {
      asked.current = providerID;
      refresh();
    }
  }, [needsCheck, providerID, refresh]);
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
  const provider = audio.provider;
  const running = audio.busy !== "" || audio.listening || ready || audio.model === "starting";
  const toggle = (id: string) => {
    const next = new Set(selected);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    setPicked(next);
  };
  return (
    <section className="audio-panel" aria-label="Распознавание речи">
      <div className="audio-provider">
        {audio.providers.length > 1 && (
          <label>
            Распознаватель
            <select
              value={provider.id}
              disabled={running || disabled}
              onChange={(e) => actions.selectProvider(e.target.value)}
            >
              {audio.providers.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.label}
                </option>
              ))}
            </select>
          </label>
        )}
        <span className={provider.local ? "privacy local" : "privacy cloud"}>
          {provider.local
            ? "Локально: звук не покидает компьютер"
            : `Облако: звук отправляется в ${provider.vendor || provider.label}`}
        </span>
      </div>
      {provider.needs_key && !ready && (
        <form
          className="audio-key"
          onSubmit={(e) => {
            e.preventDefault();
            if (key.trim() === "") return;
            actions.setKey(provider.id, key);
            setKey("");
          }}
        >
          <label>
            {provider.key_label}
            <input
              type="password"
              autoComplete="off"
              value={key}
              placeholder={audio.key_configured ? "Ключ сохранён" : "Ключ не задан"}
              onChange={(e) => setKey(e.target.value)}
            />
          </label>
          <span className="key-state">
            {audio.key_configured ? "Ключ сохранён" : "Ключ не задан"}
          </span>
          <Button type="submit" disabled={key.trim() === "" || disabled}>
            Сохранить ключ
          </Button>
          {audio.key_configured && (
            <Button disabled={disabled} onClick={() => actions.deleteKey(provider.id)}>
              Удалить ключ
            </Button>
          )}
        </form>
      )}
      {!ready && (
        <ul className="audio-checks">
          {audio.checks.map((c) => (
            <li key={c.id} className={`check ${c.state}`}>
              <CheckIcon state={c.state} />
              <span className="check-name">{c.label}</span>
              <span className="check-message">{c.message}</span>
            </li>
          ))}
        </ul>
      )}
      <div className="audio-controls">
        {audio.busy === "" &&
          provider.preparable &&
          !allOk &&
          audio.checks.length > 0 &&
          !ready && (
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
            {provider.start_label}
          </Button>
        )}
        {audio.busy === "" && ready && !audio.listening && (
          <>
            <Button className="primary" disabled={!idle} onClick={actions.startListening}>
              <Mic size={15} />
              Слушать
            </Button>
            <Button disabled={!idle} onClick={actions.stopModel}>
              {provider.stop_label}
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
      {audio.history_enabled && (
        <p className="audio-note">
          Окончательные фразы автоматически сохраняются в историю. Звук не записывается.
        </p>
      )}
      {audio.history_error && (
        <p className="audio-error" role="alert">
          История: {audio.history_error}
        </p>
      )}
      {archive && <h2 className="archive-title">{archive.title}</h2>}
      <TranscriptList
        segments={archive?.segments ?? audio.segments}
        selected={archive ? new Set() : selected}
        onToggle={toggle}
        readOnly={archive !== undefined}
      />
      {!archive && (
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
      )}
    </section>
  );
}
