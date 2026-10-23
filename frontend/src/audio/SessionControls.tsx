import { useEffect, useRef } from "react";
import { Mic, Settings2, Square } from "lucide-react";
import type { AudioState } from "../bridge/state";
import { Button } from "../ui/Button";
import type { AudioActions } from "./actions";
import { checksPassed } from "./readiness";

function stateLabel(audio: AudioState) {
  if (audio.listening) return "Слушаю";
  if (audio.model === "ready") return "Готов";
  if (audio.model === "starting") return "Подключаю…";
  if (audio.model === "failed") return "Не удалось подключить";
  return "Не подключён";
}

// SessionControls is the always-visible session lifecycle: connect → listen → stop. Rare setup
// actions (prepare, unload, keys) live in the settings tab. It also requests the first readiness
// check, once per opening and per provider.
export function SessionControls({
  audio,
  disabled,
  actions,
  onSetup,
}: {
  audio: AudioState;
  disabled: boolean;
  actions: AudioActions;
  onSetup: () => void;
}) {
  const needsCheck = audio.available && audio.checks.length === 0 && audio.busy === "";
  const { refresh } = actions;
  // A later empty list means a check is already running.
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
      <div className="session-bar">
        <p className="audio-note">Распознавание речи недоступно в этой сборке</p>
      </div>
    );
  const idle = audio.busy === "" && !disabled;
  const ready = audio.model === "ready";
  const tone = audio.listening ? "listening" : ready ? "ready" : "off";
  let action = null;
  if (audio.listening)
    action = (
      <Button className="listening" disabled={disabled} onClick={actions.stopListening}>
        <Square size={13} />
        Остановить
      </Button>
    );
  else if (audio.busy !== "") action = null;
  else if (ready)
    action = (
      <Button className="primary" disabled={!idle} onClick={actions.startListening}>
        <Mic size={15} />
        Слушать
      </Button>
    );
  else if (checksPassed(audio) && (audio.model === "stopped" || audio.model === "failed"))
    action = (
      <Button className="primary" disabled={!idle} onClick={actions.startModel}>
        {audio.provider.start_label}
      </Button>
    );
  else if (audio.checks.length > 0)
    action = (
      <Button onClick={onSetup}>
        <Settings2 size={15} />
        Настроить
      </Button>
    );
  return (
    <div className="session-bar">
      <div className="session-state">
        <span className={`session-indicator ${tone}`}>
          <span className="session-dot" aria-hidden="true" />
          {stateLabel(audio)}
        </span>
        <span className="session-provider" title={audio.provider.label}>
          {audio.provider.label}
        </span>
        {audio.busy !== "" && (
          <span className="audio-progress" aria-live="polite">
            {audio.progress || "Выполняется…"}
          </span>
        )}
      </div>
      {action}
    </div>
  );
}
