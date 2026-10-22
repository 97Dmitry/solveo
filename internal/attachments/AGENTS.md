# Incoming images

- All new sources share PNG/JPEG content validation: 32 MiB input/output, 40 million pixels, maximum side 32768. DecodeConfig precedes full decoding. PNG chunk CRC/end and APNG rejection cover data the image decoder can ignore.
- Queue/archive output is PNG for the native viewer. Preserve validated PNG bytes; normalize JPEG without scaling. Encoding uses a bounded writer.
- ReadFile accepts bounded regular files only. Archive callers must first resolve the source through Archive.AttachmentPath; no renderer-supplied paths.
- Run `go test ./internal/attachments`; new limits do not migrate or delete existing files.

- JPEG normalization applies all eight EXIF Orientation values through a read-only image view, without allocating another bitmap. Parse only bounded APP1/IFD0 SHORT orientation metadata; malformed optional metadata falls back to orientation 1. Test both TIFF byte orders, rotations and mirrors.
