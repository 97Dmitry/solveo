import {
  Fragment,
  useCallback,
  useDeferredValue,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { RefreshCw, Search } from "lucide-react";
import { audioHistory, type AudioHistoryDocument } from "../bridge/wails";
import { Button, IconButton } from "../ui/Button";
import { sourceLabel } from "./actions";
import "./history.css";

const pageSize = 300;
// A pause this long (or a new day) starts a new block with a timestamp heading.
const blockGap = 10 * 60 * 1000;
const date = (value: string) => (value ? new Date(value).toLocaleString() : "—");
const heading = new Intl.DateTimeFormat(undefined, {
  day: "numeric",
  month: "long",
  hour: "2-digit",
  minute: "2-digit",
});
const clock = new Intl.DateTimeFormat(undefined, {
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
});
const hours = new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit" });
const period = (start: string, end: string) => {
  const a = new Date(start);
  if (!end) return `${heading.format(a)} – сейчас`;
  const b = new Date(end);
  return a.toDateString() === b.toDateString()
    ? `${heading.format(a)}–${hours.format(b)}`
    : `${heading.format(a)} – ${heading.format(b)}`;
};
const plural = new Intl.PluralRules("ru");
const phraseWords: Partial<Record<Intl.LDMLPluralRule, string>> = {
  one: "фраза",
  few: "фразы",
  many: "фраз",
};
const phrases = (count: number) => `${count} ${phraseWords[plural.select(count)] ?? "фразы"}`;
const sourceName = (track: string) =>
  track === "system" || track === "mic" ? sourceLabel[track] : "Сеанс";
const errorMessage = (error: unknown) => (error instanceof Error ? error.message : String(error));
type Row = AudioHistoryDocument["segments"][number];
const startsBlock = (row: Row, previous: Row | undefined) => {
  if (!previous) return true;
  const a = new Date(previous.started_at);
  const b = new Date(row.started_at);
  return a.toDateString() !== b.toDateString() || b.getTime() - a.getTime() >= blockGap;
};

function Highlight({ text, query }: { text: string; query: string }) {
  const lower = text.toLowerCase();
  // Case mapping that changes length would misplace the marks.
  if (!query || lower.length !== text.length) return text;
  const parts: ReactNode[] = [];
  let from = 0;
  for (let at = lower.indexOf(query); at >= 0; at = lower.indexOf(query, from)) {
    parts.push(text.slice(from, at), <mark key={at}>{text.slice(at, at + query.length)}</mark>);
    from = at + query.length;
  }
  parts.push(text.slice(from));
  return parts;
}

// AudioHistory is the history tab: it loads the current chat's merged timeline when the tab
// opens and when the chat changes. An epoch prevents closed or superseded loads from displaying
// old chats. Rows are plain selectable text; archive rows never join the live transcript.
export function AudioHistory({ chatID }: { chatID: string }) {
  const [document, setDocument] = useState<AudioHistoryDocument>();
  // undefined shows the latest page.
  const [page, setPage] = useState<number>();
  const [query, setQuery] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [confirmDelete, setConfirmDelete] = useState(false);
  const epoch = useRef(0);
  const list = useRef<HTMLOListElement>(null);
  const loadCurrent = useCallback(async () => {
    const request = ++epoch.current;
    setDocument(undefined);
    setPage(undefined);
    setConfirmDelete(false);
    setBusy(true);
    setError("");
    try {
      const doc = await audioHistory.forChat(chatID);
      if (request === epoch.current) setDocument(doc);
    } catch (cause) {
      if (request === epoch.current) setError(errorMessage(cause));
    } finally {
      if (request === epoch.current) setBusy(false);
    }
  }, [chatID]);
  const invalidate = useCallback(() => {
    epoch.current++;
  }, []);
  useEffect(() => {
    invalidate();
    let cancelled = false;
    queueMicrotask(() => {
      if (!cancelled) void loadCurrent();
    });
    return () => {
      cancelled = true;
      invalidate();
    };
  }, [loadCurrent, invalidate]);
  const remove = async () => {
    if (!document) return;
    const request = ++epoch.current;
    setBusy(true);
    setError("");
    try {
      await audioHistory.deleteChat(chatID);
      if (request !== epoch.current) return;
      setDocument(undefined);
      setPage(undefined);
    } catch (cause) {
      if (request === epoch.current) setError(errorMessage(cause));
    } finally {
      if (request === epoch.current) {
        setBusy(false);
        setConfirmDelete(false);
      }
    }
  };
  const needle = useDeferredValue(query.trim().toLowerCase());
  const matches = useMemo(
    () =>
      needle
        ? (document?.segments.filter((s) => s.text.toLowerCase().includes(needle)) ?? [])
        : (document?.segments ?? []),
    [document, needle],
  );
  const sent = useMemo(
    () => new Set(document?.submissions.flatMap((entry) => entry.segment_ids) ?? []),
    [document],
  );
  const sentText = useMemo(
    () => new Map(document?.segments.map((segment) => [segment.id, segment.text]) ?? []),
    [document],
  );
  const pages = Math.max(1, Math.ceil(matches.length / pageSize));
  const current = Math.min(page ?? pages - 1, pages - 1);
  const rows = matches.slice(current * pageSize, (current + 1) * pageSize);
  const firstRow = rows[0]?.id;
  // Like a chat, every page opens at its latest phrase.
  useEffect(() => {
    if (list.current) list.current.scrollTop = list.current.scrollHeight;
  }, [firstRow, rows.length]);
  const total = document?.segments.length ?? 0;
  const span = document?.started_at && period(document.started_at, document.ended_at);
  return (
    <section className="audio-history" aria-label="Голосовая история чата">
      <div className="history-toolbar">
        <label className="history-search">
          <Search size={15} aria-hidden="true" />
          <input
            type="search"
            aria-label="Поиск по истории"
            placeholder="Поиск по истории"
            value={query}
            disabled={total === 0}
            onChange={(e) => {
              setQuery(e.target.value);
              setPage(undefined);
            }}
          />
        </label>
        <IconButton
          label="Обновить историю чата"
          disabled={busy}
          onClick={() => void loadCurrent()}
        >
          <RefreshCw size={16} />
        </IconButton>
      </div>
      {busy && <p role="status">Загрузка истории…</p>}
      {error && (
        <p className="audio-error" role="alert">
          {error}
        </p>
      )}
      {document?.notice && (
        <p className="audio-error" role="alert">
          {document.notice}
        </p>
      )}
      {!busy && !error && total === 0 && (
        <p className="transcript-empty">Голосовой истории в этом чате пока нет</p>
      )}
      {total > 0 && (
        <p className="history-summary">
          {needle ? `Найдено: ${matches.length} из ${total}` : phrases(total)}
          {span && ` · ${span}`}
        </p>
      )}
      {pages > 1 && (
        <div className="history-pagination">
          <Button disabled={current === 0} onClick={() => setPage(current - 1)}>
            Предыдущие фразы
          </Button>
          <span>
            Страница {current + 1} из {pages}
          </span>
          <Button disabled={current + 1 >= pages} onClick={() => setPage(current + 1)}>
            Следующие фразы
          </Button>
        </div>
      )}
      {total > 0 && needle && matches.length === 0 && (
        <p className="transcript-empty">Ничего не найдено</p>
      )}
      {rows.length > 0 && (
        <ol className="history-list" ref={list}>
          {rows.map((row, index) => (
            <Fragment key={row.id}>
              {startsBlock(row, rows[index - 1]) && (
                <li className="history-block">
                  <time dateTime={row.started_at}>{heading.format(new Date(row.started_at))}</time>
                </li>
              )}
              <li className={`history-row ${row.track}`}>
                <span className="segment-meta">
                  <span>
                    {sourceLabel[row.track]}
                    {row.provider_label ? ` · ${row.provider_label}` : ""}
                  </span>
                  {sent.has(row.id) && <span className="history-sent">отправлено в чат</span>}
                  <time dateTime={row.started_at}>{clock.format(new Date(row.started_at))}</time>
                </span>
                <span className="segment-text">
                  <Highlight text={row.text} query={needle} />
                </span>
              </li>
            </Fragment>
          ))}
        </ol>
      )}
      {document &&
        (document.started_at ||
          total > 0 ||
          document.errors.length > 0 ||
          document.submissions.length > 0) && (
          <details className="history-metadata" key={chatID}>
            <summary>
              Подробности
              {document.errors.length > 0 && ` · ошибок: ${document.errors.length}`}
            </summary>
            <p>
              Начало: {date(document.started_at)} ·{" "}
              {document.ended_at
                ? `Конец: ${date(document.ended_at)}`
                : "Завершение не зафиксировано"}
            </p>
            <p>
              Фраз: {total} · Отправок в чат: {document.submissions.length}
            </p>
            <p className="audio-note">
              Время отражает наблюдение приложения, а не положение в аудиозаписи.
            </p>
            {document.errors.length > 0 && (
              <details>
                <summary>Ошибки: {document.errors.length}</summary>
                <ul>
                  {document.errors.map((entry, index) => (
                    <li key={index}>
                      {date(entry.at)} · {sourceName(entry.track)}: {entry.message}
                    </li>
                  ))}
                </ul>
              </details>
            )}
            {document.submissions.length > 0 && (
              <details>
                <summary>Отправки в чат</summary>
                <ul>
                  {document.submissions.map((entry, index) => (
                    <li key={index}>
                      {date(entry.at)} · Отправлено фраз: {entry.segment_ids.length}
                      <blockquote>
                        {entry.segment_ids
                          .slice(0, 3)
                          .map((id) => sentText.get(id)?.slice(0, 1000) ?? "Реплика недоступна")
                          .join("\n")}
                        {entry.segment_ids.length > 3 ? "\n…" : ""}
                      </blockquote>
                    </li>
                  ))}
                </ul>
              </details>
            )}
            {document.dictionary.length > 0 && (
              <details>
                <summary>Словарь истории</summary>
                <p>{document.dictionary.join(", ")}</p>
              </details>
            )}
            {document.sources.length > 0 && (
              <details>
                <summary>Источники истории</summary>
                <ul>
                  {document.sources.map((source, index) => (
                    <li key={index}>
                      {sourceName(source.track)}: {source.device || "—"} · {source.stream_id}
                    </li>
                  ))}
                </ul>
              </details>
            )}
            {confirmDelete ? (
              <div
                className="history-delete"
                role="group"
                aria-label="Подтверждение удаления истории"
              >
                <p>Удалить голосовую историю этого чата? Восстановить её будет невозможно.</p>
                <Button className="danger" disabled={busy} onClick={() => void remove()}>
                  Подтвердить удаление
                </Button>
                <Button disabled={busy} onClick={() => setConfirmDelete(false)}>
                  Отмена
                </Button>
              </div>
            ) : (
              <Button className="danger" disabled={busy} onClick={() => setConfirmDelete(true)}>
                Удалить голосовую историю этого чата
              </Button>
            )}
          </details>
        )}
    </section>
  );
}
