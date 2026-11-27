import { useEffect, useLayoutEffect, useRef, useState } from "react";
import {
  ArrowUp,
  ChevronDown,
  Pause,
  Pin,
  Play,
  Plus,
  Settings2,
  Square,
  type LucideIcon,
} from "lucide-react";
import {
  answersWindow,
  type AnswersCommand,
  type AnswersWindowView,
  type AutoAnswersState,
} from "../bridge/wails";
import { autoAnswersStatus } from "../ui/autoAnswersStatus";
import { Button, IconButton } from "../ui/Button";
import { AnswerPanel } from "./AnswerPanel";

const labels = new Map([
  ["requesting", "Готовится"],
  ["refining", "Уточняется"],
  ["complete", "Готов"],
  ["failed", "Ошибка"],
  ["canceled", "Отменён"],
  ["unknown", "Статус неизвестен"],
  ["interrupted", "Прерван"],
  ["pending", "Не сохранён"],
]);

type PlayerAction = {
  name: Extract<AnswersCommand, "startAnswers" | "pauseAnswers" | "openAudioSettings">;
  label: string;
  Icon: LucideIcon;
};

const pause: PlayerAction = { name: "pauseAnswers", label: "Пауза", Icon: Pause };

function playerAction(state: AutoAnswersState, running: boolean): PlayerAction | undefined {
  switch (state) {
    case "unavailable":
      return undefined;
    case "setup":
      return { name: "openAudioSettings", label: "Настройки аудио", Icon: Settings2 };
    case "paused":
      return { name: "startAnswers", label: "Запустить автоответы", Icon: Play };
    case "starting_model":
    case "starting_capture":
      return { name: "pauseAnswers", label: "Отменить запуск", Icon: Square };
    case "listening":
    case "analyzing":
    case "awaiting_continuation":
    case "answering":
      return pause;
    case "error":
      return running ? pause : { name: "startAnswers", label: "Повторить запуск", Icon: Play };
  }
}

