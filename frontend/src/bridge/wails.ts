import * as api from "../../wailsjs/go/main/App";
import { app, settings } from "../../wailsjs/go/models";
import { EventsOn, BrowserOpenURL, ClipboardSetText } from "../../wailsjs/runtime/runtime";
import { SolveoClient } from "./client";
import { imageBase64 } from "./attachments";
import {
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
