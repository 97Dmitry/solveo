export function latestCode(text: string): string {
  const blocks = [...text.matchAll(/```[^\n]*\n([\s\S]*?)```/g)];
  return blocks.at(-1)?.[1] ?? "";
}
