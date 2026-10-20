# Provider boundary

- Keep provider-owned session state opaque to the application and archive. Concrete adapters live below this package.
- Model discovery is an optional `ModelCatalog` capability and returns normalized model/effort metadata. It must not start a conversation or model turn.
- Effort IDs are provider-advertised tokens, not a hard-coded shared enum. Preserve advertised order and defaults. Never infer support from another model's options.
