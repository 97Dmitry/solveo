import { memo, useEffect, useRef, useState } from "react";
import { ArrowDown, Check, Copy, SlidersHorizontal } from "lucide-react";
import {
  recoveredText,
  streamText,
  type AttachmentView,
  type Message,
  type Snapshot,
  type StreamSnapshot,
} from "../bridge/state";
import { AnswerBlocks } from "./AnswerBlocks";
import { Markdown } from "./Markdown";
import { AttachmentStrip } from "./AttachmentStrip";
import { AnswerReference } from "./AnswerReference";
import { ChatSearch } from "./ChatSearch";
import { FollowupActions } from "./FollowupActions";
import { answerElementID, type FollowupHandler } from "./followup";
import { Button, IconButton } from "../ui/Button";
// ChatView is memoized: typing changes only the editor, so the conversation is
// not re-rendered on each keystroke. App passes stable callbacks.
export const ChatView = memo(function ChatView({
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
  searchFocus = 0,
  onSearchClose,
}: {
  // Non-zero shows conversation search; a new value refocuses its field.
  searchFocus?: number;
  onSearchClose?: () => void;
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
  const messagesRef = useRef<HTMLDivElement>(null);
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
      {searchFocus > 0 && onSearchClose && (
        <ChatSearch
          focus={searchFocus}
          root={messagesRef}
          content={`${snapshot.revision}:${stream.revision}`}
          onClose={onSearchClose}
        />
      )}
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
            {snapshot.settings.hotkeys["quick-region"] && (
              <div className="shortcut-card">
                {snapshot.settings.hotkeys["quick-region"].split("+").map((key, i) => (
                  <span key={key} className="shortcut-key">
                    {i > 0 && <span>+</span>}
                    <kbd>{key}</kbd>
                  </span>
                ))}
                <span>снимок и ответ</span>
              </div>
            )}
          </div>
        ) : (
          <div className="messages" ref={messagesRef}>
            {snapshot.messages.map((message) => (
              <MessageRow
                key={message.id}
                message={message}
                messages={message.reply_to_id ? snapshot.messages : undefined}
                images={snapshot.images}
                retry={snapshot.controls.can_retry && last?.id === message.id}
                previewMessage={streamText(snapshot, stream, message, "message")}
                previewApproach={streamText(snapshot, stream, message, "approach")}
                previewSolution={streamText(snapshot, stream, message, "solution")}
                recoveredMessage={
                  message.status === "interrupted"
                    ? recoveredText(snapshot, message, "message")
                    : ""
                }
                recoveredApproach={recoveredText(snapshot, message, "approach")}
                recoveredSolution={recoveredText(snapshot, message, "solution")}
                copied={copied === message.id}
                setCopied={setCopied}
                canPrepareFollowup={canPrepareFollowup}
                canReuseImage={canReuseImage}
                onOpenImage={onOpenImage}
                onRetry={onRetry}
                onCopy={onCopy}
                onOpenLink={onOpenLink}
                {...(onReuseImage ? { onReuseImage } : {})}
                {...(onFollowup ? { onFollowup } : {})}
                {...(onReuse ? { onReuse } : {})}
              />
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
});

// MessageRow is memoized: snapshots and stream updates arrive several times a
// second, and the client keeps unchanged messages as the same objects, so a
// long conversation re-renders only the rows that actually changed.
const MessageRow = memo(function MessageRow({
  message,
  messages,
  images,
  retry,
  previewMessage,
  previewApproach,
  previewSolution,
  recoveredMessage,
  recoveredApproach,
  recoveredSolution,
  copied,
  setCopied,
  canPrepareFollowup,
  canReuseImage,
  onOpenImage,
  onReuseImage,
  onRetry,
  onCopy,
  onOpenLink,
  onFollowup,
  onReuse,
}: {
  message: Message;
  // Only rows that reference another answer need the whole conversation.
  messages: Message[] | undefined;
  images: AttachmentView[];
  retry: boolean;
  previewMessage: string;
  previewApproach: string;
  previewSolution: string;
  recoveredMessage: string;
  recoveredApproach: string;
  recoveredSolution: string;
  copied: boolean;
  setCopied: (id: string) => void;
  canPrepareFollowup: boolean;
  canReuseImage: boolean;
  onOpenImage: (id: string) => void;
  onReuseImage?: (id: string) => void;
  onRetry: (id: string) => void;
  onCopy: (text: string) => Promise<boolean>;
  onOpenLink: (url: string) => void;
  onFollowup?: FollowupHandler;
  onReuse?: (id: string) => void;
}) {
  return (
    <div>
      <article
        className={`message ${message.role}`}
        id={answerElementID(message.id, "")}
        tabIndex={-1}
      >
        <div className="message-meta">
          <span>{message.role === "assistant" ? "Solveo" : "Вы"}</span>
          {message.from_transcript && <span>Из транскрипции</span>}
          {message.role === "assistant" && (
            <IconButton
              label={copied ? "Ответ скопирован" : "Скопировать ответ"}
              onClick={() => {
                void onCopy(message.text).then((ok) => {
                  if (ok) setCopied(message.id);
                });
              }}
            >
              {copied ? <Check size={13} /> : <Copy size={13} />}
            </IconButton>
          )}
        </div>
        {message.reply_to_id && (
          <AnswerReference
            messages={messages ?? []}
            messageID={message.reply_to_id}
            kind={message.reply_to_kind ?? ""}
          />
        )}
        {message.question && (
          <details className="question-provenance">
            <summary>Исходные реплики вопроса · версия {message.question.version}</summary>
            {message.question.previous_question_id && (
              <Button
                onClick={() => {
                  const target = document.getElementById(
                    answerElementID(message.question?.previous_question_id ?? "", ""),
                  );
                  target?.scrollIntoView({ block: "center" });
                  target?.focus();
                }}
              >
                К предыдущему вопросу
              </Button>
            )}
            {message.question.sources.map((source) => (
              <p key={source.id}>
                {source.track === "system" ? "Собеседник" : "Микрофон"}: {source.text}
              </p>
            ))}
          </details>
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
              images.filter((image) => image.id === id),
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
            {retry && (
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
          previews={{ approach: previewApproach, solution: previewSolution }}
          recovered={{ approach: recoveredApproach, solution: recoveredSolution }}
          onCopy={onCopy}
          onOpenLink={onOpenLink}
        />
      ) : (
        message.role === "user" && (
          <>
            <RecoveredPreview text={recoveredMessage} onOpenLink={onOpenLink} />
            <StreamPreview text={previewMessage} onOpenLink={onOpenLink} />
          </>
        )
      )}
    </div>
  );
});

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
