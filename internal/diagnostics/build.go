package diagnostics

import (
	"log/slog"
	"runtime"
	"runtime/debug"
)

// BuildAttrs describes the running executable: the VCS revision recorded by
// the Go toolchain (absent in builds outside git) and the Go runtime.
func BuildAttrs() []slog.Attr {
	attrs := []slog.Attr{slog.String("go", runtime.Version()), slog.String("arch", runtime.GOOS+"/"+runtime.GOARCH)}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return attrs
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision", "vcs.time", "vcs.modified":
			attrs = append(attrs, slog.String(s.Key, s.Value))
		}
	}
	return attrs
}
