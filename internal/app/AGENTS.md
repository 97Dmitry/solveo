# Application controller

- Keep operation transitions and shared documents under the controller mutex; release it before I/O or provider work. Deep-copy documents transferred to workers.
- Model discovery is independent of chat operation ownership. Register discovery in the worker lifecycle under the same mutex used by Close, and use the controller context so shutdown cancels it. Discovery must not change active chat, drafts, queue or configuration.
- Preserve the persistence/recovery and live-draft contracts in the parent guide. Run the controller tests under the race detector after changing concurrency or lifecycle behavior.
