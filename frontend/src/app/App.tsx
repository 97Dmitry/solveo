import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import {
  Camera,
  ChevronDown,
  CircleHelp,
  Code2,
  History,
  Image,
  Layers,
  Mic,
  Pin,
  Plus,
  Search,
  Settings,
  SlidersHorizontal,
  X,
} from "lucide-react";
import {
  actions,
  client,
  defaultHotkeys,
  onAudioInsert,
  loadAccount,
  loadModels,
  searchHistory,
  suspendHotkeys,
} from "../bridge/wails";
import { ChatView } from "../chat/ChatView";
import { AnswerReference } from "../chat/AnswerReference";
import { Composer } from "../chat/Composer";
import { AttachmentStrip } from "../chat/AttachmentStrip";
import { lastAnswerText, latestCode } from "../chat/code";
import { ChatSettings } from "../settings/ChatSettings";
import { AppSettings } from "../settings/AppSettings";
import { HelpPanel } from "../help/HelpPanel";
import { HistoryDrawer } from "../history/HistoryDrawer";
import { Button, IconButton } from "../ui/Button";
import { Drawer } from "../ui/Drawer";
import { Menu } from "../ui/Menu";
import "./theme.css";
type Panel = "closed" | "configuration" | "settings" | "history" | "images" | "help";
export default function App() {
  const state = useSyncExternalStore(client.subscribe, client.getState);
  const composerRef = useRef<HTMLTextAreaElement>(null);
  const [panel, setPanel] = useState<Panel>("closed");
  // The help panel opens settings directly on the shortcuts tab.
  const [settingsTab, setSettingsTab] = useState<"general" | "hotkeys">("general");
  const [notice, setNotice] = useState("");
  // 0 hides conversation search; every Ctrl+F increments it to refocus the field.
  const [chatSearch, setChatSearch] = useState(0);
  const copy = useCallback(async (text: string) => {
    const ok = await actions.copy(text);
    if (!ok) setNotice("Не удалось скопировать в буфер обмена");
    return ok;
  }, []);
  // Stable callbacks let the memoized ChatView skip rendering while typing.
  const activeChatID = state.status === "ready" ? state.snapshot.active_chat_id : "";
  const followup = useCallback((id: string, kind: string, text: string) => {
    if (client.prepareFollowup(id, kind, text)) composerRef.current?.focus();
  }, []);
  const reuseQuestion = useCallback((id: string) => {
    if (client.reuseQuestion(id)) composerRef.current?.focus();
  }, []);
  const configure = useCallback(() => setPanel("configuration"), []);
  const reuseImage = useCallback(
    (id: string) => {
      void actions.reuseImage(activeChatID, id);
    },
    [activeChatID],
  );
  const openImage = useCallback(
    (id: string) => {
      void actions.image(activeChatID, id);
    },
    [activeChatID],
  );
  const retry = useCallback((id: string) => {
    void client.submit("", id);
  }, []);
  const closeSearch = useCallback(() => {
    setChatSearch(0);
    composerRef.current?.focus();
  }, []);
  useEffect(() => client.connect(), []);
  useEffect(
    () =>
      onAudioInsert((chatID, text) => {
        const current = client.getState();
        if (current.status !== "ready" || current.snapshot.active_chat_id !== chatID) return false;
        const ok = client.insertTranscript(text);
        if (ok) composerRef.current?.focus();
        return ok;
      }),
    [],
  );

  // Ctrl+F searches the open conversation; e.code keeps it layout-independent.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && !e.altKey && !e.shiftKey && e.code === "KeyF") {
        if (document.querySelector("dialog[open]")) return;
        e.preventDefault();
        setChatSearch((n) => n + 1);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
  useEffect(() => {
    if (state.status === "ready") {
      document.documentElement.dataset["theme"] = state.snapshot.settings.theme;
      document.documentElement.style.setProperty(
        "--ui-scale",
        String(state.snapshot.settings.ui_scale / 100),
      );
      document.documentElement.style.setProperty(
        "--chat-size",
        `${state.snapshot.settings.font_size}px`,
      );
    }
  }, [state]);
  if (state.status === "loading")
    return (
      <main className="boot-state">
        <span className="brand-name">Solveo</span>
        <p>Подключаю чат…</p>
      </main>
    );
  if (state.status === "error")
    return (
      <main className="boot-state">
        <h1>Не удалось открыть чат</h1>
        <p role="alert">{state.error}</p>
        <Button onClick={() => location.reload()}>Повторить подключение</Button>
      </main>
    );
  const s = state.snapshot;
  const busy = state.working;
  const close = () => setPanel("closed");
  const lastAnswer = lastAnswerText(s.messages);
  const code = latestCode(lastAnswer);
  const error = state.error || s.error;
  const editorDisabled = (busy && !state.submitting) || s.operation.phase === "save-pending";
  const canAddImage = !busy && s.controls.can_submit && s.queue.length < 20;
  const canPrepareFollowup =
    !busy &&
    !state.editor.composing &&
    s.controls.can_submit &&
    state.editor.text === "" &&
    !state.editor.replyToID;
  const receiving =
    s.operation.phase === "requesting" &&
    state.stream.blocks.some(
      (b) => b.chat_id === s.active_chat_id && b.operation_id === s.operation.id && b.text !== "",
    );
  const operationLabel =
    state.submitting && s.operation.phase === "idle"
      ? "Подготавливаю запрос…"
      : receiving
        ? "Получаю ответ…"
        : s.operation.message ||
          (error
            ? "Ошибка запроса"
            : s.provider_id === s.provider.id
              ? "Готов к работе"
              : "Архив доступен для чтения");
  const panelTitle = {
    configuration: "Преднастройка чата",
    settings: "Настройки Solveo",
    history: "История чатов",
    images: "Снимки чата",
    help: "Справка и горячие клавиши",
    closed: "",
  } satisfies Record<Panel, string>;
  return (
    <main className="app-shell">
      <header className="app-header">
        <div className="brand">
          <svg width="26" height="28" viewBox="0 0 26 28" fill="none" aria-hidden="true">
            <path
              d="M20 4H9a5 5 0 0 0 0 10h8a5 5 0 0 1 0 10H6"
              stroke="currentColor"
              strokeWidth="3.2"
              strokeLinecap="round"
            />
            <path
              d="m17 1 4 3-4 3M9 21l-4 3 4 3"
              stroke="currentColor"
              strokeWidth="2.3"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
          <div>
            <span className="brand-name">Solveo</span>
            <span className="chat-title" title={s.title}>
              {s.title}
            </span>
          </div>
        </div>
        <nav className="header-actions">
          <IconButton
            label={s.settings.always_on_top ? "Открепить окно" : "Поверх всех окон"}
            aria-pressed={s.settings.always_on_top}
            className={s.settings.always_on_top ? "selected" : ""}
            disabled={busy}
            onClick={() => {
              void actions.pin(!s.settings.always_on_top);
            }}
          >
            <Pin size={17} />
          </IconButton>
          <IconButton
            label="Поиск в разговоре (Ctrl+F)"
            className={chatSearch ? "selected" : ""}
            onClick={() => setChatSearch((n) => (n ? 0 : 1))}
          >
            <Search size={17} />
          </IconButton>
          <IconButton
            label={
              state.audio.listening ? "Распознавание речи (идёт запись)" : "Распознавание речи"
            }
            className={`audio-toggle ${state.audio.listening ? "recording" : ""}`}
            onClick={() => void actions.showAudioWindow()}
          >
            <Mic size={17} />
          </IconButton>
          <IconButton label="История чатов" onClick={() => setPanel("history")}>
            <History size={18} />
          </IconButton>
          <IconButton
            label="Новый чат"
            disabled={busy || !s.controls.can_switch}
            onClick={() => {
              void actions.newChat();
            }}
          >
            <Plus size={20} />
          </IconButton>
          <IconButton label="Справка и горячие клавиши" onClick={() => setPanel("help")}>
            <CircleHelp size={18} />
          </IconButton>
          <IconButton
            label="Настройки Solveo"
            onClick={() => {
              setSettingsTab("general");
              setPanel("settings");
            }}
          >
            <Settings size={18} />
          </IconButton>
        </nav>
      </header>
      <div className="context-bar">
        <Button
          className="config-chip"
          onClick={() => setPanel("configuration")}
          disabled={busy || !s.controls.can_switch}
        >
          <SlidersHorizontal size={14} />
          <span>Преднастройка</span>
          {(s.config.context || s.config.instructions) && (
            <span className="saved-dot" title="Сохранена" />
          )}
        </Button>
        <span className="provider-label">
          {s.provider_id === s.provider.id ? s.provider.label : `${s.provider_id} недоступен`}
        </span>
        {s.images.length > 0 && (
          <Button
            className="images-chip"
            aria-label={`Снимки чата: ${s.images.length}`}
            title="Все снимки этого чата"
            onClick={() => setPanel("images")}
          >
            <Image size={14} />
            {s.images.length}
          </Button>
        )}
      </div>
      {error && (
        <div className="error-banner" role="alert">
          <span>{error}</span>
          {s.controls.can_discard && (
            <Button
              onClick={() => {
                setSettingsTab("general");
                setPanel("settings");
              }}
            >
              Восстановление
            </Button>
          )}
        </div>
      )}
      {s.work_error && (
        <div className="error-banner" role="alert">
          <span>{s.work_error}</span>
        </div>
      )}
      {notice && (
        <div className="notice" role="status">
          {notice}
          <IconButton label="Скрыть уведомление" onClick={() => setNotice("")}>
            <X size={14} />
          </IconButton>
        </div>
      )}
      <ChatView
        key={s.active_chat_id}
        snapshot={s}
        stream={state.stream}
        searchFocus={chatSearch}
        onSearchClose={closeSearch}
        canPrepareFollowup={canPrepareFollowup}
        onFollowup={followup}
        onReuse={reuseQuestion}
        onConfigure={configure}
        canReuseImage={canAddImage}
        onReuseImage={reuseImage}
        onOpenImage={openImage}
        onRetry={retry}
        onCopy={copy}
        onOpenLink={actions.openLink}
      />
      <section className="input-area">
        <div className="capture-toolbar">
          <div className="capture-group">
            <Button
              className="capture-button"
              title="Отправит только новый снимок; текст и очередь останутся"
              disabled={busy || !s.controls.can_capture}
              onClick={() => {
                void actions.capture("quick-region");
              }}
            >
              <Camera size={16} />
              Снимок
            </Button>
            <Menu
              label="Варианты снимка"
              className="capture-options"
              disabled={busy || !s.controls.can_capture}
              side="top"
              items={[
                {
                  label: "Весь экран и ответ",
                  title: "Отправит только новый снимок; текст и очередь останутся",
                  onSelect: () => {
                    void actions.capture("quick-full");
                  },
                },
              ]}
            >
              <ChevronDown size={14} />
            </Menu>
            <Button
              disabled={busy || !s.controls.can_capture}
              onClick={() => {
                void actions.capture("queue-region");
              }}
            >
              <Layers size={15} />В очередь
              {s.queue.length > 0 && (
                <span className="count" title="Изображений к вопросу, не более 20">
                  {s.queue.length}
                </span>
              )}
            </Button>
          </div>
          <div className="answer-tools">
            <IconButton
              label="Скопировать последний код"
              disabled={!code}
              onClick={() => {
                void copy(code);
              }}
            >
              <Code2 size={17} />
            </IconButton>
          </div>
        </div>
        <p className="visually-hidden" aria-live="polite">
          {s.queue.length ? `К вопросу: ${s.queue.length} из 20 изображений` : "Изображений нет"}
        </p>
        <AttachmentStrip
          items={s.queue}
          onReorder={(ids) => {
            void actions.reorder(s.active_chat_id, ids);
          }}
          disabled={busy || !s.controls.can_switch}
          onOpen={(id) => {
            void actions.image(s.active_chat_id, id);
          }}
          onRemove={(id) => {
            void actions.remove(s.active_chat_id, id);
          }}
        />
        {state.editor.replyToID && (
          <AnswerReference
            messages={s.messages}
            messageID={state.editor.replyToID}
            kind={state.editor.replyToKind}
            {...(!editorDisabled ? { onClear: () => client.clearReference() } : {})}
          />
        )}
        <Composer
          textareaRef={composerRef}
          canAddImage={canAddImage}
          onAddImage={(file) => {
            void actions.importImage(s.active_chat_id, file);
          }}
          onImageError={(message) => {
            void actions.imageError(message);
          }}
          text={state.editor.text}
          disabled={editorDisabled}
          canSend={
            !busy &&
            s.controls.can_submit &&
            (state.editor.text.trim().length > 0 || s.queue.length > 0)
          }
          busy={s.controls.can_cancel}
          onChange={(text) => client.edit(text)}
          onSend={() => {
            void client.submit();
          }}
          onCancel={() => {
            void actions.cancel(s.operation.id);
          }}
          onComposition={(v) => client.setComposing(v)}
        />
        <footer className="status-bar">
          <span className={`status-dot ${s.operation.phase !== "idle" ? "active" : ""}`} />
          <span aria-live="polite">{operationLabel}</span>
          <span className="hide-hint">
            Shift+Enter — новая строка
            {s.settings.hotkeys.toggle && ` · ${s.settings.hotkeys.toggle} — скрыть`}
          </span>
        </footer>
      </section>
      {panel !== "closed" && (
        <Drawer title={panelTitle[panel]} onClose={close}>
          {panel === "configuration" && (
            <ChatSettings
              loadModels={loadModels}
              busy={state.working}
              profileActions={{
                save: actions.saveProfile,
                remove: actions.deleteProfile,
                reload: actions.reloadProfiles,
                create: actions.newChatFromProfile,
              }}
              key={`${s.active_chat_id}:${s.config_revision}`}
              snapshot={s}
              onClose={close}
              onSave={(title, config) =>
                actions.saveConfig(s.active_chat_id, title, config, s.config_revision)
              }
            />
          )}
          {panel === "help" && (
            <HelpPanel
              hotkeys={s.settings.hotkeys}
              errors={s.hotkey_errors}
              onEditHotkeys={() => {
                setSettingsTab("hotkeys");
                setPanel("settings");
              }}
            />
          )}
          {panel === "settings" && (
            <AppSettings
              initialTab={settingsTab}
              busy={busy}
              hotkeyActions={{ loadDefaults: defaultHotkeys, suspend: suspendHotkeys }}
              connection={{
                loadAccount,
                onLogin: () => {
                  void actions.startLogin();
                },
                onCancelLogin: () => {
                  void actions.cancelLogin();
                },
                onOpenLink: actions.openLink,
              }}
              onExit={() => {
                void actions.exit();
              }}
              snapshot={s}
              onSettings={(cfg) => {
                void actions.settings(cfg);
              }}
              onCheck={() => {
                void actions.check();
              }}
              onLog={() => {
                void actions.log();
              }}
              onRetrySave={() => {
                void actions.retrySave();
              }}
              onDiscard={() => {
                void actions.discard().then((ok) => {
                  if (ok) close();
                });
              }}
            />
          )}
          {panel === "history" && (
            <HistoryDrawer
              snapshot={s}
              busy={busy}
              search={searchHistory}
              onPin={(id, pinned) => {
                void actions.pinChat(id, pinned);
              }}
              onExport={actions.exportChat}
              onReveal={(name) => {
                void actions.revealExport(name);
              }}
              onNew={() => {
                void actions.newChat().then((ok) => {
                  if (ok) close();
                });
              }}
              onOpen={(id) => {
                void actions.openChat(id).then((ok) => {
                  if (ok) close();
                });
              }}
              onDelete={actions.deleteChats}
            />
          )}
          {panel === "images" && (
            <div className="chat-images">
              <p>Все сохранённые снимки этого чата</p>
              <AttachmentStrip
                items={s.images}
                disabled={!canAddImage}
                onReuse={(id) => {
                  void actions.reuseImage(s.active_chat_id, id);
                }}
                onOpen={(id) => {
                  void actions.image(s.active_chat_id, id);
                }}
              />
              {s.images.length === 0 && <p>Снимков пока нет</p>}
            </div>
          )}
        </Drawer>
      )}
    </main>
  );
}
