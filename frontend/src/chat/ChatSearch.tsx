import { useEffect, useRef, useState, type RefObject } from "react";
import { ChevronDown, ChevronUp, X } from "lucide-react";
import { IconButton } from "../ui/Button";
import { clearRanges, findRanges, paintRanges } from "./search";

// ChatSearch finds text in the rendered conversation. `content` changes when
// messages or previews change, so matches are recomputed after each render.
export function ChatSearch({
  focus,
  root,
  content,
  onClose,
}: {
  focus: number;
  root: RefObject<HTMLElement | null>;
  content: string;
  onClose: () => void;
}) {
  const input = useRef<HTMLInputElement>(null);
  const ranges = useRef<Range[]>([]);
  const [query, setQuery] = useState("");
  const [count, setCount] = useState(0);
  const [current, setCurrent] = useState(0);
  useEffect(() => {
    input.current?.focus();
    input.current?.select();
  }, [focus]);
  useEffect(() => clearRanges, []);
  useEffect(() => {
    const el = root.current;
    ranges.current = el ? findRanges(el, query.trim()) : [];
    setCount(ranges.current.length);
    setCurrent((i) => Math.min(i, Math.max(0, ranges.current.length - 1)));
  }, [root, query, content]);
  useEffect(() => paintRanges(ranges.current, current), [current, count, query, content]);
  // Scroll only on navigation: arriving answer text must not move the reader.
  useEffect(() => {
    ranges.current[current]?.startContainer.parentElement?.scrollIntoView?.({ block: "center" });
  }, [current, query, count]);
  const step = (delta: number) => {
    if (count > 0) setCurrent((i) => (i + delta + count) % count);
  };
  return (
    <div className="chat-search" role="search">
      <input
        ref={input}
        type="search"
        aria-label="Поиск в разговоре"
        placeholder="Поиск в разговоре"
        value={query}
        onChange={(e) => {
          setQuery(e.target.value);
          setCurrent(0);
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            step(e.shiftKey ? -1 : 1);
          } else if (e.key === "Escape") {
            e.preventDefault();
            onClose();
          }
        }}
      />
      <span aria-live="polite">
        {query.trim() === "" ? "" : count === 0 ? "Не найдено" : `${current + 1} из ${count}`}
      </span>
      <IconButton label="Предыдущее совпадение" disabled={count === 0} onClick={() => step(-1)}>
        <ChevronUp size={15} />
      </IconButton>
      <IconButton label="Следующее совпадение" disabled={count === 0} onClick={() => step(1)}>
        <ChevronDown size={15} />
      </IconButton>
      <IconButton label="Закрыть поиск" onClick={onClose}>
        <X size={15} />
      </IconButton>
    </div>
  );
}
