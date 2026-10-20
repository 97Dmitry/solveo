import type { app } from "../../wailsjs/go/models";
import {
  acceptSnapshot,
  errorText,
  intentSchema,
  parseSnapshot,
  type Snapshot,
  type Wire,
} from "./state";
export type SubmitInput = Wire<app.SubmitInput>;
export interface Transport {
  snapshot(): Promise<unknown>;
  ready(): Promise<void>;
  on(name: string, callback: (data: unknown) => void): () => void;
  setDraft(input: Wire<app.DraftInput>): Promise<void>;
  submit(input: SubmitInput): Promise<string>;
}
type Editor = { chatID: string; text: string; revision: number; composing: boolean };
export type ClientState =
  | { status: "loading"; error: string }
  | { status: "error"; error: string }
  | { status: "ready"; snapshot: Snapshot; editor: Editor; working: boolean; error: string };
export class SolveoClient {
  private state: ClientState = { status: "loading", error: "" };
  private listeners = new Set<() => void>();
  private generation = 0;
  private connected = false;
  private draftTimer: ReturnType<typeof setTimeout> | undefined;
  private writes: Promise<void> = Promise.resolve();
  private intents = new Set<string>();
  constructor(private readonly transport: Transport) {}
  getState = (): ClientState => this.state;
  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
  private update(next: ClientState) {
    this.state = next;
    for (const fn of this.listeners) fn();
  }
  private fail(error: unknown) {
    if (this.state.status === "ready") this.update({ ...this.state, error: errorText(error) });
    else this.update({ status: "error", error: errorText(error) });
  }
  clearError() {
    if (this.state.status === "ready") this.update({ ...this.state, error: "" });
  }
  private receive = (data: unknown) => {
    try {
      const incoming = parseSnapshot(data);
      const old = this.state;
      const current = old.status === "ready" ? old.snapshot : null;
      const snapshot = acceptSnapshot(current, incoming);
      if (current === snapshot) return;
      const editor =
        old.status === "ready" &&
        old.editor.chatID === snapshot.active_chat_id &&
        old.editor.revision >= snapshot.draft.revision
          ? old.editor
          : {
              chatID: snapshot.active_chat_id,
              text: snapshot.draft.text,
              revision: snapshot.draft.revision,
              composing: false,
            };
      this.update({
        status: "ready",
        snapshot,
        editor,
        working: old.status === "ready" && old.working,
        error: old.error,
      });
    } catch (error) {
      this.update({ status: "error", error: `Ошибка обмена с приложением: ${errorText(error)}` });
    }
  };
  connect = (): (() => void) => {
    const generation = ++this.generation;
    this.connected = false;
    const offState = this.transport.on("solveo:state", (data) => {
      if (generation === this.generation) this.receive(data);
    });
    const offSubmit = this.transport.on("solveo:submit", (data) => {
      if (generation === this.generation) this.nativeSubmit(data);
    });
    void this.transport
      .snapshot()
      .then((data) => {
        if (generation !== this.generation) return;
        this.receive(data);
        this.connected = this.state.status === "ready";
        return this.transport.ready();
      })
      .catch((error) => {
        if (generation === this.generation) {
          this.fail(error);
          void this.transport.ready().catch(() => {});
        }
      });
    return () => {
      if (generation === this.generation) {
        this.generation++;
        this.connected = false;
      }
      offState();
      offSubmit();
      if (this.draftTimer !== undefined) clearTimeout(this.draftTimer);
    };
  };
  edit(text: string) {
    if (this.state.status !== "ready" || this.state.working) return;
    const editor = { ...this.state.editor, text, revision: this.state.editor.revision + 1 };
    this.update({ ...this.state, editor });
    if (this.draftTimer !== undefined) clearTimeout(this.draftTimer);
    this.draftTimer = setTimeout(() => {
      void this.flushDraft().catch((error) => this.fail(error));
    }, 120);
  }
  setComposing(value: boolean) {
    if (this.state.status === "ready")
      this.update({ ...this.state, editor: { ...this.state.editor, composing: value } });
  }
  async flushDraft() {
    if (this.draftTimer !== undefined) {
      clearTimeout(this.draftTimer);
      this.draftTimer = undefined;
    }
    if (this.state.status !== "ready") return;
    const e = this.state.editor;
    const input = { chat_id: e.chatID, text: e.text, revision: e.revision };
    const write = this.writes.catch(() => {}).then(() => this.transport.setDraft(input));
    this.writes = write;
    await write;
  }
  private nativeSubmit(data: unknown) {
    try {
      const intent = intentSchema.parse(data);
      if (
        !this.connected ||
        this.state.status !== "ready" ||
        this.state.editor.composing ||
        this.state.working ||
        intent.chat_id !== this.state.snapshot.active_chat_id ||
        Date.now() >= intent.expires_at ||
        this.intents.has(intent.id)
      )
        return;
      this.intents.add(intent.id);
      void this.submit(intent.id);
    } catch (error) {
      this.fail(error);
    }
  }
  async submit(intentID = "", retryMessageID = "") {
    if (
      this.state.status !== "ready" ||
      this.state.working ||
      this.state.editor.composing ||
      !this.connected
    )
      return;
    const { snapshot, editor } = this.state;
    if (!snapshot.controls.can_submit) return;
    const input: SubmitInput = {
      request_id: crypto.randomUUID(),
      intent_id: intentID,
      chat_id: snapshot.active_chat_id,
      text: editor.text,
      draft_revision: editor.revision,
      attachment_ids: snapshot.queue.map((a) => a.id),
      retry_message_id: retryMessageID,
    };
    await this.run(() => this.transport.submit(input));
  }
  async run(action: () => Promise<unknown>, snapshotResponse = false, flush = false) {
    if (this.state.status !== "ready" || this.state.working) return false;
    this.update({ ...this.state, working: true, error: "" });
    try {
      if (flush) await this.flushDraft();
      const result = await action();
      if (snapshotResponse) this.receive(result);
      return true;
    } catch (error) {
      this.fail(error);
      return false;
    } finally {
      if (this.state.status === "ready") this.update({ ...this.state, working: false });
    }
  }
}
