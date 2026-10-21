import type { Message } from "../bridge/state";
export function latestCode(text: string): string {
  const blocks = [...text.matchAll(/```[^\n]*\n([\s\S]*?)```/g)];
  return blocks.at(-1)?.[1] ?? "";
}

export function lastAnswerText(messages: Message[]): string {
  for (let index = messages.length - 1; index >= 0; index--) {
    const message = messages[index];
    if (!message) continue;
    const solution = message.answers?.find(
      (answer) => answer.kind === "solution" && answer.status === "complete",
    );
    if (solution) return solution.text;
    if (message.role === "assistant") return message.text;
  }
  return "";
}
