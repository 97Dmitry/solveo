import { z } from "zod";
import type { app, chat, llm, settings } from "../../wailsjs/go/models";
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
export const configSchema = z.object({
  parallel_answers: z.boolean(),
  fast_model: z.string(),
  fast_reasoning: z
    .string()
    .max(64)
    .regex(/^[a-z0-9_-]*$/),
  context: z.string(),
  instructions: z.string(),
  response_language: z.string(),
  code_language: z.string(),
  detail: z.enum(["brief", "normal", "detailed"]),
  model: z.string(),
  reasoning: z
    .string()
    .max(64)
    .regex(/^[a-z0-9_-]*$/),
} satisfies Shape<chat.Config>);
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
export const settingsSchema = z.object({
  ui_scale: z.number().int().min(80).max(150),
  theme: z.enum(["light", "dark"]),
  font_size: z.number().int().min(14).max(30),
  always_on_top: z.boolean(),
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
} satisfies Shape<app.HistorySummary>);
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
const draftSchema = z.object({ text: z.string(), revision } satisfies Shape<app.Draft>);
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
export type Snapshot = z.infer<typeof snapshotSchema>;
export type ChatConfig = z.infer<typeof configSchema>;
export type AppSettings = z.infer<typeof settingsSchema>;
export type Message = z.infer<typeof messageSchema>;
export type SubmitIntent = z.infer<typeof intentSchema>;
export type StreamSnapshot = z.infer<typeof streamSchema>;
export type StreamBlock = z.infer<typeof streamBlockSchema>;
export type Account = z.infer<typeof accountSchema>;
export function parseSnapshot(input: unknown): Snapshot {
  return snapshotSchema.parse(input);
}
export function acceptSnapshot(current: Snapshot | null, incoming: Snapshot): Snapshot {
  return current === null || incoming.revision > current.revision ? incoming : current;
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
