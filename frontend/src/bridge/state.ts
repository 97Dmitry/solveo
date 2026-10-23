import { z } from "zod";
import type { app, chat, history, llm, settings, stt } from "../../wailsjs/go/models";
// Generated classes contain conversion methods. UI state is validated plain data.
export type Wire<T> = T extends readonly (infer U)[]
  ? Wire<U>[]
  : T extends object
    ? {
        [K in keyof T as K extends "convertValues" ? never : K]: undefined extends T[K]
          ? Wire<T[K]> | undefined
          : Wire<T[K]>;
      }
    : T;
type Shape<T> = { [K in keyof Wire<T>]: z.ZodType<Wire<T>[K]> };
const revision = z.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER);
const configText = z
  .string()
  .refine(
    (text) =>
      !text.includes("\0") &&
      !/[\uD800-\uDFFF]/u.test(text) &&
      new TextEncoder().encode(text).length <= 1048576,
    "Не более 1 МиБ текста без NUL и некорректных символов Unicode",
  );
export const configSchema = z.object({
  position: configText,
  stack: configText,
  interview_format: configText,
  experience: configText,
  parallel_answers: z.boolean(),
  fast_model: configText,
  fast_reasoning: z
    .string()
    .max(64)
    .regex(/^[a-z0-9_-]*$/),
  context: configText,
  instructions: configText,
  response_language: configText,
  code_language: configText,
  detail: z.enum(["brief", "normal", "detailed"]),
  model: configText,
  reasoning: z
    .string()
    .max(64)
    .regex(/^[a-z0-9_-]*$/),
} satisfies Shape<chat.Config>);
export const profileNameSchema = z
  .string()
  .trim()
  .min(1)
  .refine(
    (text) =>
      Array.from(text).length <= 160 && !text.includes("\0") && !/[\uD800-\uDFFF]/u.test(text),
    "Название: от 1 до 160 символов без NUL",
  );
export const profileSchema = z.object({
  id: z.string().regex(/^[a-f0-9]{32}$/),
  name: profileNameSchema,
  config: configSchema,
  revision: revision.min(1),
  created_at: z.iso.datetime(),
  updated_at: z.iso.datetime(),
} satisfies Shape<app.ProfileView>);
export const profileInputSchema = z.object({
  id: z.union([z.literal(""), z.string().regex(/^[a-f0-9]{32}$/)]),
  name: profileNameSchema,
  config: configSchema,
  expected_revision: revision,
} satisfies Shape<app.ProfileInput>);
export type Profile = z.infer<typeof profileSchema>;
export type ProfileInput = z.infer<typeof profileInputSchema>;
const effortSchema = z.object({
  id: z
    .string()
    .min(1)
    .max(64)
    .regex(/^[a-z0-9_-]+$/),
  description: z.string(),
} satisfies Shape<llm.ReasoningEffort>);
export const modelSchema = z.object({
  id: z.string().min(1).max(256),
  label: z.string().min(1),
  description: z.string(),
  is_default: z.boolean(),
  default_reasoning: z.string(),
  reasoning_efforts: z.array(effortSchema),
} satisfies Shape<llm.Model>);
export const modelsSchema = z.array(modelSchema).min(1).max(10000);
export type Model = z.infer<typeof modelSchema>;
const attemptSchema = z.object({
  id: z.string(),
  status: z.enum(["requesting", "complete", "failed", "canceled", "unknown", "interrupted"]),
  config_revision: revision,
  error: z.string(),
} satisfies Shape<chat.Attempt>);
const answerSchema = z.object({
  kind: z.enum(["approach", "solution"]),
  status: z.enum(["requesting", "complete", "failed", "canceled", "unknown", "interrupted"]),
  text: z.string(),
  error: z.string(),
  attempt_id: z.string(),
  model: z.string(),
  reasoning: z.string(),
  elapsed_ms: revision,
} satisfies Shape<chat.Answer>);
export const messageSchema = z.object({
  reply_to_id: z.string().optional(),
  reply_to_kind: z.enum(["", "approach", "solution"]).optional(),
  answers: z.array(answerSchema).max(2).optional(),
  request_config: configSchema.optional(),
  id: z.string(),
  role: z.enum(["user", "assistant"]),
  text: z.string(),
  status: z.enum(["requesting", "complete", "failed", "canceled", "unknown", "interrupted"]),
  attachment_ids: z.array(z.string()),
  config_revision: revision,
  attempts: z.array(attemptSchema),
} satisfies Shape<chat.Message>);
const attachmentSchema = z.object({
  id: z.string(),
  label: z.string(),
  url: z.string().regex(/^\/attachments\/[a-zA-Z0-9_-]+\/[a-zA-Z0-9_-]+$/),
} satisfies Shape<app.AttachmentView>);
export const globalInstructionsSchema = z
  .string()
  .refine(
    (text) => Array.from(text).length <= 16384 && !/[\uD800-\uDFFF]/u.test(text),
    "Не более 16384 символов Unicode",
  );
