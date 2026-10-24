# Provider boundary

- Keep provider-owned session state opaque to the application and archive. Concrete adapters live below this package.
- Model discovery is an optional `ModelCatalog` capability and returns normalized model/effort metadata. It must not start a conversation or model turn.
- Effort IDs are provider-advertised tokens, not a hard-coded shared enum. Preserve advertised order and defaults. Never infer support from another model's options.
- Generate may run concurrently for two independent Requests within one application operation. Adapters must isolate mutable subprocess configuration. Purpose is empty for ordinary chat, approach/solution for parallel outputs; ContextText supplies the standalone approach with bounded history. Only the main request receives and updates chat continuation state.

- Request.GlobalInstructions is the immutable chat snapshot; Config is the frozen user-turn preconfiguration. Both main and standalone approach receive it. Instruction synchronization and its persistent state belong entirely to the adapter.

- Optional ChatPreparer owns an empty-chat preparation until Generate consumes it or its context is canceled; cleanup finishes before it returns. ChatPreparation carries only local ChatID and immutable config/global-instruction snapshots, never a question. Request.ChatID is a local correlation key, not provider state. Preparation cannot generate answers or start model turns; provider-native generate=false transport warmup is allowed.

- Optional QuestionAnalyzer is an auxiliary transcription analysis operation with typed bounded snapshots/results. It never receives conversational state, global answer instructions or images, and does not own the main generation slot.
- AnalyzeQuestion's emit receives only content-free progress (timing). Optional QuestionPreparer keeps one empty analysis session for a model/effort until AnalyzeQuestion claims it or ctx ends; it never sends a transcript or starts a model turn.
