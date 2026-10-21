package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/97Dmitry/solveo/internal/chat"
)

func TestGlobalSnapshotRoundTripAndOldArchive(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	doc := chat.New()
	doc.GlobalInstructions = "снимок 😀"
	saved, err := s.Save(SaveInput{Chat: doc})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(saved.ID)
	if err != nil || got.GlobalInstructions != doc.GlobalInstructions {
		t.Fatal(got, err)
	}
	// Remove the additive field to represent an old v2 archive.
	b, _ := json.Marshal(doc)
	var old map[string]json.RawMessage
	_ = json.Unmarshal(b, &old)
	delete(old, "global_instructions")
	b, _ = json.Marshal(old)
	if err = os.WriteFile(filepath.Join(s.Dir, "chats", doc.ID+".json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	got, err = s.Load(doc.ID)
	if err != nil || got.GlobalInstructions != "" {
		t.Fatal(got, err)
	}
	doc.GlobalInstructions = strings.Repeat("x", 16385)
	if _, err = s.Save(SaveInput{Chat: doc}); err == nil {
		t.Fatal("oversized archive instructions")
	}
}
