import { useCallback, useEffect, useState } from "react";
import { audioWindow, type AudioWindowView } from "../bridge/wails";
import { AudioPanel, type AudioActions, type ArchivedTranscript } from "./AudioPanel";
import { QuestionCard } from "./QuestionCard";
import type { QuestionCommand } from "../bridge/state";
import { AudioHistory } from "./AudioHistory";
import { Button } from "../ui/Button";

export function AudioWindow() {
  const [archive, setArchive] = useState<ArchivedTranscript>();
  const [view, setView] = useState<AudioWindowView | null>(null);
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);
  useEffect(() => {
    let stopped = false;
    let ready = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const poll = async () => {
      try {
        const next = await audioWindow.state();
        if (stopped) return;
        setView(next);
        document.documentElement.dataset["theme"] = next.theme;
        document.documentElement.style.setProperty("--ui-scale", String(next.ui_scale / 100));
        if (!ready) {
          await audioWindow.ready();
          ready = true;
          setError("");
        }
      } catch (e) {
        if (!stopped) setError(String(e));
      } finally {
        if (!stopped)
          timer = setTimeout(() => {
            void poll();
          }, 200);
      }
    };
    void poll();
    return () => {
      stopped = true;
      if (timer !== undefined) clearTimeout(timer);
    };
  }, []);
  const execute = useCallback(
    async (
      name: string,
      extra: {
        question?: QuestionCommand;
        provider?: string;
        key?: string;
        ids?: string[];
        text?: string;
        chat_id?: string;
      } = {},
    ) => {
      setPending(true);
      setError("");
      try {
        await audioWindow.execute({ name, ...extra });
      } catch (e) {
        setError(String(e));
      } finally {
        setPending(false);
      }
    },
    [],
  );
  if (!view)
    return (
      <main className="boot-state">
        <p role="status">Подключаю аудио…</p>
        {error && <p role="alert">{error}</p>}
      </main>
    );
  const actions: AudioActions = {
    refresh: () => void execute("refresh"),
    prepare: () => void execute("prepare"),
    startModel: () => void execute("startModel"),
    stopModel: () => void execute("stopModel"),
    startListening: () => void execute("startListening"),
    stopListening: () => void execute("stopListening"),
    clear: () => void execute("clear"),
    send: (ids) => void execute("send", { ids, chat_id: view.chat_id }),
    insert: (text) => void execute("insert", { text, chat_id: view.chat_id }),
    selectProvider: (provider) => void execute("selectProvider", { provider }),
    setKey: (provider, key) => void execute("setKey", { provider, key }),
    deleteKey: (provider) => void execute("deleteKey", { provider }),
  };
  return (
    <main className="audio-window">
      <header className="audio-window-header">
        <div className="audio-window-tools">
          <strong>Распознавание речи</strong>
          <Button
            disabled={pending}
            aria-pressed={view.docked}
            onClick={() => void execute(view.docked ? "float" : "dockLeft")}
          >
            {view.docked ? "Открепить" : "Прикрепить слева"}
          </Button>
        </div>
        <p className="audio-window-chat" title={view.chat_title || "Новый чат"}>
          Чат: {view.chat_title || "Новый чат"}
        </p>
        <details className="audio-window-info">
          <summary>Об окне</summary>
          <p className="audio-window-mode">
            {view.docked ? "Прикреплено слева · следует за основным окном" : "Плавающее окно"}
          </p>
          <small>Закрытие окна не останавливает прослушивание.</small>
        </details>
      </header>
      {(error || view.window_error) && (
        <p className="audio-error" role="alert">
          {error || view.window_error}
        </p>
      )}
      {view.audio.history_enabled && (
        <AudioHistory chatID={view.chat_id} chatTitle={view.chat_title} onArchive={setArchive} />
      )}
      <QuestionCard
        key={view.chat_id}
        view={view.question}
        busy={pending}
        canSend={view.can_send}
        execute={async (question) => {
          setPending(true);
          try {
            await audioWindow.execute({ name: "question", question });
          } finally {
            setPending(false);
          }
        }}
      />
      <AudioPanel
        archive={archive}
        audio={view.audio}
        disabled={pending || view.disabled}
        canSend={!pending && !view.disabled && view.can_send}
        actions={actions}
      />
    </main>
  );
}
