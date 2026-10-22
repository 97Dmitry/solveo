import * as api from "../../wailsjs/go/main/App";
import { app, settings } from "../../wailsjs/go/models";
import { EventsOn, BrowserOpenURL, ClipboardSetText } from "../../wailsjs/runtime/runtime";
import { SolveoClient } from "./client";
import {
  accountSchema,
  configSchema,
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
});
export const actions = {
  exit: () => client.run(api.Exit),
  newChat: () => client.run(api.NewChat, true, true),
  openChat: (id: string) => client.run(() => api.OpenChat(id), true, true),
  deleteChat: (id: string) => client.run(() => api.DeleteChat(id), true),
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
  remove: (chatID: string, id: string) => client.run(() => api.RemoveAttachment(chatID, id)),
  clear: (chatID: string) => client.run(() => api.ClearQueue(chatID)),
  retrySave: () => client.run(api.RetrySave),
  discard: () => client.run(api.DiscardUnsaved, true),
  check: () => client.run(api.CheckConnection),
  image: (chatID: string, id: string) => client.run(() => api.OpenImage(chatID, id)),
  log: () => client.run(api.ShowLog),
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
