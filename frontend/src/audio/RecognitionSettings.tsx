import { useState } from "react";
import { CircleCheck, CircleDashed, CircleHelp, CircleX } from "lucide-react";
import type { AudioState } from "../bridge/state";
import { Button } from "../ui/Button";
import { sourceLabel, type AudioActions } from "./actions";
import { checksPassed } from "./readiness";

type Check = AudioState["checks"][number];
function CheckIcon({ state }: { state: Check["state"] }) {
  if (state === "ok") return <CircleCheck size={15} aria-label="готово" />;
  if (state === "error") return <CircleX size={15} aria-label="ошибка" />;
  if (state === "missing") return <CircleDashed size={15} aria-label="не готово" />;
  return <CircleHelp size={15} aria-label="неизвестно" />;
}

// RecognitionSettings shows the provider choice, key, readiness checks, setup commands and audio
// devices exactly as Go reports them; it never decides readiness itself. The saved key is never
// shown: the field is write-only and clears after saving.
export function RecognitionSettings({
  audio,
  disabled,
  actions,
}: {
  audio: AudioState;
  disabled: boolean;
  actions: AudioActions;
}) {
  const [key, setKey] = useState("");
  if (!audio.available)
    return (
      <section className="settings-section" aria-label="Распознаватель">
        <h2>Распознаватель</h2>
        <p className="audio-note">Распознавание речи недоступно в этой сборке</p>
      </section>
    );
  const provider = audio.provider;
  const idle = audio.busy === "" && !disabled;
  const ready = audio.model === "ready";
  const running = audio.busy !== "" || audio.listening || ready || audio.model === "starting";
  return (
    <>
      <section className="settings-section" aria-label="Распознаватель">
        <h2>Распознавание</h2>
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
        {running && audio.providers.length > 1 && (
          <p className="audio-note">Распознаватель меняется только после отключения.</p>
        )}
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
        {ready ? (
          <p className="audio-note">Распознаватель подключён.</p>
        ) : (
          <ul className="audio-checks" aria-label="Готовность">
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
            !checksPassed(audio) &&
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
          {audio.busy === "" && ready && !audio.listening && (
            <Button disabled={!idle} onClick={actions.stopModel}>
              {provider.stop_label}
            </Button>
          )}
          {audio.busy !== "" && (
            <span className="audio-progress" aria-live="polite">
              {audio.progress || "Выполняется…"}
            </span>
          )}
        </div>
        {audio.history_enabled && (
          <p className="audio-note">
            Окончательные фразы автоматически сохраняются в историю чата. Звук не записывается.
          </p>
        )}
      </section>
      <section className="settings-section" aria-label="Источники аудио">
        <h2>Источники аудио</h2>
        {audio.sources.length > 0 ? (
          <ul className="audio-sources">
            {audio.sources.map((s) => (
              <li key={s.track} className={s.active ? "active" : ""}>
                {sourceLabel[s.track]}: {s.device || "—"}
              </li>
            ))}
          </ul>
        ) : (
          <p className="audio-note">
            Системный звук и микрофон по умолчанию подключаются при начале прослушивания.
          </p>
        )}
      </section>
    </>
  );
}
