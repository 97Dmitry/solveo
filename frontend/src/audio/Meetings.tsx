import { useEffect, useState } from "react";
import { meetingBridge } from "../bridge/wails";
import type {
  MeetingCommand,
  MeetingDocument,
  MeetingInfo,
  MeetingState,
} from "../bridge/meeting-state";
import { Button } from "../ui/Button";
import "./meetings.css";
const clock = (seconds: number) =>
  new Date(Math.max(0, seconds) * 1000).toISOString().slice(11, 19);
const source = (track: string) => (track === "mic" ? "Микрофон" : "Системный звук");
const pageSize = 100;
export function Meetings({
  state,
  listening,
  active = true,
}: {
  state: MeetingState;
  listening: boolean;
  active?: boolean;
}) {
  const [title, setTitle] = useState("");
  const [system, setSystem] = useState(true);
  const [mic, setMic] = useState(true);
  const [items, setItems] = useState<MeetingInfo[]>([]);
  const [selected, setSelected] = useState("");
  const [doc, setDoc] = useState<MeetingDocument | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [pending, setPending] = useState(false);
  const [reload, setReload] = useState(0);
  const [page, setPage] = useState(0);
  const [part, setPart] = useState("");
  const [rename, setRename] = useState("");
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    let live = true;
    void (async () => {
      try {
        const list = await meetingBridge.list();
        if (!live) return;
        setItems(list.items);
        setNotice(list.notice);
        if (!selected) {
          if (list.items[0]) setSelected(list.items[0].id);
          return;
        }
        const document = await meetingBridge.detail(selected);
        if (live) setDoc(document);
      } catch (e) {
        if (live) setError(String(e));
      }
    })();
    return () => {
      live = false;
    };
  }, [active, selected, state.revision, reload]);
  useEffect(() => {
    if ((!state.recording_id && !state.paused?.retry_at) || !active) return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [state.recording_id, state.paused?.retry_at, active]);
  const run = async (action: string, extra: Partial<MeetingCommand> = {}) => {
    setPending(true);
    setError("");
    try {
      await meetingBridge.command({
        action,
        id: selected,
        title: "",
        tracks: [],
        partial: false,
        ...extra,
      });
      setReload((n) => n + 1);
    } catch (e) {
      setError(String(e));
    } finally {
      setPending(false);
    }
  };
  const copy = async (text: string) => {
    try {
      if (!(await meetingBridge.copy(text))) setError("Не удалось скопировать текст");
    } catch (e) {
      setError(String(e));
    }
  };
  const choose = (id: string) => {
    setSelected(id);
    setDoc(null);
    setPage(0);
    setPart("");
    setRename("");
    setError("");
  };
  const recording = Boolean(state.recording_id);
  const processing = Boolean(state.processing_id);
  const paused = state.paused;
  const retryAt = paused?.retry_at ? Date.parse(paused.retry_at) : 0;
  const retryPending = retryAt > now;
  const cloudBlocked = (kind: string) =>
    retryPending && paused?.id === selected && paused.kind === kind;
  const recordingInfo = items.find((item) => item.id === state.recording_id);
  const ownRecording = state.recording_id === selected;
  const ownProcessing = state.processing_id === selected;
  const incomplete = doc?.status !== "recorded" || doc.parts.some((p) => !p.transcribed);
  const recovery = doc?.status === "recording" || doc?.parts.some((p) => !p.complete);
  const visible = doc?.segments.slice(page * pageSize, (page + 1) * pageSize) ?? [];
  const playable = doc?.parts.filter((p) => p.complete && p.samples > 0) ?? [];
  const playing = playable.find((p) => p.file === part) ?? playable[0];
  const evidence = (id: string) => {
    if (!doc) return;
    const index = doc.segments.findIndex((s) => s.id === id);
    if (index < 0) return;
    setPage(Math.floor(index / pageSize));
    setTimeout(
      () => document.getElementById(`meeting-${id}`)?.scrollIntoView({ block: "center" }),
      0,
    );
  };
  return (
    <section className="meetings" aria-label="Встречи">
      <section className="settings-section">
        <h2>Запись встречи</h2>
        <p className="audio-note">
          Звук сохраняется на этом компьютере. Аудио передаётся в Google только по кнопке
          «Транскрибировать». Summary передаёт только текст. Используется API-ключ Gemini из
          настроек.
        </p>
        {recording ? (
          <div className="meeting-recording" role="status">
            <strong>● Запись {recordingInfo?.title ?? "встречи"}</strong>
            {recordingInfo && (
              <span>{clock((now - Date.parse(recordingInfo.started_at)) / 1000)}</span>
            )}
            <Button disabled={pending} onClick={() => void run("stop")}>
              Остановить запись
            </Button>
          </div>
        ) : (
          <>
            <label>
              Название встречи
              <input
                aria-label="Название встречи"
                value={title}
                maxLength={200}
                onChange={(e) => setTitle(e.target.value)}
                placeholder="Новая встреча"
              />
            </label>
            <div className="meeting-actions">
              <label>
                <input
                  type="checkbox"
                  checked={system}
                  onChange={(e) => setSystem(e.target.checked)}
                />{" "}
                Системный звук
              </label>
              <label>
                <input type="checkbox" checked={mic} onChange={(e) => setMic(e.target.checked)} />{" "}
                Микрофон
              </label>
            </div>
            <Button
              disabled={pending || listening || (!system && !mic)}
              onClick={() =>
                void run("start", {
                  id: "",
                  title: title.trim() || "Новая встреча",
                  tracks: [...(system ? ["system"] : []), ...(mic ? ["mic"] : [])],
                })
              }
            >
              Начать запись
            </Button>
            {listening && (
              <p className="audio-note">
                Для записи встречи сначала остановите живое распознавание.
              </p>
            )}
          </>
        )}
        <p className="audio-note">
          Запись продолжается при скрытии окна и смене чата. Системный источник включает
          уведомления. При использовании колонок микрофон может записывать эхо. WAV: около 346
          МБ/час на источник при 48 кГц; предел 8 часов / 8 GiB.
        </p>
      </section>
      {(error || state.error) && <p role="alert">{error || state.error}</p>}
      {state.notice && <p role="status">{state.notice}</p>}
      {paused && (
        <section aria-label="Обработка на паузе">
          <h3>
            {paused.kind === "summary" ? "Summary" : "Транскрибация"}:{" "}
            {items.find((item) => item.id === paused.id)?.title ?? "встреча"}
          </h3>
          <p role="status">{paused.reason} Готовые результаты сохранены.</p>
          {paused.retry_at && (
            <p className="audio-note">
              Повтор доступен после {new Date(paused.retry_at).toLocaleString()}.
            </p>
          )}
          <Button
            disabled={pending || processing || retryPending || state.recording_id === paused.id}
            onClick={() => void run(paused.kind, { id: paused.id, partial: paused.partial })}
          >
            Продолжить
          </Button>
        </section>
      )}
      {notice && <p role="alert">{notice}</p>}
      {processing && (
        <div role="status">
          {state.stage === "cleanup" ? (
            "Удаление временных файлов Google"
          ) : (
            <>
              {state.stage === "summary" ? "Подготовка summary" : "Транскрибация"}:{" "}
              {state.completed}/{state.total}
            </>
          )}{" "}
          {state.stage !== "cleanup" && (
            <Button disabled={pending} onClick={() => void run("cancel")}>
              Отменить обработку
            </Button>
          )}
        </div>
      )}
      <section className="settings-section">
        <div className="meeting-actions">
          <h2>Сохранённые встречи</h2>
          <Button disabled={pending} onClick={() => setReload((n) => n + 1)}>
            Обновить список
          </Button>
        </div>
        {items.length === 0 ? (
          <p className="audio-note">Записей пока нет.</p>
        ) : (
          <label>
            Встреча
            <select value={selected} onChange={(e) => choose(e.target.value)}>
              {items.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.title} · {new Date(item.started_at).toLocaleString()}
                </option>
              ))}
            </select>
          </label>
        )}
        {doc && (
          <>
            <h3>{doc.title}</h3>
            <p className="audio-note">
              {doc.status === "interrupted" || recovery
                ? "Запись прервана"
                : ownRecording
                  ? "Идёт запись"
                  : "Запись завершена"}{" "}
              · {clock(doc.duration)} · {(doc.bytes / 1048576).toFixed(1)} МБ
            </p>
            {doc.error && <p role="alert">{doc.error}</p>}
            <div className="meeting-actions">
              {recovery && !ownRecording ? (
                <Button disabled={pending || ownProcessing} onClick={() => void run("recover")}>
                  Восстановить запись
                </Button>
              ) : (
                <Button
                  disabled={
                    pending ||
                    processing ||
                    ownRecording ||
                    !doc.parts.length ||
                    cloudBlocked("transcribe")
                  }
                  onClick={() => void run("transcribe")}
                >
                  {doc.parts.some((p) => p.transcribed) && doc.parts.some((p) => !p.transcribed)
                    ? "Продолжить транскрибацию"
                    : "Транскрибировать"}
                </Button>
              )}
              <Button
                disabled={
                  pending ||
                  processing ||
                  ownRecording ||
                  !doc.segments.length ||
                  Boolean(recovery) ||
                  cloudBlocked("summary") ||
                  incomplete
                }
                onClick={() => void run("summary")}
              >
                Сделать summary
              </Button>
              <Button disabled={pending} onClick={() => void run("folder")}>
                Открыть папку
              </Button>
              <Button
                disabled={pending || ownRecording || ownProcessing}
                onClick={() => void run("export")}
              >
                Экспорт текста
              </Button>
            </div>
            {incomplete && doc.segments.length > 0 && !recovery && (
              <div>
                <p className="audio-note">
                  Транскрипт неполный. Обычное summary станет доступно после завершения обработки.
                </p>
                <Button
                  disabled={pending || processing || ownRecording || cloudBlocked("summary")}
                  onClick={() => void run("summary", { partial: true })}
                >
                  Summary доступной части
                </Button>
              </div>
            )}
            <details>
              <summary>Название и аудиофайлы</summary>
              <label>
                Новое название
                <input
                  value={rename}
                  maxLength={200}
                  onChange={(e) => setRename(e.target.value)}
                  placeholder={doc.title}
                />
              </label>
              <Button
                disabled={pending || ownRecording || ownProcessing || !rename.trim()}
                onClick={() => void run("rename", { title: rename.trim() })}
              >
                Переименовать
              </Button>
              {playing && (
                <>
                  <label>
                    Дорожка и часть
                    <select value={playing.file} onChange={(e) => setPart(e.target.value)}>
                      {playable.map((p) => (
                        <option value={p.file} key={p.file}>
                          {source(p.track)} · {clock(p.offset)} · {p.file}
                        </option>
                      ))}
                    </select>
                  </label>
                  <audio
                    key={`${doc.id}/${playing.file}`}
                    controls
                    preload="none"
                    src={`/meeting-audio/${doc.id}/${playing.file}`}
                    aria-label="Аудиозапись встречи"
                  />
                </>
              )}
            </details>
            {doc.summary && (
              <section className="meeting-summary" aria-label="Summary встречи">
                <h3>Summary</h3>
                {doc.summary.partial && <p role="alert">Итоги только доступной части встречи.</p>}
                {doc.summary.revision !== doc.revision && (
                  <p role="alert">Summary устарело: транскрипт изменился.</p>
                )}
                <p>{doc.summary.overview}</p>
                <h4>Темы</h4>
                <ul>
                  {doc.summary.topics.map((text, i) => (
                    <li key={i}>{text}</li>
                  ))}
                </ul>
                {[
                  { name: "Решения", items: doc.summary.decisions },
                  { name: "Задачи", items: doc.summary.tasks },
                ].map((group) => (
                  <section key={group.name}>
                    <h4>{group.name}</h4>
                    {!group.items.length && <p>Не зафиксированы.</p>}
                    <ul>
                      {group.items.map((item, i) => (
                        <li key={i}>
                          {item.text}
                          {item.owner && ` · ${item.owner}`}
                          {item.deadline && ` · ${item.deadline}`}
                          <div className="meeting-evidence">
                            {item.sources.map((id) => (
                              <Button key={id} onClick={() => evidence(id)}>
                                Основание: {id}
                              </Button>
                            ))}
                          </div>
                        </li>
                      ))}
                    </ul>
                  </section>
                ))}
                <h4>Открытые вопросы</h4>
                <ul>
                  {doc.summary.questions.map((text, i) => (
                    <li key={i}>{text}</li>
                  ))}
                </ul>
                <Button onClick={() => void copy(JSON.stringify(doc.summary, null, 2))}>
                  Копировать summary
                </Button>
              </section>
            )}
            <section aria-label="Транскрипт встречи">
              <div className="meeting-actions">
                <h3>Транскрипт</h3>
                <Button
                  disabled={!doc.segments.length}
                  onClick={() =>
                    void copy(
                      doc.segments
                        .map(
                          (s) =>
                            `[${clock(s.start)} · ${source(s.track)} · ${s.speaker}] ${s.text}`,
                        )
                        .join("\n"),
                    )
                  }
                >
                  Копировать транскрипт
                </Button>
              </div>
              <p className="audio-note">
                Говорящие определяются внутри каждой части; одинаковые номера в разных частях не
                означают одного человека. Словарь эфира здесь не используется.
              </p>
              {visible.map((s) => (
                <p id={`meeting-${s.id}`} key={s.id} className="meeting-segment">
                  <small>
                    {clock(s.start)} · {source(s.track)} · {s.speaker || "говорящий не определён"} ·{" "}
                    {s.part}
                  </small>
                  <span>{s.text}</span>
                </p>
              ))}
              {!doc.segments.length && (
                <p className="audio-note">После записи нажмите «Транскрибировать».</p>
              )}
              {doc.segments.length > pageSize && (
                <nav className="meeting-actions" aria-label="Страницы транскрипта">
                  <Button disabled={page === 0} onClick={() => setPage((p) => p - 1)}>
                    Назад
                  </Button>
                  <span>
                    {page + 1} / {Math.ceil(doc.segments.length / pageSize)}
                  </span>
                  <Button
                    disabled={(page + 1) * pageSize >= doc.segments.length}
                    onClick={() => setPage((p) => p + 1)}
                  >
                    Далее
                  </Button>
                </nav>
              )}
            </section>
          </>
        )}
      </section>
    </section>
  );
}
