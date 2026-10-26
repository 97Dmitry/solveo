// Uncaught renderer errors go to the Go session log. Reports are bounded here
// and validated again in Go; reporting never throws or shows UI.
export type RendererErrorKind = "error" | "rejection" | "render";
export type RendererErrorReport = { kind: RendererErrorKind; message: string; stack: string };
export type ReportSink = (report: RendererErrorReport) => Promise<unknown>;

const maxMessage = 2048;
const maxStack = 4096;
const maxComponentStack = 2048;
const burst = 10;
const windowMs = 10_000;

function clip(text: string, limit: number) {
  return text.length > limit ? `${text.slice(0, limit)}…` : text;
}

function describe(value: unknown): { message: string; stack: string } {
  if (value instanceof Error)
    return { message: `${value.name}: ${value.message}`, stack: value.stack ?? "" };
  if (typeof value === "string") return { message: value, stack: "" };
  try {
    return { message: JSON.stringify(value) ?? String(value), stack: "" };
  } catch {
    return { message: Object.prototype.toString.call(value), stack: "" };
  }
}

export function createReporter(sink: ReportSink, now: () => number = Date.now) {
  let start = 0;
  let count = 0;
  return (kind: RendererErrorKind, value: unknown, extraStack = "") => {
    const at = now();
    if (at - start >= windowMs) {
      start = at;
      count = 0;
    }
    if (count >= burst) return;
    count++;
    const { message, stack } = describe(value);
    const report = {
      kind,
      message: clip(message, maxMessage),
      stack: [clip(stack, maxStack), clip(extraStack, maxComponentStack)]
        .filter(Boolean)
        .join("\n"),
    };
    // A failed report must not cause another report.
    sink(report).catch(() => undefined);
  };
}
export type Reporter = ReturnType<typeof createReporter>;

// installGlobalHandlers reports errors outside React rendering; it returns cleanup.
export function installGlobalHandlers(report: Reporter, target: EventTarget = window) {
  const onError = (event: Event) => {
    if (event instanceof ErrorEvent) report("error", event.error ?? event.message);
  };
  const onRejection = (event: Event) => {
    report("rejection", "reason" in event ? event.reason : undefined);
  };
  target.addEventListener("error", onError);
  target.addEventListener("unhandledrejection", onRejection);
  return () => {
    target.removeEventListener("error", onError);
    target.removeEventListener("unhandledrejection", onRejection);
  };
}
