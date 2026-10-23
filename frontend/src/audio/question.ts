import type { QuestionCommand, QuestionState } from "../bridge/state";

// Every question command carries the displayed chat/card identity and revision; Go rejects stale
// ones.
export function questionCommand(
  view: QuestionState,
  action: string,
  extra: Partial<QuestionCommand> = {},
): QuestionCommand {
  return {
    action,
    chat_id: view.chat_id,
    id: view.id,
    revision: view.revision,
    version: view.version,
    text: "",
    model: "",
    reasoning: "",
    confirm: false,
    ...extra,
  };
}