// Go validates and canonicalizes shortcuts; the renderer checks only the shape.
export const hotkeyActions = [
  "quick-region",
  "toggle",
  "queue-region",
  "submit",
  "quick-full",
] as const;
export type HotkeyAction = (typeof hotkeyActions)[number];
const hotkeyText = z.string().regex(/^$|^((Ctrl|Alt|Shift|Win)\+){1,4}[A-Za-z0-9]+$/);
export const hotkeysSchema = z.object({
  "quick-region": hotkeyText,
  toggle: hotkeyText,
  "queue-region": hotkeyText,
  submit: hotkeyText,
  "quick-full": hotkeyText,
});
// Go normalizes and limits the dictionary; the renderer checks only the shape.
export const audioSettingsSchema = z.object({
  provider: z.string().max(64),
  context_phrases: z.array(z.string().min(1).max(512)).max(256),
} satisfies Shape<settings.Audio>);
export const settingsSchema = z.object({
  global_instructions: globalInstructionsSchema,
  ui_scale: z.number().int().min(80).max(150),
  theme: z.enum(["light", "dark"]),
  font_size: z.number().int().min(14).max(30),
  always_on_top: z.boolean(),
  hotkeys: hotkeysSchema,
  audio: audioSettingsSchema,
} satisfies Shape<settings.Config>);
const capabilities = z.object({
  images: z.boolean(),
  reasoning: z.boolean(),
} satisfies Shape<llm.Capabilities>);
const providerSchema = z.object({
  id: z.string(),
  label: z.string(),
  capabilities,
} satisfies Shape<llm.Descriptor>);
const summarySchema = z.object({
  id: z.string(),
  title: z.string(),
  provider_id: z.string(),
  updated_at: z.string(),
  prepared: z.boolean(),
  pinned: z.boolean(),
} satisfies Shape<app.HistorySummary>);
export const searchHitsSchema = z
  .array(
    z.object({
      chat_id: z.string(),
      title: z.string(),
      snippet: z.string(),
      matches: z.number().int().positive(),
      pinned: z.boolean(),
      updated_at: z.string(),
    } satisfies Shape<app.SearchHit>),
  )
  .max(50);
