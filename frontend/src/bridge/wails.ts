import * as api from "../../wailsjs/go/main/App";
import * as audioWindowAPI from "../../wailsjs/go/main/AudioWindowApp";
import { audiowindow } from "../../wailsjs/go/models";
import { z } from "zod";
import { app, settings } from "../../wailsjs/go/models";
import { EventsOn, BrowserOpenURL, ClipboardSetText } from "../../wailsjs/runtime/runtime";
import { SolveoClient } from "./client";
import { imageBase64 } from "./attachments";
import {
  audioSchema,
  questionSchema,
  accountSchema,
  configSchema,
  deleteResultSchema,
  exportResultSchema,
  hotkeysSchema,
  searchHitsSchema,
  modelsSchema,
  profileInputSchema,
  type ProfileInput,
  settingsSchema,
  type AppSettings,
  type ChatConfig,
  type Wire,
} from "./state";
export const client = new SolveoClient({
  snapshot: api.RendererReady,
  ready: api.Ready,
  on: (name, callback) => EventsOn(name, (data: unknown) => callback(data)),
  setDraft: api.SetDraft,
  submit: api.Submit,
  streamSnapshot: api.StreamSnapshot,
  audioSnapshot: api.AudioSnapshot,
  submitTranscript: (input) => api.SubmitTranscript(new app.SubmitTranscriptInput(input)),
});
export const actions = {
  showAudioWindow: () => client.run(api.ShowAudioWindow),
  exit: () => client.run(api.Exit),
  newChat: () => client.run(api.NewChat, true, true),
  openChat: (id: string) => client.run(() => api.OpenChat(id), true, true),
  // The live draft is flushed first: deleting the active chat switches to another.
  deleteChats: async (ids: string[]) =>
    client.result(async () => deleteResultSchema.parse(await api.DeleteChats(ids)), true),
  pinChat: (id: string, pinned: boolean) => client.run(() => api.SetChatPinned(id, pinned)),
  exportChat: (id: string) =>
    client.result(async () => exportResultSchema.parse(await api.ExportChat(id))),
  revealExport: (name: string) => client.run(() => api.RevealExport(name)),
  saveConfig: (chatID: string, title: string, config: ChatConfig, revision: number) =>
    client.run(
      () =>
        api.SaveChatConfig(
          new app.ConfigInput({
            chat_id: chatID,
            title,
            config: configSchema.parse(config),
            expected_revision: revision,
          } satisfies Wire<app.ConfigInput>),
        ),
      true,
    ),
  saveProfile: (input: ProfileInput) =>
    client.run(() => api.SaveProfile(new app.ProfileInput(profileInputSchema.parse(input))), true),
  deleteProfile: (id: string, revision: number) =>
    client.run(() => api.DeleteProfile(id, revision), true),
  reloadProfiles: () => client.run(api.ReloadProfiles, true),
  newChatFromProfile: (id: string, revision: number) =>
    client.run(() => api.NewChatFromProfile(id, revision), true, true),
  settings: (input: AppSettings) =>
    client.run(() => api.UpdateSettings(new settings.Config(settingsSchema.parse(input))), true),
  pin: (value: boolean) => client.run(() => api.SetAlwaysOnTop(value), true),
  capture: (mode: "quick-region" | "queue-region" | "quick-full") =>
    client.run(() => api.BeginCapture(mode)),
  cancel: (id: string) => client.run(() => api.Cancel(id)),
  importImage: (chatID: string, file: File) =>
    client.run(async () => api.ImportAttachment(chatID, file.name, await imageBase64(file))),
  imageError: (message: string) => client.run(() => Promise.reject(new Error(message))),
  reuseImage: (chatID: string, id: string) => client.run(() => api.ReuseAttachment(chatID, id)),
  reorder: (chatID: string, ids: string[]) => client.run(() => api.ReorderAttachments(chatID, ids)),
  remove: (chatID: string, id: string) => client.run(() => api.RemoveAttachment(chatID, id)),
  clear: (chatID: string) => client.run(() => api.ClearQueue(chatID)),
  retrySave: () => client.run(api.RetrySave),
  discard: () => client.run(api.DiscardUnsaved, true),
  check: () => client.run(api.CheckConnection),
  image: (chatID: string, id: string) => client.run(() => api.OpenImage(chatID, id)),
  log: () => client.run(api.ShowLog),
  refreshAudio: () => client.run(api.RefreshAudio),
  prepareAudio: () => client.run(api.PrepareAudio),
  startAudioModel: () => client.run(api.StartAudioModel),
  stopAudioModel: () => client.run(api.StopAudioModel),
  startListening: () => client.run(api.StartListening),
  stopListening: () => client.run(api.StopListening),
  clearTranscript: () => client.run(api.ClearTranscript),
  selectAudioProvider: (id: string) => client.run(() => api.SetAudioProvider(id)),
  setSTTKey: (provider: string, key: string) => client.run(() => api.SetSTTKey(provider, key)),
  deleteSTTKey: (provider: string) => client.run(() => api.DeleteSTTKey(provider)),
  startLogin: () => client.run(api.StartLogin, true),
  cancelLogin: () => client.run(api.CancelLogin, true),
  copy: async (text: string) => {
    try {
      return await ClipboardSetText(text);
    } catch {
      return false;
    }
  },
  openLink: (url: string) => {
    if (/^https?:\/\//i.test(url)) BrowserOpenURL(url);
  },
};

export async function loadAccount() {
  return accountSchema.parse(await api.Account());
}

export async function loadModels() {
  return modelsSchema.parse(await api.ListModels());
}

// Search runs outside client.run: Go scans the archive without the operation
// slot, so history stays searchable during a request.
export async function searchHistory(query: string) {
  return searchHitsSchema.parse(await api.SearchHistory(query));
}

// Global hotkeys are paused while a shortcut is being recorded.
export function suspendHotkeys(suspend: boolean) {
  return api.SuspendHotkeys(suspend);
}
export async function defaultHotkeys() {
  return hotkeysSchema.parse(await api.DefaultHotkeys());
}

const audioWindowSchema = z.object({
  question: questionSchema,
  docked: z.boolean(),
  window_error: z.string().max(8192),
  pinned: z.boolean(),
  audio: audioSchema,
  chat_id: z.string().max(256),
  chat_title: z.string().max(4096),
  can_send: z.boolean(),
  disabled: z.boolean(),
  theme: z.string().max(32),
  ui_scale: z.number().min(50).max(300),
  show_version: z.number().int().nonnegative(),
}) satisfies z.ZodType<Wire<audiowindow.View>>;
export type AudioWindowView = z.infer<typeof audioWindowSchema>;
export const audioWindow = {
  state: async () => audioWindowSchema.parse(await audioWindowAPI.State()),
  ready: audioWindowAPI.Ready,
  execute: (command: Wire<audiowindow.Command>) =>
    audioWindowAPI.Execute(new audiowindow.Command(command)),
};
const audioInsertSchema = z.object({
  expires_at: z.number().int().positive(),
  id: z.string().min(1).max(64),
  chat_id: z.string().min(1).max(256),
  text: z.string().max(1048576),
});
export function onAudioInsert(insert: (chatID: string, text: string) => boolean) {
  return EventsOn("solveo:audio-insert", (raw: unknown) => {
    const parsed = audioInsertSchema.safeParse(raw);
    if (!parsed.success) return;
    const { id, chat_id: chatID, text } = parsed.data;
    const ok = Date.now() <= parsed.data.expires_at && insert(chatID, text);
    void api.CompleteAudioInsert(
      id,
      ok ? "" : "Не удалось вставить текст: чат изменился или редактор занят",
    );
  });
}

const audioHistoryDocumentSchema = z.object({
  notice: z.string().max(8192).optional(),
  chat_id: z.string().regex(/^[0-9a-f]{32}$/),
  id: z.string().regex(/^[0-9a-f]{32}$/),
  provider_id: z.string().max(128),
  provider_label: z.string().max(1024),
  started_at: z.union([z.literal(""), z.iso.datetime()]),
  ended_at: z.union([z.literal(""), z.iso.datetime()]),
  dictionary: z.array(z.string().max(256)).max(4096),
  sources: z
    .array(
      z.object({
        stream_id: z.string().max(1024),
        provider_label: z.string().max(1024).optional(),
        track: z.string().max(16),
        device: z.string().max(1024),
      }),
    )
    .max(128),
  segments: z
    .array(
      z.object({
        id: z.string().max(1024),
        provider_label: z.string().max(1024).optional(),
        track: z.enum(["system", "mic"]),
        started_at: z.iso.datetime(),
        text: z.string().max(65536),
        final: z.boolean(),
      }),
    )
    .max(100000),
  errors: z
    .array(
      z.object({ at: z.iso.datetime(), track: z.string().max(16), message: z.string().max(8192) }),
    )
    .max(10000),
  submissions: z
    .array(
      z.object({
        at: z.iso.datetime(),
        chat_id: z.string().max(256),
        operation_id: z.string().max(256),
        segment_ids: z.array(z.string().max(1024)).max(300),
      }),
    )
    .max(10000),
}) satisfies z.ZodType<Wire<app.AudioHistoryView>>;
export type AudioHistoryDocument = z.infer<typeof audioHistoryDocumentSchema>;
export const audioHistory = {
  forChat: async (chatID: string) =>
    audioHistoryDocumentSchema.parse(await audioWindowAPI.HistoryForChat(chatID)),
  deleteChat: audioWindowAPI.DeleteChatHistory,
};
