//go:build windows

package instance

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveHasOnlyOneRunningInstance(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "screen-codex-data")
	release, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if second, err := Acquire(strings.ToUpper(dir)); !errors.Is(err, ErrAlreadyRunning) {
		if second != nil {
			second()
		}
		t.Fatalf("second instance accepted the same archive: %v", err)
	}
	other, err := Acquire(filepath.Join(t.TempDir(), "screen-codex-data"))
	if err != nil {
		t.Fatal("independent archive blocked:", err)
	}
	other()
	release()
	reopened, err := Acquire(dir)
	if err != nil {
		t.Fatal("closed instance still holds archive:", err)
	}
	reopened()
}
