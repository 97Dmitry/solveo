import type { app } from "../../wailsjs/go/models";
import {
  acceptSnapshot,
  audioSchema,
  emptyAudio,
  referencedAnswer,
  emptyStream,
  errorText,
  intentSchema,
  parseSnapshot,
  streamSchema,
  type AudioState,
  type Snapshot,
  type StreamSnapshot,
  type Wire,
} from "./state";
export type SubmitInput = Wire<app.SubmitInput>;
export interface Transport {
  snapshot(): Promise<unknown>;
  ready(): Promise<void>;
  on(name: string, callback: (data: unknown) => void): () => void;
  setDraft(input: Wire<app.DraftInput>): Promise<void>;
  submit(input: SubmitInput): Promise<string>;
  streamSnapshot(): Promise<unknown>;
  audioSnapshot(): Promise<unknown>;
  submitTranscript(input: Wire<app.SubmitTranscriptInput>): Promise<string>;
}
type Editor = {
  chatID: string;
  text: string;
  revision: number;
  composing: boolean;
  replyToID: string;
  replyToKind: string;
};
export type ClientState =
  | { status: "loading"; error: string }
  | { status: "error"; error: string }
  | {
      status: "ready";
      snapshot: Snapshot;
      editor: Editor;
      working: boolean;
      submitting: boolean;
      error: string;
      // Unfinished answer text; presentation filters it by the running attempt.
      stream: StreamSnapshot;
      // Speech recognition state; independent of chat snapshots.
      audio: AudioState;
    };
