import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { audioHistory, type AudioHistoryDocument } from "../bridge/wails";
import { Button } from "../ui/Button";
import type { ArchivedTranscript } from "./AudioPanel";
import "./history.css";

const pageSize = 300;
const date = (value: string) => (value ? new Date(value).toLocaleString() : "—");
const sourceName = (track: string) =>
  track === "system" ? "Собеседник" : track === "mic" ? "Микрофон" : "Сеанс";
const errorMessage = (error: unknown) => (error instanceof Error ? error.message : String(error));

// Requests are initiated by user actions; an epoch prevents closed or superseded loads from
// displaying old sessions. Archive pages never become part of the live transcript.
export function AudioHistory({
  chatID,
  chatTitle,
  onArchive,
}: {
  chatID: string;
  chatTitle: string;
  onArchive: (archive: ArchivedTranscript | undefined) => void;
}) {
  const [open, setOpen] = useState(false);
  const [document, setDocument] = useState<AudioHistoryDocument>();
  const [archiveTitle, setArchiveTitle] = useState("");
  const [page, setPage] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [confirmDelete, setConfirmDelete] = useState(false);
  const epoch = useRef(0);
  const invalidate = useCallback(() => {
    epoch.current++;
  }, []);
  const display = (doc: AudioHistoryDocument, nextPage: number, heading = archiveTitle) => {
    setPage(nextPage);
    onArchive({
      id: doc.id,
      title: heading,
      segments: doc.segments.slice(nextPage * pageSize, (nextPage + 1) * pageSize),
    });
  };
  const loadCurrent = useCallback(async () => {
    const request = ++epoch.current;
    const heading = `Голосовая история: ${chatTitle || "Без названия"}`;
    setDocument(undefined);
    setPage(0);
    setConfirmDelete(false);
    setBusy(true);
    setError("");
    setArchiveTitle(heading);
    onArchive({ id: chatID, title: heading, segments: [] });
    try {
      const doc = await audioHistory.forChat(chatID);
      if (request !== epoch.current) return;
      setDocument(doc);
      onArchive({ id: doc.id, title: heading, segments: doc.segments.slice(0, pageSize) });
    } catch (cause) {
      if (request === epoch.current) setError(errorMessage(cause));
    } finally {
      if (request === epoch.current) setBusy(false);
    }
  }, [chatID, chatTitle, onArchive]);
  useEffect(() => {
    if (!open) return;
    invalidate();
    let cancelled = false;
    queueMicrotask(() => {
      if (!cancelled) void loadCurrent();
    });
    return () => {
      cancelled = true;
      invalidate();
    };
  }, [open, loadCurrent, invalidate]);
  const close = () => {
    epoch.current++;
    setOpen(false);
    setBusy(false);
    setDocument(undefined);
    setConfirmDelete(false);
    onArchive(undefined);
  };
  const remove = async () => {
    if (!document) return;
    const request = ++epoch.current;
    setBusy(true);
    setError("");
    try {
      await audioHistory.deleteChat(chatID);
      if (request !== epoch.current) return;
      setDocument(undefined);
      setPage(0);
      onArchive(undefined);
    } catch (cause) {
      if (request === epoch.current) setError(errorMessage(cause));
    } finally {
      if (request === epoch.current) {
        setBusy(false);
        setConfirmDelete(false);
      }
    }
  };
  const sentText = useMemo(
    () => new Map(document?.segments.map((segment) => [segment.id, segment.text]) ?? []),
    [document],
  );
  const pages = document ? Math.max(1, Math.ceil(document.segments.length / pageSize)) : 1;
  return (
    <section className="audio-history" aria-label="Голосовая история чата">
      <div className="history-toolbar">
        <Button
          aria-expanded={open}
          onClick={() => {
            if (open) close();
            else setOpen(true);
          }}
        >
          {open ? "Эфир" : "История чата"}
        </Button>
        {open && (
          <Button disabled={busy} onClick={() => void loadCurrent()}>
            Обновить историю чата
          </Button>
        )}
      </div>
      {open && (
        <>
          {busy && <p role="status">Загрузка истории…</p>}
          {!busy && !error && (!document || document.segments.length === 0) && (
            <p>Голосовой истории в этом чате пока нет</p>
          )}
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
          {document &&
            (document.started_at ||
              document.segments.length > 0 ||
              document.errors.length > 0 ||
              document.submissions.length > 0) && (
              <>
                <details className="history-metadata" key={chatID}>
                  <summary>Метаданные</summary>
                  <p>
                    Начало: {date(document.started_at)} ·{" "}
                    {document.ended_at
                      ? `Конец: ${date(document.ended_at)}`
                      : "Завершение не зафиксировано"}
                  </p>
                  <p>
                    Фраз: {document.segments.length} · Отправок в чат: {document.submissions.length}
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
                                .map(
                                  (id) => sentText.get(id)?.slice(0, 1000) ?? "Реплика недоступна",
                                )
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
                      <Button disabled={busy} onClick={() => void remove()}>
                        Подтвердить удаление
                      </Button>
                      <Button disabled={busy} onClick={() => setConfirmDelete(false)}>
                        Отмена
                      </Button>
                    </div>
                  ) : (
                    <Button disabled={busy} onClick={() => setConfirmDelete(true)}>
                      Удалить голосовую историю этого чата
                    </Button>
                  )}
                </details>
                {pages > 1 && (
                  <div className="history-pagination">
                    <Button disabled={page === 0} onClick={() => display(document, page - 1)}>
                      Предыдущие фразы
                    </Button>
                    <span>
                      Страница {page + 1} из {pages}
                    </span>
                    <Button
                      disabled={page + 1 >= pages}
                      onClick={() => display(document, page + 1)}
                    >
                      Следующие фразы
                    </Button>
                  </div>
                )}
              </>
            )}
        </>
      )}
    </section>
  );
}