export function AnswersWindow() {
  const [state, setState] = useState<{
    view: AnswersWindowView;
    expanded: string | null | undefined;
  }>();
  const [connectionError, setConnectionError] = useState("");
  const [commandError, setCommandError] = useState("");
  const [pending, setPending] = useState(false);
  const scroll = useRef<HTMLElement>(null);
  const list = useRef<HTMLOListElement>(null);
  const anchor = useRef<{ row: HTMLLIElement; offset: number } | null>(null);
  const sessionID = state?.view.answers.session_id ?? "";

  useEffect(() => {
    let stopped = false;
    let ready = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let previous: AnswersWindowView | undefined;
    const poll = async () => {
      try {
        const next = await answersWindow.state();
        if (stopped) return;
        const { items, session_id: nextSession } = next.answers;
        const newest = items.at(-1)?.id;
        if (
          previous?.answers.session_id === nextSession &&
          previous.answers.items.at(-1)?.id !== newest &&
          scroll.current
        ) {
          const row = list.current?.querySelector<HTMLLIElement>("li[data-expanded='true']");
          if (row) anchor.current = { row, offset: row.offsetTop - scroll.current.scrollTop };
        }
        previous = next;
        document.documentElement.dataset["theme"] = next.theme;
        document.documentElement.style.setProperty("--ui-scale", String(next.ui_scale / 100));
        setState((current) => {
          const selected = current?.expanded;
          const keepSelection =
            current?.view.answers.session_id === nextSession &&
            (selected === null || items.some((item) => item.id === selected));
          return { view: next, expanded: keepSelection ? selected : newest };
        });
        if (!ready) {
          await answersWindow.ready();
          ready = true;
        }
        if (!stopped) setConnectionError("");
      } catch (error) {
        if (!stopped) setConnectionError(String(error));
      } finally {
        if (!stopped) timer = setTimeout(() => void poll(), 200);
      }
    };
    void poll();
    return () => {
      stopped = true;
      if (timer !== undefined) clearTimeout(timer);
    };
  }, []);

  useLayoutEffect(() => {
    if (scroll.current) scroll.current.scrollTop = 0;
  }, [sessionID]);

  useLayoutEffect(() => {
    const saved = anchor.current;
    anchor.current = null;
    if (saved?.row.isConnected && scroll.current)
      scroll.current.scrollTop = saved.row.offsetTop - saved.offset;
  });

  if (!state)
    return (
      <main className="boot-state">
        <p role="status">Открываю автоответы…</p>
        {connectionError && <p role="alert">{connectionError}</p>}
      </main>
    );

  const { view, expanded } = state;
  const { running, available, busy, sessions, items } = view.answers;
  const playerState = view.answers.state;
  const action = playerAction(playerState, running);
  const starting = playerState === "starting_model" || playerState === "starting_capture";
  const newest = items.at(-1);
  const error = commandError || view.answers.error || connectionError;
  const canNavigate = !pending && !busy && !starting;
  const command = async (
    name: AnswersCommand,
    ...target: [sessionID: string, cardID?: string, revision?: string]
  ) => {
    setPending(true);
    setCommandError("");
    try {
      await answersWindow.command(name, ...target);
    } catch (error) {
      setCommandError(String(error));
    } finally {
      setPending(false);
    }
  };
  const select = (id: string | null, row?: HTMLLIElement | null) => {
    anchor.current =
      id && row && scroll.current
        ? { row, offset: row.offsetTop - scroll.current.scrollTop }
        : null;
    setState((current) => current && { ...current, expanded: id });
  };

  return (
    <main className="answers-window">
      <header className="answers-header">
        <div className="answers-heading">
          <h1>Автоответы</h1>
          <IconButton
            label={view.pinned ? "Открепить окно" : "Закрепить поверх окон"}
            className={view.pinned ? "selected" : ""}
            aria-pressed={view.pinned}
            disabled={pending}
            onClick={() => void command("pinAnswers", sessionID)}
          >
            <Pin size={17} />
          </IconButton>
        </div>
        <div className="answers-toolbar">
          <span
            className="answers-listening"
            data-running={
              action?.name === "pauseAnswers" &&
              playerState !== "starting_model" &&
              playerState !== "starting_capture"
            }
            data-state={playerState}
            role="status"
          >
            {autoAnswersStatus[playerState]}
          </span>
          {action && (
            <Button
              className="small primary"
              disabled={pending || (action.name === "startAnswers" && busy)}
              onClick={() => void command(action.name, sessionID)}
            >
              <action.Icon size={13} /> {action.label}
            </Button>
          )}
        </div>
        {playerState === "setup" && view.answers.setup_hint && (
          <p className="answers-note">{view.answers.setup_hint}</p>
        )}
        <div className="answers-toolbar">
          <select
            aria-label="Сессия автоответов"
            value={sessionID}
            disabled={!canNavigate || sessions.length === 0}
            onChange={(event) => void command("openAnswers", event.currentTarget.value)}
          >
            {!sessions.some(({ id }) => id === sessionID) && (
              <option value={sessionID} disabled>
                {sessionID ? "Текущая сессия" : "Выбрать сессию"}
              </option>
            )}
            {sessions.map(({ id, title }) => (
              <option key={id} value={id}>
                {title || "Сессия автоответов"}
              </option>
            ))}
          </select>
          <Button
            className="small"
            disabled={!canNavigate || !available}
            onClick={() => void command("newAnswers", sessionID)}
          >
            <Plus size={13} />
            Новая сессия
          </Button>
        </div>
      </header>
      {error && (
        <div className="answers-error">
          {playerState === "error" && !running && (
            <Button
              className="small"
              disabled={pending}
              onClick={() => void command("openAudioSettings", sessionID)}
            >
              Настройки аудио
            </Button>
          )}
          <span role="alert">{error}</span>
        </div>
      )}
      <section className="answers-scroll" aria-label="Вопросы и ответы" ref={scroll}>
        {newest ? (
          <ol className="answers-list" aria-label="Вопросы" ref={list} key={sessionID}>
            {items.toReversed().map((item) => {
              const open = expanded === item.id;
              const panelID = `answer-${item.id}`;
              return (
                <li key={item.id} data-expanded={open}>
                  <h2>
                    <button
                      type="button"
                      className="answers-question"
                      id={`${panelID}-title`}
                      aria-expanded={open}
                      aria-controls={panelID}
                      onClick={(event) =>
                        select(open ? null : item.id, event.currentTarget.closest("li"))
                      }
                    >
                      <ChevronDown size={15} />
                      <span className="answers-question-text">{item.question}</span>
                      <span className="answers-question-status" data-status={item.status}>
                        {labels.get(item.status) ?? "Статус неизвестен"}
                        {item.revised && <span>Уточнён</span>}
                      </span>
                    </button>
                  </h2>
                  <div
                    id={panelID}
                    role="region"
                    aria-labelledby={`${panelID}-title`}
                    hidden={!open}
                  >
                    {open && (
                      <AnswerPanel
                        item={item}
                        sessionID={sessionID}
                        latest={newest.id === item.id}
                        busy={busy}
                        disabled={pending}
                        execute={(name, ...revision) =>
                          void command(name, sessionID, item.id, ...revision)
                        }
                      />
                    )}
                  </div>
                </li>
              );
            })}
          </ol>
        ) : (
          <div className="answers-empty">
            <p>В этой сессии пока нет вопросов</p>
            <p>Вопросы появятся из расшифровки собеседника.</p>
          </div>
        )}
      </section>
      {newest && newest.id !== expanded && (
        <footer className="answers-footer">
          <Button
            className="small"
            onClick={() => {
              select(newest.id);
              list.current?.querySelector("button")?.focus({ preventScroll: true });
              if (scroll.current) scroll.current.scrollTop = 0;
            }}
          >
            <ArrowUp size={14} /> К новому вопросу
          </Button>
        </footer>
      )}
    </main>
  );
}
