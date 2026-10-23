package worker

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

func TestContextHasBuildFiles(t *testing.T) {
	for _, name := range []string{"Dockerfile", "requirements.lock", "solveo_stt/__init__.py", "solveo_stt/__main__.py", "solveo_stt/server.py", "solveo_stt/pronunciations.json"} {
		if _, err := fs.Stat(Context, name); err != nil {
			t.Fatalf("%s missing from build context: %v", name, err)
		}
	}
}

func TestContextHasNoTests(t *testing.T) {
	_ = fs.WalkDir(Context, ".", func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(path, "tests") || strings.Contains(path, "__pycache__") {
			t.Fatalf("%s must not be in the image context", path)
		}
		return nil
	})
}

func TestTagIsStableAndContentAddressed(t *testing.T) {
	tag := Tag()
	if tag != Tag() {
		t.Fatal("tag is not stable")
	}
	if !regexp.MustCompile(`^solveo-stt:[0-9a-f]{12}$`).MatchString(tag) {
		t.Fatalf("unexpected tag %q", tag)
	}
}