export class SolveoClient {
  private state: ClientState = { status: "loading", error: "" };
  private listeners = new Set<() => void>();
  private generation = 0;
  private connected = false;
  private draftTimer: ReturnType<typeof setTimeout> | undefined;
  private writes: Promise<void> = Promise.resolve();
  private intents = new Set<string>();
  private stream: StreamSnapshot = emptyStream;
  private audio: AudioState = emptyAudio;
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
              replyToID: snapshot.draft.reply_to_id ?? "",
              replyToKind: snapshot.draft.reply_to_kind ?? "",
            };
      this.update({
        status: "ready",
        snapshot,
        editor,
        working: old.status === "ready" && old.working,
        submitting: old.status === "ready" && old.submitting,
        error: old.error,
        stream: this.stream,
        audio: this.audio,
      });
    } catch (error) {
      this.update({ status: "error", error: `Ошибка обмена с приложением: ${errorText(error)}` });
    }
  };
  private receiveStream = (data: unknown) => {
    try {
      const incoming = streamSchema.parse(data);
      if (incoming.revision <= this.stream.revision) return;
      this.stream = incoming;
      if (this.state.status === "ready") this.update({ ...this.state, stream: incoming });
    } catch (error) {
      this.fail(`Ошибка обмена с приложением: ${errorText(error)}`);
    }
  };
  private receiveAudio = (data: unknown) => {
    try {
      const incoming = audioSchema.parse(data);
      if (incoming.revision <= this.audio.revision) return;
      this.audio = incoming;
      if (this.state.status === "ready") this.update({ ...this.state, audio: incoming });
    } catch (error) {
      this.fail(`Ошибка обмена с приложением: ${errorText(error)}`);
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
    const offStream = this.transport.on("solveo:stream", (data) => {
      if (generation === this.generation) this.receiveStream(data);
    });
    const offAudio = this.transport.on("solveo:audio", (data) => {
      if (generation === this.generation) this.receiveAudio(data);
    });
    void this.transport
      .snapshot()
      .then((data) => {
        if (generation !== this.generation) return;
        this.receive(data);
        this.connected = this.state.status === "ready";
        // A reloaded renderer recovers previews of a request already running.
        void this.transport
          .streamSnapshot()
          .then((stream) => {
            if (generation === this.generation) this.receiveStream(stream);
          })
          .catch(() => {});
        // Listening survives renderer reloads; the transcript comes back from Go.
        void Promise.resolve()
          .then(() => this.transport.audioSnapshot())
          .then((audio) => {
            if (generation === this.generation) this.receiveAudio(audio);
          })
          .catch(() => {});
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
      offStream();
      offAudio();
      if (this.draftTimer !== undefined) clearTimeout(this.draftTimer);
    };
  };
  edit(text: string) {
    if (this.state.status !== "ready" || (this.state.working && !this.state.submitting)) return;
    const editor = { ...this.state.editor, text, revision: this.state.editor.revision + 1 };
    this.update({ ...this.state, editor });
    if (this.draftTimer !== undefined) clearTimeout(this.draftTimer);
    this.draftTimer = setTimeout(() => {
      void this.flushDraft().catch((error) => this.fail(error));
    }, 120);
  }
  // insertTranscript appends recognized text to the live editor (a new line after existing text).
  insertTranscript(text: string) {
    if (
      this.state.status !== "ready" ||
      this.state.editor.composing ||
      (this.state.working && !this.state.submitting) ||
      text.trim() === ""
    )
      return false;
    const current = this.state.editor.text;
    this.edit(current === "" ? text : `${current}\n${text}`);
    return true;
  }
  // submitTranscript sends selected final phrases as a question; the draft is not involved.
  async submitTranscript(segmentIDs: string[]) {
    if (this.state.status !== "ready" || segmentIDs.length === 0) return false;
    const input = {
      chat_id: this.state.snapshot.active_chat_id,
      request_id: crypto.randomUUID(),
      segment_ids: segmentIDs,
    };
    return this.run(() => this.transport.submitTranscript(input));
  }
  prepareFollowup(messageID: string, kind: string, text: string) {
    if (this.state.status !== "ready") return false;
    if (!referencedAnswer(this.state.snapshot.messages, messageID, kind).trim()) return false;
    return this.prepareEditor(text, messageID, kind);
  }
  reuseQuestion(messageID: string) {
    if (this.state.status !== "ready") return false;
    const question = this.state.snapshot.messages.find(
      (m) => m.id === messageID && m.role === "user",
    );
    if (!question || !question.text) return false;
    return this.prepareEditor(
      question.text,
      question.reply_to_id ?? "",
      question.reply_to_kind ?? "",
    );
  }
  private prepareEditor(text: string, replyToID: string, replyToKind: string) {
    if (
      this.state.status !== "ready" ||
      this.state.working ||
      this.state.editor.composing ||
      !this.state.snapshot.controls.can_submit ||
      this.state.editor.text !== "" ||
      this.state.editor.replyToID !== ""
    )
      return false;
    this.update({ ...this.state, editor: { ...this.state.editor, replyToID, replyToKind } });
    this.edit(text);
    return true;
  }
  clearReference() {
    if (this.state.status !== "ready" || (this.state.working && !this.state.submitting)) return;
    const text = this.state.editor.text;
    this.update({
      ...this.state,
      editor: { ...this.state.editor, replyToID: "", replyToKind: "" },
    });
    this.edit(text);
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
    const input = {
      chat_id: e.chatID,
      text: e.text,
      revision: e.revision,
      reply_to_id: e.replyToID,
      reply_to_kind: e.replyToKind,
    };
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
      reply_to_id: editor.replyToID,
      reply_to_kind: editor.replyToKind,
    };
    await this.run(() => this.transport.submit(input), false, false, true);
  }
  // result is run for actions whose value the caller needs; undefined means failure.
  async result<T>(action: () => Promise<T>, flush = false): Promise<T | undefined> {
    let value: T | undefined;
    const ok = await this.run(
      async () => {
        value = await action();
      },
      false,
      flush,
    );
    return ok ? value : undefined;
  }
  async run(
    action: () => Promise<unknown>,
    snapshotResponse = false,
    flush = false,
    submitting = false,
  ) {
    if (this.state.status !== "ready" || this.state.working) return false;
    this.update({ ...this.state, working: true, submitting, error: "" });
    try {
      if (flush) await this.flushDraft();
      const result = await action();
      if (snapshotResponse) this.receive(result);
      return true;
    } catch (error) {
      this.fail(error);
      return false;
    } finally {
      if (this.state.status === "ready")
        this.update({ ...this.state, working: false, submitting: false });
    }
  }
}
