import { useCallback, useEffect, useState, useSyncExternalStore } from "react";
import {
  Camera,
  ChevronDown,
  Code2,
  History,
  Image,
  Layers,
  Pin,
  PinOff,
  Plus,
  Settings,
  SlidersHorizontal,
  X,
} from "lucide-react";
import { actions, client, loadAccount, loadModels } from "../bridge/wails";
import { ChatView } from "../chat/ChatView";
import { Composer } from "../chat/Composer";
import { AttachmentStrip } from "../chat/AttachmentStrip";
import { lastAnswerText, latestCode } from "../chat/code";
import { ChatSettings } from "../settings/ChatSettings";
import { AppSettings } from "../settings/AppSettings";
import { HistoryDrawer } from "../history/HistoryDrawer";
import { Button, IconButton } from "../ui/Button";
import { Drawer } from "../ui/Drawer";
import "./theme.css";
type Panel = "closed" | "configuration" | "settings" | "history" | "images";
export default function App() {
  const state = useSyncExternalStore(client.subscribe, client.getState);
  const [panel, setPanel] = useState<Panel>("closed");
  const [captureMenu, setCaptureMenu] = useState(false);
  const [notice, setNotice] = useState("");
  const copy = useCallback(async (text: string) => {
    const ok = await actions.copy(text);
    if (!ok) setNotice("Не удалось скопировать в буфер обмена");
    return ok;
  }, []);
  useEffect(() => client.connect(), []);
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
  const panelTitle = {
    configuration: "Преднастройка чата",
    settings: "Настройки Solveo",
    history: "История чатов",
    images: "Снимки чата",
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
            <span className="chat-title">{s.title}</span>
          </div>
        </div>
        <nav className="header-actions">
          <IconButton
            label={s.settings.always_on_top ? "Открепить окно" : "Поверх всех окон"}
            className={s.settings.always_on_top ? "selected" : ""}
            disabled={busy}
            onClick={() => {
              void actions.pin(!s.settings.always_on_top);
            }}
          >
            {s.settings.always_on_top ? <Pin size={17} /> : <PinOff size={17} />}
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
          <IconButton label="Настройки Solveo" onClick={() => setPanel("settings")}>
            <Settings size={18} />
          </IconButton>
        </nav>
      </header>
      <div className="context-bar">
        <Button
          className="config-chip"
          onClick={() => setPanel("configuration")}
          disabled={busy || !s.controls.can_edit_config}
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
          <Button className="images-chip" onClick={() => setPanel("images")}>
            <Image size={14} />
            {s.images.length}
          </Button>
        )}
      </div>
      {error && (
        <div className="error-banner" role="alert">
          <span>{error}</span>
          {s.controls.can_discard && (
            <Button onClick={() => setPanel("settings")}>Восстановление</Button>
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
        onConfigure={() => setPanel("configuration")}
        onOpenImage={(id) => {
          void actions.image(s.active_chat_id, id);
        }}
        onRetry={(id) => {
          void client.submit("", id);
        }}
        onCopy={copy}
        onOpenLink={actions.openLink}
      />
      <section className="input-area">
        <div className="capture-toolbar">
          <div className="capture-group">
            <Button
              className="capture-button"
              disabled={busy || !s.controls.can_capture}
              onClick={() => {
                void actions.capture("quick-region");
              }}
            >
              <Camera size={16} />
              Снимок
            </Button>
            <div className="capture-options">
              <IconButton
                label="Варианты снимка"
                disabled={busy || !s.controls.can_capture}
                aria-expanded={captureMenu}
                onClick={() => setCaptureMenu((v) => !v)}
              >
                <ChevronDown size={14} />
              </IconButton>
              {captureMenu && (
                <div className="capture-menu">
                  <Button
                    onClick={() => {
                      setCaptureMenu(false);
                      void actions.capture("quick-full");
                    }}
                  >
                    Весь экран и ответ
                  </Button>
                </div>
              )}
            </div>
            <Button
              disabled={busy || !s.controls.can_capture}
              onClick={() => {
                void actions.capture("queue-region");
              }}
            >
              <Layers size={15} />В очередь
              {s.queue.length > 0 && <span className="count">{s.queue.length}</span>}
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
        <AttachmentStrip
          items={s.queue}
          disabled={busy || !s.controls.can_switch}
          onOpen={(id) => {
            void actions.image(s.active_chat_id, id);
          }}
          onRemove={(id) => {
            void actions.remove(s.active_chat_id, id);
          }}
        />
        <Composer
          text={state.editor.text}
          disabled={busy || s.operation.phase === "saving" || s.operation.phase === "save-pending"}
          canSend={
            s.controls.can_submit && (state.editor.text.trim().length > 0 || s.queue.length > 0)
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
          <span aria-live="polite">
            {s.operation.message ||
              (s.provider_id === s.provider.id ? "Готов к работе" : "Архив доступен для чтения")}
          </span>
          <span className="hide-hint">Ctrl+Alt+O — скрыть</span>
        </footer>
      </section>
      {panel !== "closed" && (
        <Drawer title={panelTitle[panel]} onClose={close}>
          {panel === "configuration" && (
            <ChatSettings
              loadModels={loadModels}
              key={`${s.active_chat_id}:${s.config_revision}`}
              snapshot={s}
              onClose={close}
              onSave={(title, config) =>
                actions.saveConfig(s.active_chat_id, title, config, s.config_revision)
              }
            />
          )}{" "}
          {panel === "settings" && (
            <AppSettings
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
              onDelete={(id) => {
                void actions.deleteChat(id);
              }}
            />
          )}
          {panel === "images" && (
            <div className="chat-images">
              <p>Все сохранённые снимки этого чата</p>
              <AttachmentStrip
                items={s.images}
                disabled={false}
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
