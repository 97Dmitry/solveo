import { useEffect, useRef, useState } from "react";
import { ArrowDown, Check, Copy, SlidersHorizontal } from "lucide-react";
import type { Snapshot } from "../bridge/state";
import { Markdown } from "./Markdown";
import { AttachmentStrip } from "./AttachmentStrip";
import { Button, IconButton } from "../ui/Button";
export function ChatView({
  snapshot,
  onConfigure,
  onOpenImage,
  onRetry,
  onCopy,
  onOpenLink,
}: {
  snapshot: Snapshot;
  onConfigure: () => void;
  onOpenImage: (id: string) => void;
  onRetry: (id: string) => void;
  onCopy: (text: string) => Promise<boolean>;
  onOpenLink: (url: string) => void;
}) {
  const scroll = useRef<HTMLDivElement>(null);
  const nearBottom = useRef(true);
  const [unread, setUnread] = useState(false);
  const [copied, setCopied] = useState("");
  useEffect(() => {
    if (nearBottom.current) {
      scroll.current?.scrollTo({ top: scroll.current.scrollHeight });
    } else setUnread(true);
  }, [snapshot.messages.length, snapshot.active_chat_id]);
  const last = snapshot.messages.at(-1);
  return (
    <div className="chat-container">
      <div
        className="chat-scroll"
        ref={scroll}
        onScroll={(e) => {
          const el = e.currentTarget;
          nearBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
          if (nearBottom.current) setUnread(false);
        }}
      >
        {snapshot.messages.length === 0 ? (
          <div className="empty-chat">
            <div className="empty-mark">S</div>
            <h1>{snapshot.config.context ? "Всё готово к работе" : "С чего начнём?"}</h1>
            <p>
              {snapshot.config.context
                ? "Контекст сохранён. Отправьте вопрос или снимок задания."
                : "Подготовьте контекст заранее или сразу задайте вопрос."}
            </p>
            {snapshot.config.context ? (
              <div className="context-preview">
                <span>Контекст чата</span>
                <p>{snapshot.config.context}</p>
              </div>
            ) : (
              <Button
                className="prepare-button"
                disabled={!snapshot.controls.can_edit_config}
                onClick={onConfigure}
              >
                <SlidersHorizontal size={16} />
                Подготовить чат
              </Button>
            )}
            <div className="shortcut-card">
              <kbd>Ctrl</kbd>
              <span>+</span>
              <kbd>Alt</kbd>
              <span>+</span>
              <kbd>S</kbd>
              <span>снимок и ответ</span>
            </div>
          </div>
        ) : (
          <div className="messages">
            {snapshot.messages.map((message) => (
              <article key={message.id} className={`message ${message.role}`}>
                <div className="message-meta">
                  <span>{message.role === "assistant" ? "Solveo" : "Вы"}</span>
                  {message.role === "assistant" && (
                    <IconButton
                      label={copied === message.id ? "Ответ скопирован" : "Скопировать ответ"}
                      onClick={() => {
                        void onCopy(message.text).then((ok) => {
                          if (ok) setCopied(message.id);
                        });
                      }}
                    >
                      {copied === message.id ? <Check size={13} /> : <Copy size={13} />}
                    </IconButton>
                  )}
                </div>
                <div className="message-body">
                  {message.role === "assistant" ? (
                    <Markdown text={message.text} onCopy={onCopy} onOpenLink={onOpenLink} />
                  ) : (
                    <p className="user-text">{message.text || "Задание на снимках"}</p>
                  )}
                </div>
                {message.attachment_ids.length > 0 && (
                  <AttachmentStrip
                    items={snapshot.images.filter((image) =>
                      message.attachment_ids.includes(image.id),
                    )}
                    disabled={false}
                    onOpen={onOpenImage}
                  />
                )}
                {message.status !== "complete" && message.status !== "requesting" && (
                  <div className="attempt-status">
                    <span>
                      {message.status === "unknown" || message.status === "interrupted"
                        ? "Исход запроса неизвестен. Повтор может отправить вопрос ещё раз."
                        : message.status === "canceled"
                          ? "Запрос отменён"
                          : "Ответ не получен"}
                    </span>
                    {snapshot.controls.can_retry && last?.id === message.id && (
                      <Button onClick={() => onRetry(message.id)}>Повторить</Button>
                    )}
                  </div>
                )}
              </article>
            ))}
          </div>
        )}
      </div>
      {unread && (
        <Button
          className="jump-bottom"
          onClick={() => {
            scroll.current?.scrollTo({ top: scroll.current.scrollHeight, behavior: "smooth" });
            setUnread(false);
          }}
        >
          <ArrowDown size={14} />К новому ответу
        </Button>
      )}
    </div>
  );
}
