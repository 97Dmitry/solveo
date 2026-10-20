# Chat archive

- Keep archive attachment metadata as validated slash-separated relative paths; use `os.Root` for access and preserve atomic document replacement.
- `ImagePath` returns a native resolved path for providers/viewers. Resolve both the archive directory and the image before checking their relative path: Windows short names and directory aliases can change the spelling of the root. Preserve rejection of links escaping the archive.
- Cover path handling with `go test ./internal/history` and the native Windows CI suite. An archive reached through a directory alias must remain usable; external image links must remain rejected.
