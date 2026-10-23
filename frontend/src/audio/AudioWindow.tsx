import { useCallback, useEffect, useState } from "react";
import * as Tabs from "@radix-ui/react-tabs";
import { PanelLeftClose, PanelLeftOpen } from "lucide-react";
import { audioWindow, type AudioWindowView } from "../bridge/wails";
import type { QuestionCommand } from "../bridge/state";
import { IconButton } from "../ui/Button";
import type { AudioActions } from "./actions";
import { AudioAlerts } from "./AudioAlerts";
import { AudioHistory } from "./AudioHistory";
import { LiveTranscript } from "./LiveTranscript";
import { questionCommand } from "./question";
import { QuestionCard } from "./QuestionCard";
import { QuestionSettings } from "./QuestionSettings";
import { RecognitionSettings } from "./RecognitionSettings";
import { needsSetup } from "./readiness";
import { SessionControls } from "./SessionControls";

type Tab = "live" | "history" | "settings";
const isTab = (value: string): value is Tab =>
  value === "live" || value === "history" || value === "settings";

export function AudioWindow() {
  const [tab, setTab] = useState<Tab>("live");
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
  const question = useCallback(async (command: QuestionCommand) => {
    setPending(true);
    try {
      await audioWindow.execute({ name: "question", question: command });
    } finally {
      setPending(false);
    }
  }, []);
  const refresh = useCallback(() => void execute("refresh"), [execute]);
  if (!view)
    return (
      <main className="boot-state">
        <p role="status">Подключаю аудио…</p>
        {error && <p role="alert">{error}</p>}
      </main>
    );
  const chatID = view.chat_id;
  const actions: AudioActions = {
    refresh,
    prepare: () => void execute("prepare"),
    startModel: () => void execute("startModel"),
    stopModel: () => void execute("stopModel"),
    startListening: () => void execute("startListening"),
    stopListening: () => void execute("stopListening"),
    clear: () => void execute("clear"),
    send: (ids) => void execute("send", { ids, chat_id: chatID }),
    insert: (text) => void execute("insert", { text, chat_id: chatID }),
    selectProvider: (provider) => void execute("selectProvider", { provider }),
    setKey: (provider, key) => void execute("setKey", { provider, key }),
    deleteKey: (provider) => void execute("deleteKey", { provider }),
  };
  const disabled = pending || view.disabled;
  const history = view.audio.history_enabled;
  const active = tab === "history" && !history ? "live" : tab;
  const chatTitle = view.chat_title || "Новый чат";
  // Opening settings is the explicit action that requests the builder's model catalog.
  const open = (next: Tab) => {
    setTab(next);
    const q = view.question;
    if (next === "settings" && active !== "settings" && q.available && q.models.length === 0)
      void question(questionCommand(q, "models")).catch((e: unknown) => setError(String(e)));
  };
  return (
    <main className="audio-window">
      <header className="audio-window-header">
        <div className="audio-window-top">
          <p className="audio-window-chat" title={chatTitle}>
            Чат: {chatTitle}
          </p>
          <IconButton
            label={view.docked ? "Открепить" : "Прикрепить слева"}
            disabled={pending}
            aria-pressed={view.docked}
            className={view.docked ? "selected" : ""}
            onClick={() => void execute(view.docked ? "float" : "dockLeft")}
          >
            {view.docked ? <PanelLeftClose size={17} /> : <PanelLeftOpen size={17} />}
          </IconButton>
        </div>
        <SessionControls
          audio={view.audio}
          disabled={disabled}
          actions={actions}
          onSetup={() => open("settings")}
        />
      </header>
      <AudioAlerts audio={view.audio} window={error || view.window_error} />
      <Tabs.Root
        className="audio-tabs"
        value={active}
        onValueChange={(value) => {
          if (isTab(value)) open(value);
        }}
      >
        <Tabs.List className="audio-tab-list" aria-label="Разделы окна распознавания">
          <Tabs.Trigger className="audio-tab" value="live">
            Эфир
            {view.audio.listening && <span className="tab-dot listening" aria-hidden="true" />}
          </Tabs.Trigger>
          {history && (
            <Tabs.Trigger className="audio-tab" value="history">
              История
            </Tabs.Trigger>
          )}
          <Tabs.Trigger className="audio-tab" value="settings">
            Настройки
            {needsSetup(view.audio) && (
              <span className="tab-dot attention" aria-label="требуется настройка" />
            )}
          </Tabs.Trigger>
        </Tabs.List>
        {/* Live and settings stay mounted so local selection, edits and form input survive tab
            switches; history loads only while its tab is open. */}
        <Tabs.Content
          className="audio-tab-panel live"
          value="live"
          forceMount
          hidden={active !== "live"}
        >
          <QuestionCard
            key={chatID}
            view={view.question}
            busy={pending}
            canSend={view.can_send}
            execute={question}
            onOpenSettings={() => open("settings")}
          />
          <LiveTranscript
            audio={view.audio}
            disabled={disabled}
            canSend={!disabled && view.can_send}
            actions={actions}
          />
        </Tabs.Content>
        {history && (
          <Tabs.Content className="audio-tab-panel history" value="history">
            <AudioHistory chatID={chatID} />
          </Tabs.Content>
        )}
        <Tabs.Content
          className="audio-tab-panel settings"
          value="settings"
          forceMount
          hidden={active !== "settings"}
        >
          <RecognitionSettings audio={view.audio} disabled={disabled} actions={actions} />
          <QuestionSettings key={chatID} view={view.question} busy={pending} execute={question} />
          <section className="settings-section" aria-label="Окно">
            <h2>Окно</h2>
            <p className="audio-note">
              {view.docked
                ? "Прикреплено слева: следует за основным окном и скрывается вместе с ним."
                : "Плавающее окно. Кнопка в правом верхнем углу прикрепляет его слева от основного."}
            </p>
            <p className="audio-note">Закрытие окна не останавливает прослушивание.</p>
          </section>
        </Tabs.Content>
      </Tabs.Root>
    </main>
  );
}
