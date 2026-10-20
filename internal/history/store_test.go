package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/97Dmitry/solveo/internal/chat"
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
	if err := os.WriteFile(filepath.Join(s.Dir, "chats", id+".json"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
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
	if err := json.Unmarshal(c.ProviderState, &state); err != nil {
		t.Fatal(err)
	}
	if state["session_id"] != id {
		t.Fatal(state)
	}
}
func TestFailedCopyPreservesOldDocument(t *testing.T) {
	s, _ := Open(t.TempDir())
	c := chat.New()
	if _, err := s.Save(SaveInput{Chat: c}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(s.Dir, "chats", c.ID+".json"))
	p := filepath.Join(t.TempDir(), "a.png")
	if err := os.WriteFile(p, []byte("png"), 0600); err != nil {
		t.Fatal(err)
	}
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
	if err := os.WriteFile(filepath.Join(outside, "shot.png"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	c := chat.New()
	if err := os.Symlink(outside, filepath.Join(s.Dir, "images", c.ID)); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	a := chat.Attachment{ID: chat.ID(), Path: "images/" + c.ID + "/shot.png"}
	c.Attachments = []chat.Attachment{a}
	if _, e := s.Save(SaveInput{Chat: c}); e == nil {
		t.Fatal("accepted escaping link")
	}
}

func TestImagePathThroughArchiveAlias(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "archive")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(target, alias); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	s, err := Open(alias)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(base, "shot.png")
	if err := os.WriteFile(source, []byte("png"), 0600); err != nil {
		t.Fatal(err)
	}
	c := chat.New()
	a := chat.Attachment{ID: chat.ID()}
	c.Attachments = []chat.Attachment{a}
	if _, err := s.Save(SaveInput{Chat: c, Sources: []Source{{a.ID, source}}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ImagePath(c.ID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(got)
	if err != nil || string(data) != "png" {
		t.Fatalf("archived image unreadable: %q, %v", data, err)
	}
}

func TestAttachmentPathValidatesInMemoryMetadata(t *testing.T) {
	s, _ := Open(t.TempDir())
	source := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(source, []byte("png"), 0600); err != nil {
		t.Fatal(err)
	}
	c := chat.New()
	a := chat.Attachment{ID: chat.ID()}
	c.Attachments = []chat.Attachment{a}
	saved, err := s.Save(SaveInput{Chat: c, Sources: []Source{{a.ID, source}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AttachmentPath(saved.ID, saved.Attachments[0]); err != nil {
		t.Fatal(err)
	}
	other := chat.New()
	for _, bad := range []chat.Attachment{
		{ID: a.ID, Path: "../../secret.png"},
		{ID: a.ID, Path: "images/" + saved.ID + "/missing.png"},
		saved.Attachments[0],
	} {
		chatID := saved.ID
		if bad == saved.Attachments[0] {
			chatID = other.ID // a path belonging to another chat
		}
		if _, err := s.AttachmentPath(chatID, bad); err == nil {
			t.Fatalf("accepted %q for chat %s", bad.Path, chatID)
		}
	}
}

func TestLoadNormalizesTimesToUTC(t *testing.T) {
	s, _ := Open(t.TempDir())
	id := "12345678-1234-1234-1234-123456789abd"
	data := `{"version":1,"thread_id":"` + id + `","created_at":"2026-10-21T04:30:00+06:00","updated_at":"2026-10-21T05:00:00+06:00","messages":[]}`
	if err := os.WriteFile(filepath.Join(s.Dir, "chats", id+".json"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 20, 22, 30, 0, 0, time.UTC)
	if c.CreatedAt.Location() != time.UTC || !c.CreatedAt.Equal(want) || c.UpdatedAt.Location() != time.UTC {
		t.Fatalf("times not normalized to UTC: %v %v", c.CreatedAt, c.UpdatedAt)
	}
	saved, err := s.Save(SaveInput{Chat: c})
	if err != nil {
		t.Fatal(err)
	}
	if saved.UpdatedAt.Location() != time.UTC {
		t.Fatalf("saved time is not UTC: %v", saved.UpdatedAt)
	}
}
