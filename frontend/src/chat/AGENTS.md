# Chat presentation

- Consume validated bridge types only. Ordinary assistant messages and nested parallel Answers are both supported; do not infer that every user turn has a following assistant message.
- Parallel approach and solution render separately, in that order, with independent status/error/time and copy actions. Label approach preliminary; never imply algorithm consistency or simulate streaming finished text.
- Streaming previews come only from `streamText`; they are visibly provisional, render Markdown without `onCopy` (no answer or code copy) and are replaced by the completed answer from Go. Unclosed Markdown/code must render while text is still arriving (`stream.test.tsx`).
- `recoveredText` (text received before a crash) renders only for interrupted turns/blocks, labelled incomplete, without copy actions, like a stream preview.
- Retry targets the original user message and repeats only the solution. Only the last failed user turn can expose retry; Go owns eligibility and operation lifecycle.
- Last-code selection must use the latest complete solution or ordinary assistant text, never the preliminary approach. Keep scroll-to-bottom responsive to nested block completion as well as message count; preserve reading position.
- Markdown forbids raw HTML and remote images. Run frontend checks/tests from the parent guide, including parallel.test.tsx.

- FollowupActions prepares the editor for a selected complete ordinary answer or parallel block; commands do not submit automatically. AnswerReference displays a local target and supports scrolling/focus; no references to previews/recovered text. Reuse copies question text/reference only, without attaching historical PNGs or changing old messages. Keep the next draft editable while waiting/streaming/saving; save-pending recovery remains blocking.

- Composer image paste consumes clipboard files only, preserving ordinary text paste; reject multiple files explicitly. File input resets after selection for same-file reuse. Queue numbering and earlier/later controls use the current confirmed order. Historical strips expose a distinct add-to-question action and preserve Message.attachment_ids order. Preview remains usable when mutation buttons are disabled.

- ChatView and MessageRow are memoized for long chats: App passes stable callbacks, the bridge keeps unchanged messages/images as previous objects, and rows receive primitive preview/recovered strings. Do not pass new object/array literals or inline callbacks to rows. Measurement: long-chat.test.tsx (logs timings, no timing asserts).
- Conversation search (ChatSearch/search.ts) uses DOM Ranges and the CSS Custom Highlight API; never rewrite rendered Markdown for highlighting. It skips buttons and message meta, scrolls only on navigation, and does not match across formatting boundaries.

- Accepted Message.question metadata renders source text under collapsed details and can jump to the preceding user question. Keep this link distinct from AnswerReference; history/retry never changes the accepted card snapshot.
