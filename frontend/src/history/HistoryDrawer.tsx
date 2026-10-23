import { useEffect, useState } from "react";
import { Download, MessageSquare, Pin, PinOff, Plus, Search, Trash2, X } from "lucide-react";
import {
  errorText,
  type DeleteResult,
  type ExportResult,
  type SearchHit,
  type Snapshot,
} from "../bridge/state";
import { Button, IconButton } from "../ui/Button";

const plural = new Intl.PluralRules("ru-RU");
const chatWords: Partial<Record<Intl.LDMLPluralRule, string>> = {
  one: "чат",
  few: "чата",
  many: "чатов",
};
function chatsCount(n: number) {
  return `${n} ${chatWords[plural.select(n)] ?? "чата"}`;
}
type Row = {
  id: string;
  title: string;
  pinned: boolean;
  updatedAt: string;
  detail: string;
};
export function HistoryDrawer({
  snapshot,
  busy,
  onOpen,
  onNew,
  onDelete,
  onPin,
  onExport,
  onReveal,
  search,
}: {
  snapshot: Snapshot;
  busy: boolean;
  onOpen: (id: string) => void;
  onNew: () => void;
  onDelete: (ids: string[]) => Promise<DeleteResult | undefined>;
  onPin: (id: string, pinned: boolean) => void;
  onExport: (id: string) => Promise<ExportResult | undefined>;
  onReveal: (name: string) => void;
  search: (query: string) => Promise<SearchHit[]>;
}) {
  const [query, setQuery] = useState("");
  // Results of the latest finished search; ignored once the field is cleared.
  const [found, setFound] = useState<{ hits: SearchHit[] | null; error: string }>({
    hits: null,
    error: "",
  });
  const [selecting, setSelecting] = useState(false);
  const [picked, setPicked] = useState<string[]>([]);
  const [confirming, setConfirming] = useState(false);
  const [report, setReport] = useState<DeleteResult | null>(null);
  const [exported, setExported] = useState<ExportResult | null>(null);
  const canChange = snapshot.controls.can_switch && !busy;
  // Results are refreshed when the history changes (deletion, rename, new answers).
  const historyKey = snapshot.history.map((h) => `${h.id}:${h.updated_at}:${h.pinned}`).join("|");
  const trimmed = query.trim();
  const hits = trimmed === "" ? null : found.hits;
  const searchError = trimmed === "" ? "" : found.error;
  useEffect(() => {
    if (trimmed === "") return;
    let live = true;
    const timer = setTimeout(() => {
      search(trimmed).then(
        (result) => {
          if (live) setFound({ hits: result, error: "" });
        },
        (error: unknown) => {
          if (live) setFound((f) => ({ ...f, error: errorText(error) }));
        },
      );
    }, 250);
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [trimmed, historyKey, search]);
  const known = new Set(snapshot.history.map((h) => h.id));
  const selected = picked.filter((id) => known.has(id));
  const rows: Row[] =
    hits === null
      ? snapshot.history.map((h) => ({
          id: h.id,
          title: h.title,
          pinned: h.pinned,
          updatedAt: h.updated_at,
          detail: h.prepared ? "Подготовлен" : "",
        }))
      : hits
          .filter((h) => known.has(h.chat_id))
          .map((h) => ({
            id: h.chat_id,
            title: h.title,
            pinned: h.pinned,
            updatedAt: h.updated_at,
            detail: h.snippet,
          }));
  const toggle = (id: string) =>
    setPicked((ids) => (ids.includes(id) ? ids.filter((v) => v !== id) : [...ids, id]));
  const finishSelection = () => {
    setSelecting(false);
    setPicked([]);
    setConfirming(false);
  };
  const remove = async () => {
    setConfirming(false);
    const result = await onDelete(selected);
    if (result) {
      setReport(result);
      finishSelection();
    } else if (!selecting) setPicked([]);
  };
  return (
    <div className="history-body">
      <div className="history-toolbar">
        <Button className="new-chat-button" disabled={!canChange} onClick={onNew}>
          <Plus size={16} />
          Новый чат
        </Button>
        <Button
          className={selecting ? "selected" : ""}
          aria-pressed={selecting}
          disabled={!selecting && snapshot.history.length === 0}
          onClick={() => (selecting ? finishSelection() : setSelecting(true))}
        >
          {selecting ? "Готово" : "Выбрать"}
        </Button>
      </div>
      <label className="history-search">
        <Search size={15} aria-hidden="true" />
        <input
          type="search"
          aria-label="Поиск по чатам"
          placeholder="Поиск по названию и тексту"
          maxLength={200}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
      </label>
      {searchError && (
        <p className="history-note" role="alert">
          {searchError}
        </p>
      )}
      {selecting && (
        <div className="history-selection">
          <span aria-live="polite">Выбрано: {selected.length}</span>
          <Button
            onClick={() =>
              setPicked(selected.length === rows.length ? [] : rows.map((row) => row.id))
            }
          >
            {selected.length === rows.length && rows.length > 0 ? "Снять выбор" : "Выбрать все"}
          </Button>
          <Button
            className="danger"
            disabled={!canChange || selected.length === 0}
            onClick={() => setConfirming(true)}
          >
            <Trash2 size={14} />
            Удалить
          </Button>
        </div>
      )}
      {confirming && selected.length > 0 && (
        <div className="history-confirm" role="alertdialog" aria-label="Подтверждение удаления">
          <p>
            Удалить {chatsCount(selected.length)} вместе со снимками, черновиками и очередью?
            {selected.includes(snapshot.active_chat_id) &&
              " Текущий чат будет закрыт, откроется другой."}{" "}
            Действие необратимо.
          </p>
          <div>
            <Button className="danger" disabled={!canChange} onClick={() => void remove()}>
              Удалить
            </Button>
            <Button
              onClick={() => {
                setConfirming(false);
                if (!selecting) setPicked([]);
              }}
            >
              Отмена
            </Button>
          </div>
        </div>
      )}
      {report && (
        <div className="history-report" role="status">
          <div>
            <span>Удалено: {chatsCount(report.deleted.length)}.</span>
            <IconButton label="Скрыть отчёт" onClick={() => setReport(null)}>
              <X size={14} />
            </IconButton>
          </div>
          {report.failed.length > 0 && (
            <ul role="alert">
              {report.failed.map((f) => (
                <li key={f.id}>
                  Не удалось удалить «{f.title}»: {f.error}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
      {exported && (
        <div className="history-report" role="status">
          <div>
            <span>Экспортировано в {exported.path}</span>
            <IconButton label="Скрыть отчёт" onClick={() => setExported(null)}>
              <X size={14} />
            </IconButton>
          </div>
          {exported.missing > 0 && (
            <p>Недоступных снимков: {exported.missing}; в файле оставлена отметка.</p>
          )}
          <Button disabled={busy} onClick={() => onReveal(exported.name)}>
            Открыть папку
          </Button>
        </div>
      )}
      <div className="history-list">
        {hits !== null && rows.length === 0 && <p className="history-note">Ничего не найдено</p>}
        {rows.map((row) => (
          <div
            className={`history-item ${row.id === snapshot.active_chat_id ? "active" : ""}`}
            key={row.id}
          >
            {selecting && (
              <input
                type="checkbox"
                aria-label={`Выбрать чат ${row.title}`}
                checked={selected.includes(row.id)}
                onChange={() => toggle(row.id)}
              />
            )}
            <button
              type="button"
              disabled={!canChange}
              onClick={() => (selecting ? toggle(row.id) : onOpen(row.id))}
            >
              {row.pinned ? <Pin size={17} /> : <MessageSquare size={17} />}
              <span>
                <strong>{row.title}</strong>
                <small>
                  {row.detail && hits === null ? `${row.detail} · ` : ""}
                  {new Date(row.updatedAt).toLocaleDateString("ru-RU", {
                    day: "numeric",
                    month: "short",
                  })}
                </small>
                {hits !== null && <small className="history-snippet">{row.detail}</small>}
              </span>
            </button>
            {!selecting && (
              <div className="history-actions">
                <IconButton
                  label={row.pinned ? `Открепить чат ${row.title}` : `Закрепить чат ${row.title}`}
                  className={row.pinned ? "selected" : ""}
                  disabled={busy}
                  onClick={() => onPin(row.id, !row.pinned)}
                >
                  {row.pinned ? <PinOff size={14} /> : <Pin size={14} />}
                </IconButton>
                <IconButton
                  label={`Экспортировать чат ${row.title}`}
                  disabled={!canChange}
                  onClick={() => {
                    void onExport(row.id).then((result) => {
                      if (result) setExported(result);
                    });
                  }}
                >
                  <Download size={14} />
                </IconButton>
                <IconButton
                  label={`Удалить чат ${row.title}`}
                  disabled={!canChange}
                  onClick={() => {
                    setPicked([row.id]);
                    setConfirming(true);
                  }}
                >
                  <Trash2 size={14} />
                </IconButton>
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
