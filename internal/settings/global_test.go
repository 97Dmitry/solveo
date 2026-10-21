package settings

import (
	"strings"
	"testing"
)

func TestGlobalInstructionsUnicodeLimitAndRoundTrip(t *testing.T) {
	s := Open(t.TempDir())
	c, _ := s.Load()
	if c.GlobalInstructions != "" {
		t.Fatal("old default")
	}
	c.GlobalInstructions = strings.Repeat("😀", 16384)
	if err := s.Save(c); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil || got != c {
		t.Fatalf("round trip: %v", err)
	}
	c.GlobalInstructions += "a"
	if err := s.Save(c); err == nil {
		t.Fatal("oversized instructions accepted")
	}
	got, _ = s.Load()
	if got.GlobalInstructions != strings.Repeat("😀", 16384) {
		t.Fatal("invalid save replaced settings")
	}
}