export const deleteResultSchema = z.object({
  deleted: z.array(z.string()),
  failed: z.array(
    z.object({
      id: z.string(),
      title: z.string(),
      error: z.string(),
    } satisfies Shape<app.DeleteFailure>),
  ),
} satisfies Shape<app.DeleteResult>);
export const exportResultSchema = z.object({
  name: z.string().min(1),
  path: z.string().min(1),
  missing: z.number().int().nonnegative(),
} satisfies Shape<history.ExportResult>);
const controlsSchema = z.object({
  can_submit: z.boolean(),
  can_capture: z.boolean(),
  can_edit_config: z.boolean(),
  can_cancel: z.boolean(),
  can_switch: z.boolean(),
  can_retry: z.boolean(),
  can_discard: z.boolean(),
} satisfies Shape<app.Controls>);
const operationSchema = z.object({
  id: z.string(),
  kind: z.string(),
  phase: z.enum(["idle", "selecting", "capturing", "saving", "requesting", "save-pending"]),
  message: z.string(),
} satisfies Shape<app.Operation>);
const draftSchema = z.object({
  text: z.string(),
  revision,
  reply_to_id: z.string().optional(),
  reply_to_kind: z.enum(["", "approach", "solution"]).optional(),
} satisfies Shape<app.Draft>);
const loginSchema = z.object({
  id: z.string(),
  url: z.union([z.literal(""), z.url({ protocol: /^https$/ })]),
  code: z.string().max(64),
  state: z.enum(["", "pending", "done", "failed"]),
  error: z.string(),
} satisfies Shape<app.LoginView>);
const streamBlockSchema = z.object({
  chat_id: z.string(),
  operation_id: z.string(),
  message_id: z.string(),
  attempt_id: z.string(),
  block: z.enum(["message", "approach", "solution"]),
  text: z.string(),
} satisfies Shape<app.StreamBlock>);
export const snapshotSchema = z.object({
  profiles: z.array(profileSchema).max(100),
  profiles_error: z.string(),
  revision,
  active_chat_id: z.string(),
  title: z.string(),
  provider_id: z.string(),
  config: configSchema,
  config_revision: revision,
  messages: z.array(messageSchema),
  queue: z.array(attachmentSchema).max(20),
  images: z.array(attachmentSchema),
  draft: draftSchema,
  operation: operationSchema,
  controls: controlsSchema,
  settings: settingsSchema,
  provider: providerSchema,
  history: z.array(summarySchema),
  error: z.string(),
  uncertain: z.boolean(),
  login: loginSchema,
  work_error: z.string(),
  recovered: z.array(streamBlockSchema).max(8),
  hotkey_errors: z.partialRecord(z.enum(hotkeyActions), z.string()),
} satisfies Shape<app.Snapshot>);
export const streamSchema = z.object({
  revision,
  blocks: z.array(streamBlockSchema).max(2),
} satisfies Shape<app.StreamSnapshot>);
export const accountSchema = z.object({
  signed_in: z.boolean(),
  kind: z.string(),
} satisfies Shape<llm.Account>);
export const intentSchema = z.object({
  id: z.string(),
  chat_id: z.string(),
  draft_revision: revision,
  expires_at: revision,
} satisfies Shape<app.SubmitIntent>);
const audioTrack = z.enum(["system", "mic"]);
// Checks and wording come from the active provider; the renderer shows them as given.
const audioCheckSchema = z.object({
  id: z.string().min(1).max(64),
  label: z.string().max(256),
  state: z.enum(["ok", "missing", "error", "unknown"]),
  message: z.string().max(8192),
} satisfies Shape<stt.Check>);
const audioProviderSchema = z.object({
  id: z.string().max(64),
  label: z.string().max(256),
  local: z.boolean(),
  preparable: z.boolean(),
  needs_key: z.boolean(),
  vendor: z.string().max(128),
  key_label: z.string().max(128),
  start_label: z.string().max(128),
  stop_label: z.string().max(128),
  max_phrases: z.number().int().min(0),
  max_phrase_runes: z.number().int().min(0),
} satisfies Shape<stt.Description>);
const audioProviderOptionSchema = z.object({
  id: z.string().min(1).max(64),
  label: z.string().max(256),
  local: z.boolean(),
} satisfies Shape<app.AudioProviderOption>);
const audioSourceSchema = z.object({
  track: audioTrack,
  device: z.string().max(1024),
  active: z.boolean(),
  error: z.string().max(8192),
} satisfies Shape<app.AudioSourceView>);
const transcriptSegmentSchema = z.object({
  provider_label: z.string().max(1024).optional(),
  id: z.string().min(1).max(256),
  track: audioTrack,
  started_at: z.iso.datetime(),
  text: z.string().max(65536),
  final: z.boolean(),
} satisfies Shape<app.TranscriptSegment>);
export const audioSchema = z.object({
  history_enabled: z.boolean(),
  history_error: z.string().max(8192),
  available: z.boolean(),
  provider: audioProviderSchema,
  providers: z.array(audioProviderOptionSchema).max(8),
  revision,
  checks: z.array(audioCheckSchema).max(16),
  busy: z.enum(["", "checking", "preparing", "starting", "stopping", "opening", "switching"]),
  progress: z.string().max(2048),
  model: z.enum(["stopped", "starting", "ready", "failed"]),
  listening: z.boolean(),
  sources: z.array(audioSourceSchema).max(2),
  segments: z.array(transcriptSegmentSchema).max(300),
  error: z.string().max(8192),
  key_configured: z.boolean(),
} satisfies Shape<app.AudioView>);
export type AudioState = z.infer<typeof audioSchema>;
export type TranscriptSegment = z.infer<typeof transcriptSegmentSchema>;
// Before the first AudioSnapshot: revision -1 accepts any real state.
export const emptyAudio: AudioState = {
  history_enabled: false,
  history_error: "",
  available: false,
  provider: {
    id: "",
    label: "",
    local: true,
    preparable: false,
    needs_key: false,
    vendor: "",
    key_label: "",
    start_label: "",
    stop_label: "",
    max_phrases: 0,
    max_phrase_runes: 0,
  },
  providers: [],
  revision: -1,
  checks: [],
  busy: "",
  progress: "",
  model: "stopped",
  listening: false,
  sources: [],
  segments: [],
  error: "",
  key_configured: false,
};
export type Snapshot = z.infer<typeof snapshotSchema>;
export type ChatConfig = z.infer<typeof configSchema>;
export type AppSettings = z.infer<typeof settingsSchema>;
export type Message = z.infer<typeof messageSchema>;
export type AttachmentView = z.infer<typeof attachmentSchema>;
export type SubmitIntent = z.infer<typeof intentSchema>;
export type StreamSnapshot = z.infer<typeof streamSchema>;
export type StreamBlock = z.infer<typeof streamBlockSchema>;
export type Account = z.infer<typeof accountSchema>;
export type SearchHit = z.infer<typeof searchHitsSchema>[number];
export type DeleteResult = z.infer<typeof deleteResultSchema>;
export type ExportResult = z.infer<typeof exportResultSchema>;
export type Hotkeys = z.infer<typeof hotkeysSchema>;
export function parseSnapshot(input: unknown): Snapshot {
  return snapshotSchema.parse(input);
}
export function acceptSnapshot(current: Snapshot | null, incoming: Snapshot): Snapshot {
  return current === null || incoming.revision > current.revision
    ? shareUnchanged(current, incoming)
    : current;
}
// Serialized forms of already accepted values; kept per object, so every
// unchanged message is serialized once rather than on each snapshot.
const serialized = new WeakMap<object, string>();
function wire(value: object): string {
  let text = serialized.get(value);
  if (text === undefined) {
    text = JSON.stringify(value);
    serialized.set(value, text);
  }
  return text;
}
// shareUnchanged keeps equal messages and images as the previous objects, so
// memoized rows of a long conversation skip rendering on every snapshot.
export function shareUnchanged(current: Snapshot | null, incoming: Snapshot): Snapshot {
  if (current === null) return incoming;
  const old = new Map(current.messages.map((m) => [m.id, m]));
  const messages = incoming.messages.map((m) => {
    const previous = old.get(m.id);
    return previous !== undefined && wire(previous) === wire(m) ? previous : m;
  });
  const images = wire(current.images) === wire(incoming.images) ? current.images : incoming.images;
  return { ...incoming, messages, images };
}
export const emptyStream: StreamSnapshot = { revision: 0, blocks: [] };

