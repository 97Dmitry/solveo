package history

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/97Dmitry/solveo/internal/chat"
)

func TestInterruptedTurnSurvivesRestart(t *testing.T) {
	s, _ := Open(t.TempDir())
	c := chat.New()
	c.Messages = []chat.Message{{ID: chat.ID(), Role: "user", Text: "question", Status: "requesting", Attempts: []chat.Attempt{{ID: chat.ID(), Status: "requesting"}}}}
	if _, err := s.Save(SaveInput{Chat: c}); err != nil {
		t.Fatal(err)
	}
	got, e := s.Load(c.ID)
	if e != nil || got.Messages[0].Status != "interrupted" || got.Messages[0].Attempts[0].Status != "interrupted" {
		t.Fatalf("%+v %v", got, e)
	}
}
func TestOversizedJSONPreservesPreviousArchive(t *testing.T) {
	s, _ := Open(t.TempDir())
	c := chat.New()
	if _, err := s.Save(SaveInput{Chat: c}); err != nil {
		t.Fatal(err)
	}
	c.Config.Context = strings.Repeat("x", maxChatBytes)
	if _, e := s.Save(SaveInput{Chat: c}); e == nil {
		t.Fatal("oversized save accepted")
	}
	got, e := s.Load(c.ID)
	if e != nil || got.Config.Context != "" {
		t.Fatal("old archive lost")
	}
}
func TestCorruptArchiveDoesNotHideHealthyChat(t *testing.T) {
	s, _ := Open(t.TempDir())
	c := chat.New()
	if _, err := s.Save(SaveInput{Chat: c}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "chats", "broken.json"), []byte("no json"), 0600); err != nil {
		t.Fatal(err)
	}
	list, e := s.List()
	if e != nil || len(list) != 1 {
		t.Fatalf("%v %v", list, e)
	}
}
func TestMalformedLegacyIsSkipped(t *testing.T) {
	s, _ := Open(t.TempDir())
	c := chat.New()
	if _, err := s.Save(SaveInput{Chat: c}); err != nil {
		t.Fatal(err)
	}
	id := "12345678-1234-1234-1234-123456789abc"
	data := `{"version":1,"thread_id":"` + id + `","messages":[{"Role":"invalid","Text":"bad"}]}`
	if err := os.WriteFile(filepath.Join(s.Dir, "chats", id+".json"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	list, e := s.List()
	if e != nil || len(list) != 1 {
		t.Fatalf("malformed legacy entry hides healthy history: %+v %v", list, e)
	}
}
