package history

import (
	"encoding/json"
	"github.com/97Dmitry/solveo/internal/chat"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPreparedChatWithoutSessionSurvivesRestart(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := chat.New()
	c.Config.Context = "Go interview"
	if _, err = s.Save(SaveInput{Chat: c}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.Context != c.Config.Context || len(got.Messages) != 0 || len(got.ProviderState) != 0 {
		t.Fatalf("bad prepared chat: %+v", got)
	}
}
func TestLegacyImagesRemainChatLevel(t *testing.T) {
	s, _ := Open(t.TempDir())
	id := "12345678-1234-1234-1234-123456789abc"
	data := `{"version":1,"thread_id":"` + id + `","messages":[{"Role":"user","Text":"question"}],"images":["images/` + id + `/shot.png"]}`
	os.WriteFile(filepath.Join(s.Dir, "chats", id+".json"), []byte(data), 0600)
	c, e := s.Load(id)
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.Load(id)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(c, again) {
		t.Fatal("legacy IDs unstable")
	}
	if len(c.Messages[0].AttachmentIDs) != 0 || len(c.Attachments) != 1 || c.Messages[0].Text != "question" {
		t.Fatal(c)
	}
	var state map[string]interface{}
	json.Unmarshal(c.ProviderState, &state)
	if state["session_id"] != id {
		t.Fatal(state)
	}
}
func TestFailedCopyPreservesOldDocument(t *testing.T) {
	s, _ := Open(t.TempDir())
	c := chat.New()
	s.Save(SaveInput{Chat: c})
	before, _ := os.ReadFile(filepath.Join(s.Dir, "chats", c.ID+".json"))
	p := filepath.Join(t.TempDir(), "a.png")
	os.WriteFile(p, []byte("png"), 0600)
	c.Attachments = []chat.Attachment{{ID: chat.ID()}, {ID: chat.ID()}}
	_, e := s.Save(SaveInput{Chat: c, Sources: []Source{{c.Attachments[0].ID, p}, {c.Attachments[1].ID, p + "missing"}}})
	if e == nil {
		t.Fatal("expected failure")
	}
	after, _ := os.ReadFile(filepath.Join(s.Dir, "chats", c.ID+".json"))
	if string(before) != string(after) {
		t.Fatal("old document changed")
	}
}
func TestUnsafeImagePaths(t *testing.T) {
	s, _ := Open(t.TempDir())
	for _, p := range []string{"../../secret.png", "C:/secret.png", "/secret.png", "images/../secret.png", `images\x\a.png`} {
		c := chat.New()
		c.Attachments = []chat.Attachment{{ID: chat.ID(), Path: p}}
		if _, e := s.Save(SaveInput{Chat: c}); e == nil {
			t.Fatalf("accepted %q", p)
		}
	}
}
func TestSymlinkEscape(t *testing.T) {
	s, _ := Open(t.TempDir())
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "shot.png"), []byte("private"), 0600)
	c := chat.New()
	os.Symlink(outside, filepath.Join(s.Dir, "images", c.ID))
	a := chat.Attachment{ID: chat.ID(), Path: "images/" + c.ID + "/shot.png"}
	c.Attachments = []chat.Attachment{a}
	if _, e := s.Save(SaveInput{Chat: c}); e == nil {
		t.Fatal("accepted escaping link")
	}
}
