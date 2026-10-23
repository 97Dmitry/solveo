package secrets

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"
)

func TestCredentialManagerRoundTrip(t *testing.T) {
	var raw [6]byte
	_, _ = rand.Read(raw[:])
	name := "Solveo/test-" + hex.EncodeToString(raw[:])
	s := NewCredentialManager()
	t.Cleanup(func() { _ = s.Delete(name) })
	value := "ключ-" + strings.Repeat("x", 100)
	if err := s.Set(name, value); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Get(name)
	if err != nil || !ok || got != value {
		t.Fatalf("got %q %v %v", got, ok, err)
	}
	if err := s.Delete(name); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Get(name); ok || err != nil {
		t.Fatalf("after delete: %v %v", ok, err)
	}
	if err := s.Delete(name); err != nil {
		t.Fatalf("deleting an absent secret must succeed: %v", err)
	}
}
