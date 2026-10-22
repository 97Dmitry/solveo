import { useEffect, useRef } from "react";
import type { TranscriptSegment } from "../bridge/state";
const trackLabel = { system: "Собеседник", mic: "Микрофон" } as const;
const time = new Intl.DateTimeFormat(undefined, {
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
});
// Transcript rows, newest at the bottom. Only final phrases are selectable; the list follows new
// phrases unless the user scrolled up to read.
export function TranscriptList({
  segments,
  selected,
  onToggle,
}: {
  segments: TranscriptSegment[];
  selected: ReadonlySet<string>;
  onToggle: (id: string) => void;
}) {
  const ref = useRef<HTMLOListElement>(null);
  const pinned = useRef(true);
  const last = segments.at(-1);
  useEffect(() => {
    const list = ref.current;
    if (list && pinned.current) list.scrollTop = list.scrollHeight;
  }, [segments.length, last?.text]);
  if (segments.length === 0)
    return <p className="transcript-empty">Фразы появятся здесь во время прослушивания</p>;
  return (
    <ol
      ref={ref}
      className="transcript"
      onScroll={(e) => {
        const list = e.currentTarget;
        pinned.current = list.scrollHeight - list.scrollTop - list.clientHeight < 24;
      }}
    >
      {segments.map((s) => (
        <li key={s.id}>
          <button
            type="button"
            className={`segment ${s.track} ${s.final ? "" : "partial"}`}
            aria-pressed={selected.has(s.id)}
            disabled={!s.final}
            title={s.final ? "Выбрать фразу" : "Фраза ещё распознаётся"}
            onClick={() => onToggle(s.id)}
          >
            <span className="segment-meta">
              <span>{trackLabel[s.track]}</span>
              <time dateTime={s.started_at}>{time.format(new Date(s.started_at))}</time>
            </span>
            <span className="segment-text">{s.text || "…"}</span>
          </button>
        </li>
      ))}
    </ol>
  );
}
