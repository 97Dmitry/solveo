// Text search inside the rendered conversation. Matches are DOM Ranges over
// text nodes, highlighted with the CSS Custom Highlight API, so rendered
// Markdown is never rewritten. A match spanning formatting (for example
// across bold text) is not found.

const skip = "button, .message-meta, [data-search-skip]";

export function findRanges(root: Node, query: string): Range[] {
  const needle = query.toLowerCase();
  if (needle === "") return [];
  const doc = root.ownerDocument ?? document;
  const walker = doc.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode: (node) =>
      node.parentElement?.closest(skip) ? NodeFilter.FILTER_REJECT : NodeFilter.FILTER_ACCEPT,
  });
  const ranges: Range[] = [];
  for (let node = walker.nextNode(); node !== null; node = walker.nextNode()) {
    const text = node.textContent ?? "";
    // Lowercasing may change the length of a few characters; then match exactly.
    let haystack = text.toLowerCase();
    let pattern = needle;
    if (haystack.length !== text.length) {
      haystack = text;
      pattern = query;
    }
    for (
      let at = haystack.indexOf(pattern);
      at >= 0;
      at = haystack.indexOf(pattern, at + pattern.length)
    ) {
      const range = doc.createRange();
      range.setStart(node, at);
      range.setEnd(node, at + pattern.length);
      ranges.push(range);
    }
  }
  return ranges;
}

const allName = "chat-search";
const currentName = "chat-search-current";

function registry(): HighlightRegistry | undefined {
  return typeof CSS !== "undefined" && "highlights" in CSS && typeof Highlight === "function"
    ? CSS.highlights
    : undefined;
}
export function paintRanges(ranges: Range[], current: number) {
  const highlights = registry();
  if (!highlights) return;
  highlights.set(allName, new Highlight(...ranges));
  const active = ranges[current];
  if (active) highlights.set(currentName, new Highlight(active));
  else highlights.delete(currentName);
}
export function clearRanges() {
  const highlights = registry();
  highlights?.delete(allName);
  highlights?.delete(currentName);
}
