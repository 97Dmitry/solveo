# Interview profiles

- Library is a version-1 `profiles.json` beside settings, separate from chats. Each entry contains only name, chat.Config and local identity/revision/UTC timestamps. Never store global instructions, provider state or messages here.
- Controller owns CRUD/revision checks and serialization. Store confines I/O with os.Root, bounds reads/documents to 32 MiB, validates all entries and atomically replaces the whole document. A failed Load disables Save until an explicit successful Load; never replace corrupt/newer data with an empty library.
- At most 100 entries; names 1..160 Unicode code points after trim; IDs are local 32-character lowercase hex, revisions positive safe JS integers. Config validation is shared with chat. Errors must not contain resume text or other preparation content.
- Verify: `go test -race ./internal/profiles ./internal/app`. Use temporary stores; no real provider calls.
