package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiagnosticsContentIsOffForOldFilesAndRoundTrips(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"schema_version":1,"theme":"light","font_size":17,"ui_scale":100}`), 0600); err != nil {
		t.Fatal(err)
	}
	s := Open(dir)
	c, err := s.Load()
	if err != nil || c.Diagnostics.IncludeContent {
		t.Fatalf("old file: %+v %v", c.Diagnostics, err)
	}
	c.Diagnostics.IncludeContent = true
	if err := s.Save(c); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Load(); err != nil || !got.Diagnostics.IncludeContent || got.Theme != "light" {
		t.Fatalf("round trip: %+v %v", got, err)
	}
}