// streamText returns unfinished text only for the running request it belongs
// to: the active chat, the current operation and the still-requesting attempt.
// An ordinary answer follows the turn's status; a parallel block follows its
// own, because the solution may fail while the approach is still arriving.
export function streamText(
  snapshot: Snapshot,
  stream: StreamSnapshot,
  message: Message,
  block: StreamBlock["block"],
): string {
  if (snapshot.operation.phase !== "requesting") return "";
  let attemptID: string | undefined;
  if (block === "message") {
    const attempt = message.attempts.at(-1);
    if (message.status === "requesting" && attempt?.status === "requesting") attemptID = attempt.id;
  } else {
    attemptID = message.answers?.find(
      (a) => a.kind === block && a.status === "requesting",
    )?.attempt_id;
  }
  if (attemptID === undefined) return "";
  const found = stream.blocks.find(
    (b) =>
      b.block === block &&
      b.chat_id === snapshot.active_chat_id &&
      b.operation_id === snapshot.operation.id &&
      b.message_id === message.id &&
      b.attempt_id === attemptID,
  );
  return found?.text ?? "";
}
// recoveredText returns text received before a crash for an interrupted block.
// Go publishes only blocks whose attempt is still interrupted; the text is not an
// answer and is never sent to the LLM.
export function recoveredText(
  snapshot: Snapshot,
  message: Message,
  block: StreamBlock["block"],
): string {
  return (
    snapshot.recovered.find(
      (b) =>
        b.block === block && b.chat_id === snapshot.active_chat_id && b.message_id === message.id,
    )?.text ?? ""
  );
}
export function errorText(error: unknown): string {
  return error instanceof Error
    ? error.message
    : typeof error === "string"
      ? error
      : "Не удалось выполнить действие";
}

// Resolve only finished answers, including independent parallel blocks.
export function referencedAnswer(messages: Message[], id: string, kind: string): string {
  const message = messages.find((m) => m.id === id);
  if (kind === "")
    return message?.role === "assistant" && message.status === "complete" ? message.text : "";
  return message?.role === "user"
    ? (message.answers?.find((a) => a.kind === kind && a.status === "complete")?.text ?? "")
    : "";
}
