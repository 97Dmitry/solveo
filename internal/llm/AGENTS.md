# Provider boundary

- Keep provider-owned session state opaque to the application and archive. Concrete adapters live below this package.
- Model discovery is an optional `ModelCatalog` capability and returns normalized model/effort metadata. It must not start a conversation or model turn.
- Effort IDs are provider-advertised tokens, not a hard-coded shared enum. Preserve advertised order and defaults. Never infer support from another model's options.
- Generate may run concurrently for two independent Requests within one application operation. Adapters must isolate mutable subprocess configuration. Purpose is empty for ordinary chat, approach/solution for parallel outputs; ContextText supplies the standalone approach with bounded history. Only the main request receives and updates chat continuation state.
