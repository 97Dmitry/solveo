//go:build windows

package codex

import (
	"strings"
	"testing"
)

func TestLocalDiskRejectsNetworkPaths(t *testing.T) {
	if err := localDisk(t.TempDir()); err != nil {
		t.Fatalf("local temp dir rejected: %v", err)
	}
	for _, p := range []string{`\\wsl.localhost\archlinux\home\solveo-data\codex-home`, `\\server\share\solveo-data`} {
		if err := localDisk(p); err == nil || !strings.Contains(err.Error(), "сетевом пути") {
			t.Fatalf("%s: %v", p, err)
		}
	}
}
