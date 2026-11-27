package audiowindow

import (
	"io/fs"
	"strings"
)

type detachedAssets struct {
	fs.FS
	entry string
}

func (a detachedAssets) Open(name string) (fs.File, error) {
	if name == "index.html" {
		name = a.entry
	}
	return a.FS.Open(name)
}

// Assets maps only the main entry to the detached UI; shared chunks remain unchanged.
func Assets(source fs.FS) fs.FS { return detachedAssets{FS: source, entry: "audio.html"} }

func AnswersAssets(source fs.FS) fs.FS {
	return detachedAssets{FS: source, entry: "answers.html"}
}

// ChildEnvironment isolates the bundled renderer from the parent's development servers.
// Even under wails dev the child uses its embedded production assets (no separate HMR server).
func ChildEnvironment(parent []string) []string {
	out := make([]string, 0, len(parent))
	for _, entry := range parent {
		key, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "devserver") || strings.EqualFold(key, "frontenddevserverurl") || strings.EqualFold(key, "assetdir") {
			continue
		}
		out = append(out, entry)
	}
	return out
}
