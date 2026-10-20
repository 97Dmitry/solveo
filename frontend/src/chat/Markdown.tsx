import { memo, useState, type ReactNode } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Check, Copy } from "lucide-react";
import { IconButton } from "../ui/Button";
function plainText(value: ReactNode): string {
  if (typeof value === "string" || typeof value === "number") return String(value);
  if (Array.isArray(value)) return value.map((part: ReactNode) => plainText(part)).join("");
  return "";
}
function Code({
  children,
  className,
  onCopy,
}: {
  children: ReactNode;
  className: string | undefined;
  onCopy: (text: string) => Promise<boolean>;
}) {
  const [copied, setCopied] = useState(false);
  const text = plainText(children);
  const language = className?.replace(/^language-/, "") ?? "Код";
  return (
    <div className="code-block">
      <div className="code-toolbar">
        <span>{language}</span>
        <IconButton
          label={copied ? "Код скопирован" : "Скопировать код"}
          onClick={() => {
            void onCopy(text).then((ok) => setCopied(ok));
          }}
        >
          {copied ? <Check size={14} /> : <Copy size={14} />}
        </IconButton>
      </div>
      <pre>
        <code className={className}>{children}</code>
      </pre>
    </div>
  );
}
// Snapshots arrive on every draft or operation change. Memoizing by text and stable
// callbacks keeps long chats from re-parsing every answer on each update.
export const Markdown = memo(function Markdown({
  text,
  onCopy,
  onOpenLink,
}: {
  text: string;
  onCopy: (text: string) => Promise<boolean>;
  onOpenLink: (url: string) => void;
}) {
  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm]}
      skipHtml
      disallowedElements={["img", "iframe", "object", "embed", "form", "input"]}
      urlTransform={(url) => (/^https?:\/\//i.test(url) ? url : "")}
      components={{
        a: ({ href, children }) =>
          href ? (
            <a
              href={href}
              onClick={(e) => {
                e.preventDefault();
                onOpenLink(href);
              }}
              rel="noreferrer"
            >
              {children}
            </a>
          ) : (
            <span>{children}</span>
          ),
        pre: ({ children }) => <>{children}</>,
        code: ({ className, children }) =>
          className || plainText(children).includes("\n") ? (
            <Code className={className} onCopy={onCopy}>
              {children}
            </Code>
          ) : (
            <code>{children}</code>
          ),
      }}
    >
      {text}
    </ReactMarkdown>
  );
});
