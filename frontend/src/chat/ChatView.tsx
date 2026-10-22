import { useEffect, useRef, useState } from "react";
import { ArrowDown, Check, Copy, SlidersHorizontal } from "lucide-react";
import { recoveredText, streamText, type Snapshot, type StreamSnapshot } from "../bridge/state";
import { AnswerBlocks } from "./AnswerBlocks";
import { Markdown } from "./Markdown";
import { AttachmentStrip } from "./AttachmentStrip";
import { AnswerReference } from "./AnswerReference";
import { FollowupActions } from "./FollowupActions";
import { answerElementID, type FollowupHandler } from "./followup";
import { Button, IconButton } from "../ui/Button";
export function ChatView({
  snapshot,
  stream,
  onConfigure,
  onOpenImage,
  onReuseImage,
  canReuseImage = false,
  onRetry,
  onCopy,
  onOpenLink,
  onFollowup,
  onReuse,
  canPrepareFollowup = false,
}: {
  onFollowup?: FollowupHandler;
  onReuse?: (id: string) => void;
  canPrepareFollowup?: boolean;
  snapshot: Snapshot;
  stream: StreamSnapshot;
  onConfigure: () => void;
  onOpenImage: (id: string) => void;
  onReuseImage?: (id: string) => void;
  canReuseImage?: boolean;
  onRetry: (id: string) => void;
  onCopy: (text: string) => Promise<boolean>;
  onOpenLink: (url: string) => void;
}) {
  const scroll = useRef<HTMLDivElement>(null);
  const nearBottom = useRef(true);
  const [unread, setUnread] = useState(false);
  const [copied, setCopied] = useState("");
  const completedBlocks = snapshot.messages.reduce(
    (count, message) =>
      count + (message.answers?.filter((answer) => answer.status !== "requesting").length ?? 0),
    0,
  );
  useEffect(() => {
    if (nearBottom.current) {
      scroll.current?.scrollTo({ top: scroll.current.scrollHeight });
    } else setUnread(true);
  }, [snapshot.messages.length, snapshot.active_chat_id, completedBlocks, stream.revision]);
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
              <div key={message.id}>
                <article
                  className={`message ${message.role}`}
                  id={answerElementID(message.id, "")}
                  tabIndex={-1}
                >
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
                  {message.reply_to_id && (
                    <AnswerReference
                      messages={snapshot.messages}
                      messageID={message.reply_to_id}
                      kind={message.reply_to_kind ?? ""}
                    />
                  )}
                  <div className="message-body">
                    {message.role === "assistant" ? (
                      <Markdown text={message.text} onCopy={onCopy} onOpenLink={onOpenLink} />
                    ) : (
                      <p className="user-text">{message.text || "Задание на снимках"}</p>
                    )}
                  </div>
                  {message.role === "assistant" &&
                    message.status === "complete" &&
                    message.text.trim() &&
                    onFollowup && (
                      <FollowupActions
                        messageID={message.id}
                        disabled={!canPrepareFollowup}
                        onFollowup={onFollowup}
                      />
                    )}
                  {message.role === "user" && message.text && onReuse && (
                    <Button
                      className="reuse-question"
                      disabled={!canPrepareFollowup}
                      title="Копировать текст в пустой редактор; прежние снимки не добавляются"
                      onClick={() => onReuse(message.id)}
                    >
                      Изменить и отправить заново
                    </Button>
                  )}
                  {message.attachment_ids.length > 0 && (
                    <AttachmentStrip
                      items={message.attachment_ids.flatMap((id) =>
                        snapshot.images.filter((image) => image.id === id),
                      )}
                      disabled={!canReuseImage}
                      {...(onReuseImage ? { onReuse: onReuseImage } : {})}
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
                        <Button onClick={() => onRetry(message.id)}>
                          {message.answers?.length ? "Повторить решение" : "Повторить"}
                        </Button>
                      )}
                    </div>
                  )}
                </article>
                {message.answers ? (
                  <AnswerBlocks
                    messageID={message.id}
                    {...(onFollowup ? { onFollowup } : {})}
                    canPrepareFollowup={canPrepareFollowup}
                    answers={message.answers}
                    previews={{
                      approach: streamText(snapshot, stream, message, "approach"),
                      solution: streamText(snapshot, stream, message, "solution"),
                    }}
                    recovered={{
                      approach: recoveredText(snapshot, message, "approach"),
                      solution: recoveredText(snapshot, message, "solution"),
                    }}
                    onCopy={onCopy}
                    onOpenLink={onOpenLink}
                  />
                ) : (
                  message.role === "user" && (
                    <>
                      <RecoveredPreview
                        text={
                          message.status === "interrupted"
                            ? recoveredText(snapshot, message, "message")
                            : ""
                        }
                        onOpenLink={onOpenLink}
                      />
                      <StreamPreview
                        text={streamText(snapshot, stream, message, "message")}
                        onOpenLink={onOpenLink}
                      />
                    </>
                  )
                )}
              </div>
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

// RecoveredPreview shows text received before the application stopped. The
// answer is incomplete: no copy actions, and it disappears after a retry.
function RecoveredPreview({
  text,
  onOpenLink,
}: {
  text: string;
  onOpenLink: (url: string) => void;
}) {
  if (text === "") return null;
  return (
    <article className="message assistant streaming recovered">
      <div className="message-meta">
        <span>Solveo</span>
        <small>Получено до сбоя, ответ не завершён</small>
      </div>
      <div className="message-body">
        <Markdown text={text} onOpenLink={onOpenLink} />
      </div>
    </article>
  );
}

// StreamPreview shows unfinished text of the running request. It is not an
// answer yet: no copy actions, and Go replaces it with the completed message.
function StreamPreview({ text, onOpenLink }: { text: string; onOpenLink: (url: string) => void }) {
  if (text === "") return null;
  return (
    <article className="message assistant streaming" aria-busy="true">
      <div className="message-meta">
        <span>Solveo</span>
        <small role="status">Получаю ответ…</small>
      </div>
      <div className="message-body">
        <Markdown text={text} onOpenLink={onOpenLink} />
      </div>
    </article>
  );
}
