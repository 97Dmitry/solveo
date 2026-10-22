package secrets

import "testing"

func TestMemoryStoreRoundTrip(t *testing.T) {
	var s Store = NewMemory()
	if _, ok, err := s.Get("Solveo/stt-grok"); ok || err != nil {
		t.Fatalf("empty store: %v %v", ok, err)
	}
	if err := s.Set("Solveo/stt-grok", "xai-secret"); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := s.Get("Solveo/stt-grok"); !ok || err != nil || v != "xai-secret" {
		t.Fatalf("got %q %v %v", v, ok, err)
	}
	if err := s.Delete("Solveo/stt-grok"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("Solveo/stt-grok"); err != nil {
		t.Fatalf("deleting an absent secret must succeed: %v", err)
	}
	if _, ok, _ := s.Get("Solveo/stt-grok"); ok {
		t.Fatal("deleted secret still present")
	}
}
