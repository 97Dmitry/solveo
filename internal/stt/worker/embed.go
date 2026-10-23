// Package worker embeds the solveo-stt image build context (Dockerfile, lock, Python package) into Solveo.exe.
package worker

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"sort"
)

//go:embed Dockerfile requirements.lock solveo_stt/*.py solveo_stt/*.json
var files embed.FS

// Context is the docker build context; tests and caches are never part of it.
var Context fs.FS = files

// Tag names the image after the content of Context, so a changed worker is rebuilt.
func Tag() string {
	var paths []string
	_ = fs.WalkDir(Context, ".", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			paths = append(paths, path)
		}
		return err
	})
	sort.Strings(paths)
	h := sha256.New()
	for _, path := range paths {
		data, err := fs.ReadFile(Context, path)
		if err != nil {
			panic(err) // embedded files are always readable
		}
		h.Write([]byte(path))
		h.Write([]byte{0})
		h.Write(data)
		h.Write([]byte{0})
	}
	return "solveo-stt:" + hex.EncodeToString(h.Sum(nil))[:12]
}
